package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/collision"
)

// neighborCacher 给"找邻居"加一层**降频缓存**（方案 A）。
//
// 核心洞察：ORCA 的邻居集合不需要每 tick 重算。
// τ=0.5s 是 10 个 tick 的避让窗口，邻居表滞后几个 tick 完全可接受
// （最多让避让决策基于"0.1~0.2 秒前谁在我附近"，而位置仍是实时的）。
//
// 关键设计：**缓存"有谁"，但位置/速度每次实时取**。
// 只缓存实体 id 列表，位置从 ECS 现读——否则会拿过期位置做避让，
// 那才是真的会出错（两个实体擦身而过时用的都是对方几个 tick 前的位置）。
//
// 成本模型：
//
//	刷新 tick：O(查询)         —— 与原来相同
//	非刷新 tick：O(缓存邻居数)  —— 只做 id→组件读取，不碰空间索引
//
// 所以 N tick 刷新一次，查询成本降为 1/N。
type neighborCacher struct {
	// cached：实体 → 邻居 id 列表（只在刷新 tick 重建）。
	cached map[ecs.Entity][]ecs.Entity
	// Every：每多少 tick 刷新一次（≤1 = 不缓存，等价于现状）。
	Every int
	// phase：内部计数，用于判断本 tick 是否刷新。
	phase int
	// refreshTick：上一次刷新的全局 tick（便于测试断言）。
	refreshTick int64
	// refreshes：累计刷新次数（观测用）。
	refreshes int
}

func newNeighborCacher(every int) *neighborCacher {
	return &neighborCacher{cached: make(map[ecs.Entity][]ecs.Entity), Every: every}
}

// shouldRefresh 本 tick 是否需要重算邻居表。
func (c *neighborCacher) shouldRefresh() bool {
	if c == nil || c.Every <= 1 {
		return true
	}
	c.phase++
	// phase==1 时先刷一次（保证第一帧就有邻居），之后每 Every tick 刷一次。
	return c.phase == 1 || c.phase%c.Every == 0
}

// beginRefresh 开始一轮刷新（清空映射但**复用底层切片**，避免每轮分配）。
func (c *neighborCacher) beginRefresh() {
	if c == nil {
		return
	}
	for k, v := range c.cached {
		c.cached[k] = v[:0]
	}
	c.refreshes++
}

// put 记录一个实体的邻居（刷新阶段调用）。
func (c *neighborCacher) put(e ecs.Entity, neighbors []collision.Neighbor) {
	if c == nil {
		return
	}
	buf := c.cached[e]
	buf = buf[:0]
	for _, n := range neighbors {
		buf = append(buf, n.Entity)
	}
	c.cached[e] = buf
}

// get 取一个实体的缓存邻居 id 列表。
func (c *neighborCacher) get(e ecs.Entity) []ecs.Entity {
	if c == nil {
		return nil
	}
	return c.cached[e]
}
