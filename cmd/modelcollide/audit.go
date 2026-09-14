package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"starve/pkg/modelcollide"
)

// issue 是一条验收发现。Fatal 的问题会直接让流水线失败；
// 非 Fatal 的只是警告（-strict 下升级成失败）。
type issue struct {
	Entity string
	Fatal  bool
	Msg    string
}

func (i issue) String() string {
	level := "警告"
	if i.Fatal {
		level = "错误"
	}
	if i.Entity == "" {
		return fmt.Sprintf("[%s] %s", level, i.Msg)
	}
	return fmt.Sprintf("[%s] %s: %s", level, i.Entity, i.Msg)
}

// 验收阈值（可用清单里的同名字段逐条覆盖）。
const (
	defaultMaxOutside      = 0.10 // body 胶囊：取样带内落在胶囊外的比例上限
	defaultMaxCenterOffset = 0.10 // 模型形心到原点的水平偏差上限（格）
	defaultMaxGroundOffset = 0.05 // 非 box 形状：模型原点离地偏差上限（格）
)

// auditProxy 对一条推导结果做**自动验收**。它检查的都是"手工看很难发现、
// 但一旦错了就静默错位"的性质：
//
//  1. **朝向**：客户端把模型局部 +Z 对准实体朝向（IsoCamera3D.FacingYaw），
//     而服务端把碰撞胶囊摆在朝向上。所以 body 胶囊要求模型水平长轴 = 局部 +Z；
//     长轴落在 X 的模型，碰撞会与渲染模型差 90°，且没有任何运行时报错。
//  2. **贴合**：body 胶囊的契约是"长宽刚好包住模型"（见 shape.go 注释），
//     所以取样带的顶点绝大部分应当落在胶囊内。直立胶囊取的是躯干短边、
//     手臂本来就在半径外（实测玩家 87%），所以**不卡**这一条，只打印。
//  3. **原点**：圆/胶囊形状把模型原点当"占格中心 + 地面"，原点不在脚底的模型
//     （实测 alchemy-engine 的 minY=-0.95）竖直位置会整体偏；box 用锚点+尺寸，不受影响。
//  4. **居中**：形心离原点太远说明模型没居中，应该让美术重新导出，而不是把半径放大去补。
func auditProxy(e Entry, p modelcollide.Proxy, out *[]issue) {
	add := func(fatal bool, format string, args ...any) {
		*out = append(*out, issue{Entity: e.Entity, Fatal: fatal, Msg: fmt.Sprintf(format, args...)})
	}
	maxOutside := defaultMaxOutside
	if e.MaxOutside != nil {
		maxOutside = *e.MaxOutside
	}
	maxCenter := defaultMaxCenterOffset
	if e.MaxCenterOffset != nil {
		maxCenter = *e.MaxCenterOffset
	}

	// 1. 朝向：body 胶囊的长轴必须是局部 +Z
	if e.CapsuleAxis == "body" && p.LongAxis != "z" {
		add(true, "模型水平长轴在 %s，但客户端把**局部 +Z** 对准朝向（IsoCamera3D.FacingYaw）——"+
			"这样服务端碰撞胶囊会与渲染模型差 90°。修法：让美术把模型转成 +Z 朝前再导出"+
			"（或改 models.json 的胶囊轴规则并同步客户端契约）", strings.ToUpper(p.LongAxis))
	}

	// 2. 贴合：只有 body 胶囊的契约是"刚好包住"
	if e.CapsuleAxis == "body" && p.OutsideRatio > maxOutside {
		add(false, "取样带内有 %.1f%% 的顶点落在胶囊外（上限 %.0f%%）：检查 band 是否把腿/角也算进来了，"+
			"或模型不是沿长轴的胶囊形", p.OutsideRatio*100, maxOutside*100)
	}

	// 3. 原点：非 box 形状才受竖直位置影响
	if e.Shape != "box" {
		if off := p.Bounds[1][0]; off < -defaultMaxGroundOffset || off > defaultMaxGroundOffset {
			add(false, "模型原点离地 %+.3f 格（应在 0 附近）：圆/胶囊形状会把竖直位置整体带偏，"+
				"请让美术把原点放到底面", off)
		}
	}

	// 4. 居中
	if off := p.CenterOffset; abs(off[0]) > maxCenter || abs(off[1]) > maxCenter {
		add(false, "形心相对原点偏 (%.3f,%.3f) 格（上限 %.2f）：模型没居中，应当重导出而不是把半径算大",
			off[0], off[1], maxCenter)
	}
}

// scanAssets 在清单声明的 scan 模式里找出**没被清单引用**的模型。
// 这不是错误而是线索：新加的动物/资源模型往往就是"还没登记"，
// 而动画片段/同款贴图变体属于合法未引用，用 scan_ignore 明确豁免。
func scanAssets(m Manifest, assetRoot string, verbose bool) ([]string, error) {
	if len(m.Scan) == 0 {
		return nil, nil
	}
	referenced := make(map[string]bool, len(m.Models))
	for _, e := range m.Models {
		if e.Model != "" {
			referenced[filepath.ToSlash(e.Model)] = true
		}
	}
	ignored := make([]string, 0, len(m.ScanIgnore))
	ignored = append(ignored, m.ScanIgnore...)

	var unregistered []string
	for _, pattern := range m.Scan {
		matches, err := filepath.Glob(filepath.Join(assetRoot, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, fmt.Errorf("scan 模式 %q 非法: %w", pattern, err)
		}
		for _, path := range matches {
			rel, err := filepath.Rel(assetRoot, path)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if referenced[rel] || matchAny(ignored, rel) {
				continue
			}
			unregistered = append(unregistered, rel)
		}
	}
	sort.Strings(unregistered)
	return unregistered, nil
}

// matchAny 逐个 glob 匹配（相对 asset_root 的路径）。
func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(filepath.FromSlash(p), filepath.FromSlash(rel)); ok {
			return true
		}
		// 支持 "dir/**" 这样的前缀写法
		if strings.HasSuffix(p, "/**") && strings.HasPrefix(rel, strings.TrimSuffix(p, "**")) {
			return true
		}
	}
	return false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// hashFile 模型内容摘要（写进生成文件，用来区分"改了模型"和"改了规则"）。
func hashFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
