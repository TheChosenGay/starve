package main

import (
	"math"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/internal/game/worldmap"
)

// predictor 是 TUI 的**本地移动预测**：按按键立即移动，不等服务端快照往返。
//
// 它与 Godot 客户端的 OwnMovementSim 是同一个定位，但实现方式不同——
// 这里**直接调用服务端的 MoveSolver**（见 internal/game/systems/move_solver.go），
// 而不是把公式再抄一遍。好处是预测与服务端在定义上就不会分叉：
// 同一份三阶段（desired → 静态滑掠 → ORCA 避让）、同一套参数。
//
// 世界上下文（静态障碍 + 其他动态体 + 地形）来自每帧从快照重建的 WorldSnapshot。
// 预测只维护"自己"的位置；服务端快照到达时做一致性校正（误差可见于 HUD）。
type predictor struct {
	snap   *systems.WorldSnapshot
	solver *systems.MoveSolver

	// selfID 是本地预测的实体（自己）。
	selfID ecs.Entity
	// pos 是预测出的连续位置（格，浮点）；sub 用其小数部分。
	posX, posY float64
	// vel 是上一次预测出的实际速度（ORCA 的"当前速度"输入）。
	velX, velY float64
	// intent 是当前按键意图方向。
	dirX, dirY int
	// active 表示本轮预测是否有效（世界就绪 + 自己是动态体）。
	active bool
	// svel 缓存自己的有效速度（服务端下发的 effective_speed）。
	svel float64

	// lastErr 是最近一次与服务端的偏差（格），供 HUD 显示。
	lastErr float64
	// corrections/snaps 是校正计数（软校正 / 硬贴合）。
	corrections, snaps int
}

// newPredictor 建一个本地预测器。
func newPredictor() *predictor {
	return &predictor{
		snap: systems.NewWorldSnapshot(),
		solver: systems.NewMoveSolver(systems.NewORCASolver(systems.ORCAOptions{
			TimeHorizon:  2.0,
			CollabCoeff:  0.5,
			SafetyMargin: 0.02,
		}), 20),
	}
}

// Sync 用最新快照重建世界上下文（静态障碍 + 动态体 + 地形）。
// 由主循环在每次 applySnapshot/applyDelta 之后调用。
func (p *predictor) Sync(w *world) {
	p.snap.RemoveAllDynamic()
	for id, e := range w.entities {
		if e.pos == nil {
			continue
		}
		col, ok := collideOf(e)
		if !ok {
			continue
		}
		pos := components.Position{X: int(e.pos.X), Y: int(e.pos.Y)}
		if isDynamicEntity(e) {
			mv := components.Moveable{Speed: 10}
			if e.moveable != nil {
				mv = components.Moveable{
					Speed:          e.moveable.Speed,
					EffectiveSpeed: e.moveable.EffectiveSpeed,
					DirX:           int(e.moveable.DirX),
					DirY:           int(e.moveable.DirY),
					SubX:           float64(e.moveable.SubX),
					SubY:           float64(e.moveable.SubY),
					VelX:           float64(e.moveable.VelX),
					VelY:           float64(e.moveable.VelY),
				}
			}
			p.snap.AddDynamic(ecs.Entity(id), col, pos, mv)
			continue
		}
		p.snap.AddStatic(ecs.Entity(id), col, pos)
	}
	// 地形：可走性与服务端一致（只有水不可走）。
	p.snap.SetMap(tuiMapData(w))

	// 自己的预测状态：首次进入或偏差过大时贴合服务端。
	if own := w.entities[w.own]; own != nil && own.pos != nil {
		p.selfID = ecs.Entity(w.own)
		if !p.active {
			p.posX, p.posY = own.renderX(), own.renderY()
			p.velX, p.velY = 0, 0
			p.active = true
		}
	}
}

// SetIntent 设置按键方向（0,0 = 停）。
func (p *predictor) SetIntent(dx, dy int) {
	p.dirX, p.dirY = dx, dy
	if dx == 0 && dy == 0 {
		p.velX, p.velY = 0, 0
	}
}

// Tick 推进一帧预测（dt 为秒）。
func (p *predictor) Tick(dt float64) {
	if !p.active || dt <= 0 {
		return
	}
	if p.dirX == 0 && p.dirY == 0 {
		return
	}
	// 阶段①的期望位移由服务端的同一个函数算（含坡度因子、对角归一化）。
	dir := components.MoveDir{DX: p.dirX, DY: p.dirY}
	speed := p.speed()
	dx, dy := systems.DesiredDisplacement(p.snap.World, dir, speed, dt, p.posX, p.posY)

	// 构造组件视图喂给服务端求解器。
	pos := components.Position{X: int(p.posX), Y: int(p.posY)}
	mv := components.Moveable{
		Speed: speed, SubX: p.posX - float64(int(p.posX)), SubY: p.posY - float64(int(p.posY)),
		VelX: p.velX, VelY: p.velY,
	}
	col := p.ownCollide()
	// 开轮：告诉求解器"新的一帧开始了"，邻居表才会按降频策略刷新。
	// 不调的话求解器会退化成"每帧重查"（安全但慢），拿不到降频的收益。
	p.solver.RefreshNeighborCache()
	res := p.solver.Solve(p.snap.World, p.selfID, &pos, &mv, &col, systems.MoveInput{
		DesiredX: dx, DesiredY: dy, DirX: p.dirX, DirY: p.dirY, Speed: speed, DT: dt,
	})
	p.posX += res.FinalX
	p.posY += res.FinalY
	p.velX, p.velY = res.VelX, res.VelY
	p.svel = speed

	// 把预测位置写回快照（这样下一帧的 ORCA 邻居信息包含"我在哪"）。
	p.snap.UpdateDynamic(p.selfID,
		components.Position{X: int(p.posX), Y: int(p.posY)},
		components.Moveable{Speed: speed, SubX: p.posX - float64(int(p.posX)),
			SubY: p.posY - float64(int(p.posY)), VelX: p.velX, VelY: p.velY})
}

// Position 返回预测位置（格）。
func (p *predictor) Position() (float64, float64) { return p.posX, p.posY }

// Reconcile 用服务端权威位置校正预测：小误差忽略，大误差直接贴合。
//
// 与服务端 WorldSnapshot 的等价性由 systems 包的测试保证；这里只处理"网络迟到"
// 带来的时差——所以阈值按"一帧位移量级"给，而不是追求零误差。
func (p *predictor) Reconcile(serverX, serverY float64, stopped bool) {
	if !p.active {
		p.posX, p.posY = serverX, serverY
		p.active = true
		return
	}
	ex, ey := serverX-p.posX, serverY-p.posY
	err := ex*ex + ey*ey
	p.lastErr = math.Sqrt(err)
	// 硬贴合：瞬移/传送/被推挤后的重同步
	if p.lastErr > 2.0 {
		p.posX, p.posY = serverX, serverY
		p.snaps++
		return
	}
	// 软校正：服务端已停而我还在预测（或反之），按比例合拢
	if stopped && p.lastErr > 0.05 {
		p.posX += ex * 0.5
		p.posY += ey * 0.5
		p.corrections++
	}
}

// speed 自己的有效速度（缺省 10 格/秒，与服务端一致）。
func (p *predictor) speed() float64 {
	if p.svel > 0 {
		return p.svel
	}
	return 10
}

// ownCollide 自己的碰撞体（快照里读不到时用服务端缺省值）。
func (p *predictor) ownCollide() components.Collide {
	return components.Collide{
		Shape:      components.CollideShapeCapsule,
		Radius:     systems.BodyRadius,
		BodyHeight: systems.BodyHeight,
		FaceX:      p.dirX, FaceZ: p.dirY,
	}
}

// tuiMapData 把 TUI 的地形镜像成服务端世界的 MapData，
// 让 MoveSolver 的格子层（水/悬崖判定）与坡度因子用上真实地形。
// 地形在客户端是静态的，所以每次重建一个小对象即可（不缓存指针，避免失效）。
func tuiMapData(w *world) *worldmap.MapData {
	if w == nil || len(w.cornerTypes) == 0 {
		return nil
	}
	return &worldmap.MapData{
		Width:         w.width,
		Height:        w.height,
		CornerTypes:   w.cornerTypes,
		CornerHeights: w.cornerHeights,
	}
}

// isDynamicEntity 判断实体是不是动态体（玩家/生物）。
func isDynamicEntity(e *entity) bool {
	return e.player != nil || e.creature != nil
}

// collideOf 从 TUI 实体取出碰撞体（新协议里是独立的 Collide 组件）。
func collideOf(e *entity) (components.Collide, bool) {
	if e.collide == nil {
		return components.Collide{}, false
	}
	shape := components.CollideShapeNone
	switch e.collide.Shape {
	case 1:
		shape = components.CollideShapeCircle
	case 2:
		shape = components.CollideShapeBox
	case 3:
		shape = components.CollideShapeCapsule
	}
	return components.Collide{
		Shape:      shape,
		Radius:     e.collide.Radius,
		Width:      int(e.collide.Width),
		Height:     int(e.collide.Height),
		HalfLength: e.collide.HalfLength,
		BodyHeight: e.collide.BodyHeight,
		FaceX:      int(e.collide.FaceX),
		FaceZ:      int(e.collide.FaceZ),
	}, true
}
