package world

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// CreatureOccupancySystem 把动物占格同步挂进系统流水线（order 与移动之后对齐）。
//
// 为什么做成系统而不是只在 WorldActor.tick 里调用：tick 循环只是众多入口之一
// （测试常直接 RunSystems），占格是**模拟的正确性**问题而不是编排细节，
// 所以它必须随系统流水线一起跑，不然会出现"跑测试时占格不生效"的假象。
type CreatureOccupancySystem struct {
	occ *creatureOccupancy
}

// NewCreatureOccupancySystem 建一个占格同步系统。
func NewCreatureOccupancySystem(occ *creatureOccupancy) *CreatureOccupancySystem {
	return &CreatureOccupancySystem{occ: occ}
}

// Update 实现 ECS 系统接口：每 tick 刷新动物占格。
func (s *CreatureOccupancySystem) Update(w *ecs.World, _ time.Duration) {
	if s == nil || s.occ == nil {
		return
	}
	s.occ.Sync(w)
}

// creatureOccupancy 维护"动物占了哪些格"的占位层（MapData.CreatureOccupied）。
//
// 需求：动物算占格——放置东西时不能放在动物已经占据的地方。
//
// 为什么要单独跟踪"每个实体占过哪些格"：动物每 tick 都在移动，跨格时必须
// 先把旧格清掉、再写新格。只写不清会让动物走过的地方永久变成"占用"，
// 地图很快整片不可放置。这里存 last 就是为了精确清除上一次的登记。
//
// 与静态占位（blockerIndex.tiles）分开，是因为两者的生命周期完全不同：
// 静态占位只在挂载/移除时变，动物占位每 tick 都可能变；共用一张表会互相干扰。
type creatureOccupancy struct {
	// last：实体 → 上一次登记的格。动物跨格时用它清旧格。
	last map[ecs.Entity][2]int
}

func newCreatureOccupancy() *creatureOccupancy {
	return &creatureOccupancy{last: make(map[ecs.Entity][2]int)}
}

// Sync 把所有带 Moveable 的实体（玩家/动物）的占格刷新到当前位置。
//
// 占的是"脚下那一格"（Position，整数锚点，不含 sub）；动物体型再大也按一格算，
// 与"一格只归一个占位物"的现有规则一致。
// 玩家也同步：玩家站在某格时同样不该被放置物压在身下。
func (o *creatureOccupancy) Sync(sim *ecs.World) {
	if o == nil {
		return
	}
	md, ok := ecs.TryResource[MapData](sim)
	if !ok || md == nil {
		return
	}
	alive := make(map[ecs.Entity]struct{}, len(o.last))
	ecs.Query2[components.Moveable, components.Position](sim, func(e ecs.Entity, _ *components.Moveable, p *components.Position) {
		alive[e] = struct{}{}
		cur := [2]int{p.X, p.Y}
		if prev, ok := o.last[e]; ok {
			if prev == cur {
				return // 没跨格，不用动占位层
			}
			md.SetCreatureOccupied(prev[0], prev[1], false)
		}
		md.SetCreatureOccupied(cur[0], cur[1], true)
		o.last[e] = cur
	})
	// 实体没了（死亡/移除/离线）：清掉它留下的占位，否则那一格永久不可放置。
	for e, prev := range o.last {
		if _, ok := alive[e]; ok {
			continue
		}
		md.SetCreatureOccupied(prev[0], prev[1], false)
		delete(o.last, e)
	}
}

// Reset 清空跟踪表与占位层（读档/换图后重建）。
func (o *creatureOccupancy) Reset(sim *ecs.World) {
	if o == nil {
		return
	}
	if md, ok := ecs.TryResource[MapData](sim); ok && md != nil {
		for e, prev := range o.last {
			md.SetCreatureOccupied(prev[0], prev[1], false)
			delete(o.last, e)
		}
	}
	clear(o.last)
}
