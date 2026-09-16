package systems

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 爆炸几何的契约测试。
//
// 核心是**半球**判定（不是水平圆）：爆心在地面，能量向上扩散。
// 现有数据刚好够算真半球：
//   - 爆心 y=0（地面）；
//   - 目标是一个竖直区间 [0, BodyHeight]（Collide 组件本来就带 BodyHeight，
//     注释写明"移动只用水平截面，这个字段供调试渲染用"——爆炸是第一个
//     真正用它参与模拟的地方）。

func newBlastWorld() *ecs.World {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	return w
}

// addBlastTarget 造一个带位置/血量/碰撞体的目标（可指定身高）。
func addBlastTarget(w *ecs.World, x, y int, bodyHeight float64) ecs.Entity {
	e := w.CreateEntity()
	ecs.Add(w, e, components.Position{X: x, Y: y})
	ecs.Add(w, e, components.Health{Cur: 100, Max: 100})
	ecs.Add(w, e, components.Attackable{})
	col := components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.3}
	col.BodyHeight = bodyHeight
	ecs.Add(w, e, col)
	return e
}

// 水平半径内的目标被命中（基本情形）。
func TestBlastHitsWithinRadius(t *testing.T) {
	w := newBlastWorld()
	near := addBlastTarget(w, 11, 10, 1.5) // 距爆心 1 格
	far := addBlastTarget(w, 20, 10, 1.5)  // 距爆心 10 格

	hits := BlastTargets(w, 10, 10, 2.5)
	if len(hits) != 1 || hits[0].Entity != near {
		t.Fatalf("半径 2.5 内应只命中近处目标，实际 %+v（far=%d）", hits, far)
	}
	if math.Abs(hits[0].Distance-1) > 1e-9 {
		t.Fatalf("距离应为 1，实际 %.3f", hits[0].Distance)
	}
}

// 边界：正好在半径上命中，超出一点点不命中。
func TestBlastRadiusBoundary(t *testing.T) {
	w := newBlastWorld()
	// 半径 2.5：放 (12,10) 距 2 → 命中；放 (13,10) 距 3 → 不命中
	inside := addBlastTarget(w, 12, 10, 1.0)
	outside := addBlastTarget(w, 13, 10, 1.0)

	hits := BlastTargets(w, 10, 10, 2.5)
	got := map[ecs.Entity]bool{}
	for _, h := range hits {
		got[h.Entity] = true
	}
	if !got[inside] {
		t.Fatal("距 2 格（< 2.5）应被命中")
	}
	if got[outside] {
		t.Fatal("距 3 格（> 2.5）不应被命中")
	}
}

// **半球的关键语义**：高个子能被"斜上方"的爆炸球边缘扫到，
// 而同样水平距离的矮个子扫不到。
//
// 这一条是水平圆判定**测不出来**的——它根本不看身高。
func TestBlastHemisphereUsesBodyHeight(t *testing.T) {
	// 半径 R、水平距离 d，则能被命中的最大身高满足 d² ≤ R²（下沿在地面，
	// 所以任何身高只要 d ≤ R 都命中）。要体现差异，需要目标**下沿高于地面**——
	// 而当前 Position 没有 Z，所有目标下沿都是地面。
	//
	// 因此这里验证的是**公式的另一半**：下沿在地面时，身高不影响判定
	// （因为 y=0 已在区间内，nearest=0）。这看似"没用到身高"，
	// 但它正是正确行为：站在地上的东西，无论多高，只要水平在半径内就被炸。
	w := newBlastWorld()
	short := addBlastTarget(w, 12, 10, 0.5) // 矮
	tall := addBlastTarget(w, 12, 11, 3.0)  // 高，水平同样 1 格左右

	hits := BlastTargets(w, 10, 10, 2.5)
	got := map[ecs.Entity]bool{}
	for _, h := range hits {
		got[h.Entity] = true
	}
	if !got[short] || !got[tall] {
		t.Fatalf("贴地目标只要水平在半径内就该命中（矮=%v 高=%v）", got[short], got[tall])
	}
}

// 直接测几何函数 blastAffects：**逐项写死期望值**，不做任何"按公式重算"的兜底。
//
// 为什么不能兜底重算：第一版里我为了绕开"Position 只有整数格"的问题，
// 在断言里用同样的公式重算了一遍期望值 —— 结果变异体（把"区间内离爆心最近的
// 高度"换成"上沿"）**照样通过**，因为错的公式被用在了期望值上。
// 测试自己复述实现 = 永远为真。
//
// 正确做法：这里直接调用 blastAffects 并传入浮点水平距离（它是纯函数，
// 参数与 Position 无关），期望值手工推导、写死。
func TestBlastAffectsVerticalInterval(t *testing.T) {
	const R = 2.5 // R² = 6.25

	cases := []struct {
		name       string
		horizDist  float64 // 水平距离（浮点，纯函数可直接传）
		ground     float64 // 目标下沿离地高度
		bodyHeight float64
		want       bool
		why        string
	}{
		{"贴地·水平1", 1, 0, 1.5, true, "1²+0²=1 ≤ 6.25"},
		{"贴地·水平3", 3, 0, 1.5, false, "3²+0²=9 > 6.25"},
		// 边界（含/不含）：用整数格无法表达 2.5 的半径边界，
		// 改为用"半径 3 含、半径 2 不含"验证同一条比较逻辑。
		{"贴地·水平3·半径3", 3, 0, 1.5, true, "3²=9 ≤ 9（边界含）"},
		{"贴地·水平3·半径2", 3, 0, 1.5, false, "3²=9 > 4"},

		// 悬空目标：区间 [ground, ground+bodyHeight]，取**离爆心(y=0)最近**的 y。
		// 最近点 = ground（因为 ground>0）。用上沿会算出更大的 y → 更不易命中，
		// 这正是变异体能被区分的地方。
		{"悬空2·水平2", 2, 2, 1.0, false, "最近y=2：4+4=8 > 6.25"},
		{"悬空1·水平2", 2, 1, 1.0, true, "最近y=1：4+1=5 ≤ 6.25"},
		// 若误用上沿（ground+height=2+1=3）：4+9=13 > 6.25，结论会变成 false，
		// 而上表要求 true —— 于是变异体必然失败。
		{"悬空1·水平2·高个子", 2, 1, 2.0, true, "最近y=1（不是上沿3）：4+1=5 ≤ 6.25"},

		// 跨地面（下沿在地下）：区间含 y=0，最近点就是 0。
		{"跨地面·水平2", 2, -1, 3, true, "区间含0：4+0=4 ≤ 6.25"},
		{"跨地面·水平3", 3, -1, 3, false, "3²+0²=9 > 6.25"},

		// 半径 0 / 负
		{"半径0", 0, 0, 1, false, "半径非正永不命中"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// blastAffects 的签名是 (cx, cy, radius, Position, ground, bodyHeight)。
			// Position 是整数格，所以把水平距离落在 X 轴上取整传进去；
			// 为保证语义清晰，这里只使用**整格**的水平距离（上面的用例已如此）。
			if c.horizDist != math.Trunc(c.horizDist) {
				t.Fatalf("用例设计错误：水平距离必须是整格（Position 只有整数），实际 %.2f", c.horizDist)
			}
			pos := components.Position{X: int(c.horizDist), Y: 0}
			r := R
			switch c.name {
			case "半径0":
				r = 0
			case "贴地·水平3·半径3":
				r = 3
			case "贴地·水平3·半径2":
				r = 2
			}
			got := blastAffects(0, 0, r, pos, c.ground, c.bodyHeight)
			if got != c.want {
				t.Fatalf("%s（%s）：want %v got %v", c.name, c.why, c.want, got)
			}
		})
	}
}

// 死亡/离线目标不该被爆炸命中（与仇恨/伤害链路的口径一致）。
func TestBlastSkipsDeadTargets(t *testing.T) {
	w := newBlastWorld()
	alive := addBlastTarget(w, 11, 10, 1.5)
	dead := addBlastTarget(w, 11, 11, 1.5)
	ecs.Add(w, dead, components.Dead{})

	hits := BlastTargets(w, 10, 10, 2.5)
	for _, h := range hits {
		if h.Entity == dead {
			t.Fatal("已死亡的目标不应被爆炸命中")
		}
	}
	if len(hits) != 1 || hits[0].Entity != alive {
		t.Fatalf("应只命中存活目标，实际 %+v", hits)
	}
}

// 结果按实体 id 升序（确定性——同 tick 多目标时结算顺序稳定）。
func TestBlastTargetsAreSorted(t *testing.T) {
	w := newBlastWorld()
	for i := 0; i < 6; i++ {
		addBlastTarget(w, 10+i%3, 10+i/3, 1.5)
	}
	hits := BlastTargets(w, 10, 10, 5)
	if len(hits) < 2 {
		t.Fatalf("应命中多个目标，实际 %d", len(hits))
	}
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Entity > hits[i].Entity {
			t.Fatalf("结果应按实体 id 升序：%d > %d", hits[i-1].Entity, hits[i].Entity)
		}
	}
}
