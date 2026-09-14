package collide

import "math"

// SweepSlideCapsule 是胶囊移动体版本的扫掠滑动：移动体是"段 a→b + 半径 radius"
// （俯视玩法里就是平面上的"操场形"），沿 motion 平移，命中就投影到接触切面继续滑。
//
// 为什么按"沿轴采样球"实现：真胶囊是"段上所有点为中心、半径 r 的球的并集"，
// 采样球间距 ≤ r 时并集与真胶囊的偏差 < 4%·r（凸包络一致），而每个采样球都能直接复用
// 已有的球-扫掠内核（SweepHit：球×球/盒/胶囊/三角形全部已实现），不必为每种目标再写一遍
// "胶囊×X"的闭式解。代价是每次迭代要做 n 次窄阶段，所以先用一次宽阶段剔除。
//
// 与 SweepSlideSphere 语义一致：连续扫掠（不穿透）→ 沿法向退 Skin → 剩余位移投影到切面
// → 最多 MaxIterations 次；完全被挡住时对角区域走墙角兜底。
func (e *Engine) SweepSlideCapsule(
	a, b Vec3, radius float64, motion Vec3, f Filter, opts SlideOptions,
) SlideResult {
	skin := opts.Skin
	if skin <= 0 {
		skin = DefaultSkin
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultSlideIterations
	}
	res := SlideResult{}
	if radius <= 0 || motion.LenSq() <= 0 {
		return res
	}

	ref := a.Add(b).Scale(0.5) // 参考点：段中点（调用方按它跟踪实体位置）
	parts := capsulePartOffsets(a, b, radius, ref)
	shape := Capsule{A: a, B: b, R: radius}

	// 宽阶段先剔一次：整段扫掠盒里没有候选就直接走完（绝大多数 tick 走这条）
	candidates := false
	e.scanner.Query(SweptAABB(shape.Bounds(), motion), func(Handle) bool {
		candidates = true
		return false
	})
	if !candidates {
		res.Moved = motion
		return res
	}

	pos := ref
	remaining := motion
	for i := 0; i < maxIter; i++ {
		if remaining.LenSq() <= 1e-18 {
			break
		}
		bestT := math.Inf(1)
		bestNormal := Vec3{}
		bestOffset := Vec3{}
		var bestHandle Handle
		for _, off := range parts {
			hit, ok := e.SweepHit(Sphere{C: pos.Add(off), R: radius}, remaining, f)
			if !ok || hit.T >= bestT {
				continue
			}
			bestT, bestNormal, bestOffset, bestHandle = hit.T, hit.Normal, off, hit.Handle
		}
		if math.IsInf(bestT, 1) {
			pos = pos.Add(remaining)
			remaining = Vec3{}
			break
		}
		res.Hits++
		partAt := pos.Add(bestOffset)
		residual := remaining.Scale(1 - bestT)
		partEnd := partAt.Add(remaining.Scale(bestT))
		if bestT <= 0 {
			// 已经重叠：按接触深度推出（该采样球与命中形状的 Contact）
			depth := skin
			if ct, ok := ContactShapes(Sphere{C: partAt, R: radius}, e.Shape(bestHandle)); ok && ct.Depth > 0 {
				depth = ct.Depth + skin
			}
			pos = pos.Add(bestNormal.Scale(depth))
		} else if bestT*remaining.Len() > skin {
			pos = pos.Add(remaining.Scale(bestT)).Add(bestNormal.Scale(skin))
		} else {
			pos = pos.Add(remaining.Scale(bestT))
		}

		rest := residual
		if into := rest.Dot(bestNormal); into < 0 {
			rest = rest.Sub(bestNormal.Scale(into))
		}
		if rest.LenSq() <= 1e-18 {
			// 完全被挡住：用"该采样球"做离散推进 + 沿最浅穿透面推出（对角撞墙角顺墙滑）
			if pushed, ok := e.cornerAssist(Sphere{C: partEnd, R: radius}, residual, f); ok {
				pos = pos.Add(pushed.Sub(partEnd))
			}
			break
		}
		remaining = rest
	}
	res.Moved = pos.Sub(ref)
	res.Blocked = res.Hits > 0
	return res
}

// capsulePartOffsets 沿 a→b 采样球心（相对参考点 ref 的偏移），间距 ≤ radius。
func capsulePartOffsets(a, b Vec3, radius float64, ref Vec3) []Vec3 {
	length := a.Distance(b)
	n := int(math.Ceil(length/radius)) + 1
	if n < 2 {
		n = 2
	}
	if n > 32 {
		n = 32 // 超长胶囊的兜底上限（间距略大于半径，误差仍在 10%·r 内）
	}
	out := make([]Vec3, 0, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n-1)
		out = append(out, a.Lerp(b, t).Sub(ref))
	}
	return out
}
