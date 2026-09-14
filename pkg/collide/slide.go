package collide

import "math"

// DefaultSkin 是扫掠滑动的默认接触回退距离（与调用方坐标单位一致，游戏里是格）。
// 不留 skin 时，移动到"刚好相切"后下一帧的扫掠会在 t=0 就报接触，滑动会卡死。
const DefaultSkin = 1e-3

// DefaultSlideIterations 是扫掠滑动默认的最大接触消解次数。
// 一次接触（贴墙滑）用 1 次；墙角两条边的夹角需要 2 次；留 4 次给多障碍。
const DefaultSlideIterations = 4

// CornerProbeStep 是墙角兜底（cornerAssist）的探测步长（格）。
// 被完全挡住时按这个步长离散推进，找到第一个重叠位置再沿最浅穿透方向推出。
// 步长必须小于最薄障碍的"厚度 + 身体直径"，否则离散推进可能越过它（隧穿）。
const CornerProbeStep = 0.1

// SlideOptions 是扫掠滑动的数值参数，零值即默认（Skin=1e-3、MaxIterations=4）。
type SlideOptions struct {
	// Skin 每次接触后沿法向回退的距离（格）。
	Skin float64
	// MaxIterations 最多消解几次接触；用完后剩余位移直接丢弃（宁可少走，不穿墙）。
	MaxIterations int
}

// SlideResult 是扫掠滑动的结果。
type SlideResult struct {
	Moved   Vec3 // 实际发生的位移；方向可能被切面投影改变，长度 ≤ 请求位移
	Hits    int  // 接触次数（0 = 一路畅通）
	Blocked bool // 是否发生过接触（Moved 通常短于请求位移）
}

// SweepSlideSphere 让球 s 沿 motion 平移，撞到东西就把剩余位移投影到接触切面继续滑，
// 返回实际发生的位移与接触次数。移动体目前只支持球（与 SweepShapes 的能力一致）。
//
// 这是"角色贴墙走"的内核：连续扫掠保证不穿墙（含高速），切面投影保证撞上之后是
// 顺着表面走而不是原地停住。调用方负责把返回位移加到自己的位置上。
//
// 语义细节：
//   - 初始就重叠（t=0）时按接触深度沿法向推出后再继续滑，所以"卡在障碍里"能自愈；
//   - 每次接触回退 Skin，避免共面精度问题导致的滑动停滞；
//   - 迭代次数用尽后丢弃剩余位移（不会为了走完而穿过障碍）。
func (e *Engine) SweepSlideSphere(s Sphere, motion Vec3, f Filter, opts SlideOptions) SlideResult {
	skin := opts.Skin
	if skin <= 0 {
		skin = DefaultSkin
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultSlideIterations
	}
	res := SlideResult{}
	if motion.LenSq() <= 0 || s.R <= 0 {
		return res
	}

	pos := s.C
	remaining := motion
	for i := 0; i < maxIter; i++ {
		if remaining.LenSq() <= 1e-18 {
			break
		}
		hit, ok := e.SweepHit(Sphere{C: pos, R: s.R}, remaining, f)
		if !ok {
			pos = pos.Add(remaining)
			break
		}
		res.Hits++
		if hit.T <= 0 {
			// 已经重叠：按接触深度推出（未实现接触对时退化为只推 skin）。
			depth := skin
			if ct, ok := ContactShapes(Sphere{C: pos, R: s.R}, e.Shape(hit.Handle)); ok && ct.Depth > 0 {
				depth = ct.Depth + skin
			}
			pos = pos.Add(hit.Normal.Scale(depth))
		} else if hit.Dist > skin {
			// 推进到接触位置，再沿法向（指向移动体）退出 skin 脱离表面。
			pos = pos.Add(remaining.Scale(hit.T)).Add(hit.Normal.Scale(skin))
		} else {
			// 接触点太靠近起点：只推进到接触位置，不额外回退（否则会倒退）。
			pos = pos.Add(remaining.Scale(hit.T))
		}

		// 剩余位移投影到接触切面：去掉法向分量，只保留沿表面滑走的部分。
		residual := remaining.Scale(1 - hit.T) // 还没走完的那部分（未投影）
		rest := residual
		if normal := rest.Dot(hit.Normal); normal < 0 {
			rest = rest.Sub(hit.Normal.Scale(normal))
		}
		// 投影后一点都不剩（8 向输入正对角撞墙角时法向恰好与位移反向）：
		// 退化成"离散推进 + 沿最浅穿透面推出"，于是顺着墙面滑过去而不是钉死在角上。
		// 正对平面推时最浅穿透面就是该平面本身，结果仍然是停住（不改变正面撞墙语义）。
		if rest.LenSq() <= 1e-18 {
			if pushed, ok := e.cornerAssist(Sphere{C: pos, R: s.R}, residual, f); ok {
				pos = pushed
			}
			break
		}
		remaining = rest
	}
	res.Moved = pos.Sub(s.C)
	res.Blocked = res.Hits > 0
	return res
}

// cornerAssist 是"被完全挡住"时的兜底：按 CornerProbeStep 把剩余位移离散推进，
// 碰到第一个重叠位置就解掉穿透。步长固定且很小，所以不会一步跨过薄障碍（不隧穿），
// 只用来把"正对角撞墙角"从钉死改成顺着墙面滑开。
func (e *Engine) cornerAssist(s Sphere, motion Vec3, f Filter) (Vec3, bool) {
	total := motion.Len()
	if total <= 1e-9 {
		return s.C, false
	}
	steps := int(math.Ceil(total / CornerProbeStep))
	if steps < 1 {
		steps = 1
	}
	for i := 1; i <= steps; i++ {
		probe := Sphere{C: s.C.Add(motion.Scale(float64(i) / float64(steps))), R: s.R}
		pushed, ok := e.resolveProbe(probe, f)
		if ok {
			return pushed, true
		}
	}
	return s.C, false
}

// resolveProbe 把探针位置的穿透解掉：
//   - 目标是盒、且球心落在盒的"角区"（两个轴上都在外侧）时，沿较近的那个轴推到面外，
//     保留另一个轴的位置——于是 8 向输入正对角撞墙角会顺着墙面滑开，而不是原地钉死；
//   - 其余情况（平面、圆柱、球）按接触法向推出，正面撞就是停住。
func (e *Engine) resolveProbe(s Sphere, f Filter) (Vec3, bool) {
	center := s.C
	moved := false
	e.Overlap(s, f, func(r Result) bool {
		if r.Depth <= 0 {
			return true
		}
		if box, ok := e.Shape(r.Handle).(AABB); ok {
			if corner := pushOutBoxCorner(center, box, s.R); corner != center {
				center = corner
				moved = true
				return true
			}
		}
		center = center.Add(r.Normal.Scale(r.Depth))
		moved = true
		return true
	})
	return center, moved
}

// pushOutBoxCorner 处理"球心在盒角外侧"的浅穿透：沿较近的轴推到面外，另一轴不动。
// 不在角区（至少有一轴落在盒的跨度内）时原样返回，交给常规法向推出。
func pushOutBoxCorner(c Vec3, box AABB, r float64) Vec3 {
	outX := box.Min.X - c.X
	if c.X > box.Max.X {
		outX = c.X - box.Max.X
	}
	outZ := box.Min.Z - c.Z
	if c.Z > box.Max.Z {
		outZ = c.Z - box.Max.Z
	}
	if outX <= 0 || outZ <= 0 {
		return c // 不是角区（有一轴落在盒跨度内）
	}
	if outX <= outZ {
		if c.X < box.Min.X {
			c.X = box.Min.X - r
		} else {
			c.X = box.Max.X + r
		}
		return c
	}
	if c.Z < box.Min.Z {
		c.Z = box.Min.Z - r
	} else {
		c.Z = box.Max.Z + r
	}
	return c
}
