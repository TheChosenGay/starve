package main

import (
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
type assetLock struct {
	Repo     string `json:"repo"`      // 资产仓地址（来自清单的 asset_repo）
	Commit   string `json:"commit"`    // 计算这份派生值时的资产仓 commit
	PinnedAt string `json:"pinned_at"` // 记录时间（人看）
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
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("读不到 %s 的 git HEAD（不是 git 检出？）: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// pinAssets 把当前资产仓的 HEAD 写进 lock（`make assets-pin` 调它）。
func pinAssets(root string, manifest Manifest, assetRoot string) (assetLock, error) {
	head, err := gitHead(assetRoot)
	if err != nil {
		return assetLock{}, err
	}
	repo := manifest.AssetRepo
	if repo == "" {
		repo = "(未在清单里写 asset_repo)"
	}
	l := assetLock{Repo: repo, Commit: head, PinnedAt: time.Now().Format(time.RFC3339)}
	if err := writeLock(root, l); err != nil {
		return assetLock{}, err
	}
	return l, nil
}

// checkPin 比对"资产仓当前 HEAD"与 lock。不一致说明：改了模型但没更新 lock
// （本地开发的常态，所以只是警告），或者 CI 检出错了版本。
// 资产目录不是 git 检出（例如从 tarball 解出来的）时跳过——只做提示。
func checkPin(root string, manifest Manifest, assetRoot string, out *[]issue) {
	lock, ok, err := loadLock(root)
	if err != nil {
		*out = append(*out, issue{Msg: err.Error()})
		return
	}
	if !ok {
		return // 还没钉过：不打扰
	}
	head, err := gitHead(assetRoot)
	if err != nil {
		*out = append(*out, issue{Msg: fmt.Sprintf(
			"资产目录不是 git 检出，无法核对钉住的 commit（%s）——CI 里请按 %s 检出",
			short(lock.Commit), assetLockPath)})
		return
	}
	if head == lock.Commit {
		return
	}
	*out = append(*out, issue{Msg: fmt.Sprintf(
		"资产漂移：资产仓当前 %s，而 %s 钉的是 %s。改了模型/资产就重新推导并跑 `make assets-pin`；"+
			"CI 里应当按钉住的 commit 检出（GATE_ASSET_ROOT 指向那份检出）",
		short(head), assetLockPath, short(lock.Commit))})
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
