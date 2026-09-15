package main

import "testing"

// 预测诊断行应当在 predicting=true 时出现在状态行上方。
func TestHudPredictionLineRenders(t *testing.T) {
	w := newWorld()
	w.width, w.height = 16, 16
	f := newFrame(80, 12)
	drawHUD(f, w, hudState{
		overlay: true, cam: camDeadZone,
		predicting: true,
		predX:      12.34, predY: 5.67,
		predErr: 0.123, predCorr: 3, predSnaps: 1,
	})
	// 状态行在 f.h-2，预测行在 f.h-3
	row := f.h - 3
	// 宽字符（中文）占两列，第二列是填充位——拼接时跳过 \x00 再判断，
	// 否则会看到 "预\x00测\x00" 这种中间隔了填充位的假象。
	line := ""
	for x := 0; x < f.w; x++ {
		if ch := f.cells[row*f.w+x].ch; ch != 0 {
			line += string(ch)
		}
	}
	t.Logf("预测行: %q", line)
	if !contains(line, "预测") || !contains(line, "偏差") {
		t.Fatalf("预测诊断行未渲染: %q", line)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
