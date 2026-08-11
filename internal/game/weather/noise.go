package weather

import "math"

// hash64 确定性整数哈希（splitmix64 变体）：同 seed + 坐标 → 同值，跨平台稳定。
func hash64(seed uint64, xs ...int64) uint64 {
	h := seed ^ 0x9E3779B97F4A7C15
	for i, x := range xs {
		h ^= uint64(x) * (0x9E3779B97F4A7C15 + uint64(i)*0xBF58476D1CE4E5B9)
	}
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	h ^= h >> 31
	return h
}

// unit 把哈希值映射到 [0,1)。
func unit(h uint64) float32 { return float32(h>>33) / float32(1<<31) }

// value3 三维 value noise（三线性插值；t 为时间维，天气随时间平滑演化）。
func value3(seed uint64, x, y, t float32) float32 {
	ix := int64(math.Floor(float64(x)))
	iy := int64(math.Floor(float64(y)))
	it := int64(math.Floor(float64(t)))
	fx := x - float32(ix)
	fy := y - float32(iy)
	ft := t - float32(it)

	c000 := unit(hash64(seed, ix, iy, it))
	c100 := unit(hash64(seed, ix+1, iy, it))
	c010 := unit(hash64(seed, ix, iy+1, it))
	c110 := unit(hash64(seed, ix+1, iy+1, it))
	c001 := unit(hash64(seed, ix, iy, it+1))
	c101 := unit(hash64(seed, ix+1, iy, it+1))
	c011 := unit(hash64(seed, ix, iy+1, it+1))
	c111 := unit(hash64(seed, ix+1, iy+1, it+1))

	lerp := func(a, b, f float32) float32 { return a + (b-a)*f }
	c00 := lerp(c000, c100, fx)
	c10 := lerp(c010, c110, fx)
	c01 := lerp(c001, c101, fx)
	c11 := lerp(c011, c111, fx)
	c0 := lerp(c00, c10, fy)
	c1 := lerp(c01, c11, fy)
	return lerp(c0, c1, ft)
}

// fbm 分形叠加：多倍频 value noise 求和（确定性；t 随时间推进）。
func (s *Sampler) fbm(x, y, t float32, octaves int) float32 {
	amp := float32(1)
	freq := float32(1)
	sum := float32(0)
	norm := float32(0)
	for i := 0; i < octaves; i++ {
		sum += amp * value3(s.seed, x*freq, y*freq, t*freq)
		norm += amp
		amp *= 0.5
		freq *= 2
	}
	return sum / norm
}
