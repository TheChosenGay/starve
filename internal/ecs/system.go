package ecs

import (
	"fmt"
	"sort"
	"time"
)

// System 是批量逻辑的单元：对某几类组件做查询更新。
// 约束（设计文档 §4.4）：纯函数——不调 actor API、不发网络消息、
// 不用全局 rand / time.Now()，随机数走 Resource。
type System interface {
	Update(w *World, dt time.Duration)
}

type systemEntry struct {
	order int
	sys   System
}

// AddSystem 按固定顺序注册系统；order 相同直接报错（防止依赖顺序被静默打乱）。
//
// order 约定（系统多的时候怎么填）：按"阶段"划分，阶段之间留大间隔，
// 阶段内用 10 的倍数留插入位，例如：
//
//	10: 输入/命令应用   100: 模拟（移动/饥饿/生长）   1000: 结算/输出
//
// 具体阶段常量由游戏层（internal/game）统一定义，不要在玩法代码里散落
// 魔法数字。若未来系统数量大增，可升级为"依赖式排序"（AddSystemAfter，
// 参照 Bevy/Flecs 的 before/after），顶层调度负责拓扑排序。
func (w *World) AddSystem(order int, s System) {
	if s == nil {
		panic("ecs: AddSystem: nil system")
	}
	for _, e := range w.systems {
		if e.order == order {
			panic(fmt.Sprintf("ecs: AddSystem: duplicate order %d", order))
		}
	}
	w.systems = append(w.systems, systemEntry{order: order, sys: s})
	sort.SliceStable(w.systems, func(i, j int) bool {
		return w.systems[i].order < w.systems[j].order
	})
}

// RunSystems 按注册顺序执行所有系统。
// 固定 dt 由调用方注入（M3 起为 10Hz → 100ms），ECS 不读 wall clock。
func (w *World) RunSystems(dt time.Duration) {
	for _, e := range w.systems {
		e.sys.Update(w, dt)
	}
}

// SystemCount 返回已注册系统数。
func (w *World) SystemCount() int { return len(w.systems) }
