package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// assetLock 钉住"这份碰撞体是用哪一版资产算出来的"。
//
// 为什么要钉：派生值（configs/model_collision.json）进了服务端仓库，但它的输入
// （资产仓的模型）在另一个仓库里。没有这个 lock，就说不清"这个 collision_radius
// 对应哪一版模型"，也没法在 CI 里复现。
//
// 判定漂移用的是 **AssetsSHA256（内容）**，不是 Commit：
// 资产仓里改 README / 工作流也会换 commit，但那不影响任何碰撞体——
// 用 commit 判定会让服务端为一个纯文档提交收到一个毫无意义的 pin PR。
// Commit 仍然保留，供 CI 按它检出（内容摘要一致时，检出哪个 commit 都等价）。
type assetLock struct {
	Repo         string `json:"repo"`
	Commit       string `json:"commit"`
	AssetsSHA256 string `json:"assets_sha256"` // assets/ 目录的内容摘要
	PinnedAt     string `json:"pinned_at"`     // 记录时间（人看）
}

func loadLock(root string) (*assetLock, bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, assetLockPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var l assetLock
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false, fmt.Errorf("%s 解析失败: %w", assetLockPath, err)
	}
	return &l, true, nil
}

func writeLock(root string, l assetLock) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, assetLockPath), append(data, '\n'), 0o644)
}

// gitHead 取资产目录所在仓库的 HEAD（资产根通常是仓库的子目录，-C 也管用）。
func gitHead(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("读不到 %s 的 git HEAD（不是 git 检出？）: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// assetsDigest 算资产目录的内容摘要。
//
// 用 git 的树（每个 blob 的 sha + 相对路径）而不是逐字节读文件：533 MB 全读一遍太慢，
// 而 git 的 blob 哈希本身就是内容寻址的、跨机器稳定。
// `.` 这个 pathspec 把范围限制在资产目录（通常是仓库的子目录），
// 所以仓库里与资产无关的文件（README、工作流）不会影响摘要。
func assetsDigest(assetRoot string) (string, error) {
	out, err := exec.Command("git", "-C", assetRoot, "ls-tree", "-r", "HEAD", "--", ".").Output()
	if err != nil {
		return "", fmt.Errorf("算资产内容摘要失败（%s 不是 git 检出？）: %w", assetRoot, err)
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]), nil
}

// pinAssets 把当前资产内容与 commit 写进 lock（`make assets-pin` 调它）。
// 返回 (lock, 是否有变化)：**内容没变就不动 lock**——这是幂等的关键，
// 否则资产仓每多一个文档提交，服务端就会多一个只改 pin 的 PR。
func pinAssets(root string, manifest Manifest, assetRoot string) (assetLock, bool, error) {
	digest, err := assetsDigest(assetRoot)
	if err != nil {
		return assetLock{}, false, err
	}
	if cur, ok, err := loadLock(root); err != nil {
		return assetLock{}, false, err
	} else if ok && cur.AssetsSHA256 == digest {
		return *cur, false, nil
	}
	head, err := gitHead(assetRoot)
	if err != nil {
		return assetLock{}, false, err
	}
	repo := manifest.AssetRepo
	if repo == "" {
		repo = "(未在清单里写 asset_repo)"
	}
	l := assetLock{
		Repo:         repo,
		Commit:       head,
		AssetsSHA256: digest,
		PinnedAt:     time.Now().Format(time.RFC3339),
	}
	if err := writeLock(root, l); err != nil {
		return assetLock{}, false, err
	}
	return l, true, nil
}

// checkPin 比对"资产内容"与 lock。不一致说明：改了资产却没重新推导/没更新 lock
// （本地开发的常态，所以只是警告），或者 CI 检出错了版本。
// 资产目录不是 git 检出（例如从 tarball 解出来的）时给出提示，而不是静默放过。
func checkPin(root string, manifest Manifest, assetRoot string, out *[]issue) {
	lock, ok, err := loadLock(root)
	if err != nil {
		*out = append(*out, issue{Msg: err.Error()})
		return
	}
	if !ok {
		return // 还没钉过：不打扰
	}
	digest, err := assetsDigest(assetRoot)
	if err != nil {
		*out = append(*out, issue{Msg: fmt.Sprintf(
			"无法核对资产版本：%v（CI 里请按 %s 钉住的 commit 检出资产仓）", err, assetLockPath)})
		return
	}
	if lock.AssetsSHA256 == "" {
		// 旧版 lock 只有 commit：退回按 commit 比（下次 -pin 会补上摘要）
		if head, err := gitHead(assetRoot); err == nil && head != lock.Commit {
			*out = append(*out, issue{Msg: pinDriftMsg(short(head), short(lock.Commit))})
		}
		return
	}
	if digest != lock.AssetsSHA256 {
		head, _ := gitHead(assetRoot)
		*out = append(*out, issue{Msg: pinDriftMsg(
			"资产内容 "+short(digest), "资产内容 "+short(lock.AssetsSHA256)) +
			fmt.Sprintf("（当前 commit %s）", short(head))})
	}
}

func pinDriftMsg(have, want string) string {
	return fmt.Sprintf(
		"资产漂移：%s，而 %s 钉的是 %s。改了模型/资产就重新推导并跑 `make assets-pin`；"+
			"CI 里应当按钉住的版本检出（GATE_ASSET_ROOT 指向那份检出）", have, assetLockPath, want)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
