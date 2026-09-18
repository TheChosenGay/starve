package main

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMoveCorpusVectors 用跨端语料锁定服务端"一个 tick 的位移"。
//
// 语料由本包生成（cmd/movecorpus），期望值走的是**生产同款求解路径**，
// 所以这里断言的是"回归不变"：谁改了移动数学/碰撞/ORCA/坡度，这里会立刻红，
// 并且失败信息给出**场景名 + 输入 + 期望 vs 实际**，便于定位是哪一类。
//
// 容差取 0（精确相等）：生成与回放是同一份代码、同一台机器，float64 的
// 运算顺序完全一致，本来就该逐位相同；给容差反而会把"悄悄改了运算顺序"放过去。
// 跨端（C#）那边才需要容差，见 Starve.Core.Tests/MovementCorpusTests.cs。
func TestMoveCorpusVectors(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "move_corpus.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开语料失败: %v", err)
	}
	defer f.Close()

	cats := map[string]int{}
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// 首行是元信息 {"meta":{...}}：不参与回放，只用来核对条数/种子。
		if strings.HasPrefix(line, `{"meta"`) {
			continue
		}
		var s scenario
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("第 %d 条语料解析失败: %v", n+2, err)
		}
		x, y := step(s)
		if math.Abs(x-s.WantX) > 0 || math.Abs(y-s.WantY) > 0 {
			t.Fatalf(
				"场景 %s（%s）位移不一致：\n  输入 start=(%g,%g) dir=(%d,%d) speed=%g dt=%gms shapes=%d neighbors=%d\n  实际 (%.12f, %.12f)\n  语料 (%.12f, %.12f)",
				s.Name, s.Category, s.StartX, s.StartY, s.DX, s.DY, s.Speed, s.DTMS,
				len(s.Shapes), len(s.Neighbors), x, y, s.WantX, s.WantY)
		}
		cats[s.Category]++
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读语料失败: %v", err)
	}
	if n == 0 {
		t.Fatal("语料为空或没解析出任何场景")
	}
	t.Logf("服务端回放 %d 条全部一致；分类分布 %v", n, cats)
}
