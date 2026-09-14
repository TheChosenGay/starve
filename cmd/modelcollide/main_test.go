package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 配置门禁：生成文件里的每个 grants 都要与游戏配置一致。
// 只读配置，不需要客户端模型 → 可以在任何环境（CI）跑。
func TestConfigsMatchPipeline(t *testing.T) {
	if err := run("../..", "", false, true, false, false); err != nil {
		t.Fatalf("配置与流水线不一致: %v", err)
	}
}

// 模型门禁：重新读客户端模型推导，逐字段与生成文件对比。
// 抓"改了模型/清单但没跑 go run ./cmd/modelcollide -write"；客户端资产不在本机时跳过。
func TestModelsMatchGeneratedFile(t *testing.T) {
	manifest, err := loadManifest(filepath.Join("../..", manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("../..", manifest.AssetRoot)); err != nil {
		t.Skipf("客户端资产不在本机（%s），跳过模型重算", manifest.AssetRoot)
	}
	if err := run("../..", "", false, false, true, false); err != nil {
		t.Fatalf("模型重算与生成文件不一致: %v", err)
	}
}
