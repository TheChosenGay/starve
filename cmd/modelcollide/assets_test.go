package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn 在 dir 里跑一条 git 命令（测试用；全局 user.name/email 已配好）。
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newAssetRepo 造一个最小资产仓：<dir>/assets/models/a.glb + <dir>/README.md，已提交。
func newAssetRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "assets", "models", "a.glb"), "MODEL-A")
	writeFile(t, filepath.Join(dir, "README.md"), "v1")
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// 核心性质：摘要只跟 assets/ 的内容有关。
// 资产仓里改 README / 工作流也会换 commit，但那不该让服务端收到一个
// 毫无意义的 pin PR——所以判定漂移必须用内容摘要而不是 commit。
func TestAssetsDigestIgnoresUnrelatedCommits(t *testing.T) {
	repo := newAssetRepo(t)
	assets := filepath.Join(repo, "assets")

	before, err := assetsDigest(assets)
	if err != nil {
		t.Fatal(err)
	}
	headBefore, _ := gitHead(assets)

	// 只改 README（不影响任何碰撞体）并提交
	writeFile(t, filepath.Join(repo, "README.md"), "v2 改了文档")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "docs: 改 README")

	after, err := assetsDigest(assets)
	if err != nil {
		t.Fatal(err)
	}
	headAfter, _ := gitHead(assets)
	if headAfter == headBefore {
		t.Fatal("测试前提不成立：commit 应当变了")
	}
	if after != before {
		t.Fatal("只改 README 时资产内容摘要不应变化（否则会生成无意义的 pin PR）")
	}

	// 改真正的资产 → 摘要必须变
	writeFile(t, filepath.Join(assets, "models", "a.glb"), "MODEL-A-CHANGED")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "feat: 换模型")
	changed, err := assetsDigest(assets)
	if err != nil {
		t.Fatal(err)
	}
	if changed == before {
		t.Fatal("模型变了，摘要必须变")
	}
}

// -pin 幂等：内容没变就不动 lock（否则每次资产仓提交都会改 pinned_at → 生成 PR）。
func TestPinAssetsIsIdempotent(t *testing.T) {
	repo := newAssetRepo(t)
	assets := filepath.Join(repo, "assets")
	server := t.TempDir() // 假装是服务端仓根（只需要能写 configs/assets.lock.json）
	if err := os.MkdirAll(filepath.Join(server, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{AssetRepo: "https://example.invalid/asset-starve"}

	l1, changed, err := pinAssets(server, m, assets)
	if err != nil || !changed {
		t.Fatalf("首次 pin 应当写入: changed=%v err=%v", changed, err)
	}
	if l1.AssetsSHA256 == "" {
		t.Fatal("lock 里应当有内容摘要")
	}
	raw1, _ := os.ReadFile(filepath.Join(server, assetLockPath))

	// 第二次：内容没变 → 不写
	if _, changed, err := pinAssets(server, m, assets); err != nil || changed {
		t.Fatalf("内容未变时 pin 不应改动: changed=%v err=%v", changed, err)
	}
	raw2, _ := os.ReadFile(filepath.Join(server, assetLockPath))
	if string(raw1) != string(raw2) {
		t.Fatal("内容未变时 lock 文件不应被改写（pinned_at 也不该动）")
	}

	// 只改 README → 仍然不写
	writeFile(t, filepath.Join(repo, "README.md"), "v3")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "docs")
	if _, changed, err := pinAssets(server, m, assets); err != nil || changed {
		t.Fatalf("只改 README 时 pin 不应改动: changed=%v err=%v", changed, err)
	}

	// 改资产 → 必须写，并且 commit 字段更新到新 commit
	writeFile(t, filepath.Join(assets, "models", "a.glb"), "MODEL-B")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "feat: 换模型")
	l2, changed, err := pinAssets(server, m, assets)
	if err != nil || !changed {
		t.Fatalf("资产变了应当重新 pin: changed=%v err=%v", changed, err)
	}
	if l2.Commit == l1.Commit || l2.AssetsSHA256 == l1.AssetsSHA256 {
		t.Fatal("commit 与摘要都应当更新")
	}
}

// checkPin：只改 README 不报警；改资产要报警。
func TestCheckPinOnlyCaresAboutAssets(t *testing.T) {
	repo := newAssetRepo(t)
	assets := filepath.Join(repo, "assets")
	server := t.TempDir()
	if err := os.MkdirAll(filepath.Join(server, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{AssetRepo: "https://example.invalid/asset-starve"}
	if _, _, err := pinAssets(server, m, assets); err != nil {
		t.Fatal(err)
	}

	var issues []issue
	checkPin(server, m, assets, &issues)
	if len(issues) != 0 {
		t.Fatalf("刚 pin 完不该有漂移: %+v", issues)
	}

	// 只改 README → 依旧无漂移
	writeFile(t, filepath.Join(repo, "README.md"), "v9")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "docs")
	issues = nil
	checkPin(server, m, assets, &issues)
	if len(issues) != 0 {
		t.Fatalf("只改 README 不该报资产漂移: %+v", issues)
	}

	// 改资产 → 报漂移
	writeFile(t, filepath.Join(assets, "models", "a.glb"), "MODEL-C")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "feat")
	issues = nil
	checkPin(server, m, assets, &issues)
	if len(issues) != 1 {
		t.Fatalf("改了资产应当报 1 条漂移, got %+v", issues)
	}
}
