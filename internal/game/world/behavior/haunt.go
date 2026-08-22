package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

const HauntRange = 2

type hauntWalkability interface {
	Walkable(x, y int) bool
	MapSize() (width, height int)
}

// HauntBehavior 负责复活雕像的最终业务校验与原实体复活事务。
type HauntBehavior struct{}

func (HauntBehavior) CanDo(w *ecs.World, actor, target ecs.Entity) bool {
	if !w.IsAlive(actor) || !ecs.Has[components.Dead](w, actor) ||
		!ecs.Has[components.Position](w, actor) || !w.IsAlive(target) ||
		!ecs.Has[components.Position](w, target) || !ecs.Has[components.Hauntable](w, target) {
		return false
	}
	hauntable := ecs.Get[components.Hauntable](w, target)
	if hauntable.RemainingUses <= 0 {
		return false
	}
	width, height := hauntFootprint(w, target)
	actorPos := ecs.Get[components.Position](w, actor)
	targetPos := ecs.Get[components.Position](w, target)
	return actorPos.ManhattanToFootprint(*targetPos, width, height) <= HauntRange
}

func (b HauntBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	if !b.CanDo(w, actor, target) {
		return false
	}
	targetPos := *ecs.Get[components.Position](w, target)
	width, height := hauntFootprint(w, target)
	revivePos, ok := nearestRevivePosition(w, target, targetPos, width, height)
	if !ok || !ecs.Has[components.Health](w, actor) || !ecs.Has[components.Hunger](w, actor) {
		return false
	}

	health := ecs.Get[components.Health](w, actor)
	if health.Max <= 0 {
		health.Max = 1
	}
	health.Cur = health.Max
	ecs.MarkDirty[components.Health](w, actor)

	hunger := ecs.Get[components.Hunger](w, actor)
	hunger.Level = 50
	ecs.MarkDirty[components.Hunger](w, actor)

	if ecs.Has[components.Effects](w, actor) {
		effects := ecs.Get[components.Effects](w, actor)
		effects.Active = map[components.EffectOrder]components.EffectState{}
		ecs.MarkDirty[components.Effects](w, actor)
	}
	if ecs.Has[components.Moveable](w, actor) {
		moveable := ecs.Get[components.Moveable](w, actor)
		moveable.DirX, moveable.DirY = 0, 0
		moveable.SubX, moveable.SubY = 0, 0
		moveable.Path = nil
		ecs.MarkDirty[components.Moveable](w, actor)
	}
	position := ecs.Get[components.Position](w, actor)
	*position = revivePos
	ecs.MarkDirty[components.Position](w, actor)
	ecs.Remove[components.Dead](w, actor)

	hauntable := ecs.Get[components.Hauntable](w, target)
	hauntable.RemainingUses--
	if hauntable.RemainingUses > 0 {
		ecs.MarkDirty[components.Hauntable](w, target)
	} else {
		w.DestroyEntity(target)
	}
	return true
}

func hauntFootprint(w *ecs.World, target ecs.Entity) (int, int) {
	if ecs.Has[components.Block](w, target) {
		block := ecs.Get[components.Block](w, target)
		width, height := block.Width, block.Height
		if width <= 0 {
			width = 1
		}
		if height <= 0 {
			height = 1
		}
		return width, height
	}
	return 1, 1
}

func nearestRevivePosition(
	w *ecs.World,
	target ecs.Entity,
	anchor components.Position,
	width, height int,
) (components.Position, bool) {
	mapData, hasMap := ecs.TryResourceOf[hauntWalkability](w)
	maxRadius := 64
	if hasMap {
		mapWidth, mapHeight := mapData.MapSize()
		maxRadius = mapWidth + mapHeight
	}
	for radius := 1; radius <= maxRadius; radius++ {
		for y := anchor.Y - radius; y <= anchor.Y+height-1+radius; y++ {
			for x := anchor.X - radius; x <= anchor.X+width-1+radius; x++ {
				candidate := components.Position{X: x, Y: y}
				if candidate.ManhattanToFootprint(anchor, width, height) != radius {
					continue
				}
				if hasMap {
					if !mapData.Walkable(x, y) {
						continue
					}
				} else if blockedByEntity(w, target, x, y) {
					continue
				}
				return candidate, true
			}
		}
	}
	return components.Position{}, false
}

func blockedByEntity(w *ecs.World, ignored ecs.Entity, x, y int) bool {
	blocked := false
	ecs.Query2[components.Block, components.Position](w, func(e ecs.Entity, block *components.Block, pos *components.Position) {
		if blocked || e == ignored {
			return
		}
		width, height := block.Width, block.Height
		if width <= 0 {
			width = 1
		}
		if height <= 0 {
			height = 1
		}
		blocked = x >= pos.X && x < pos.X+width && y >= pos.Y && y < pos.Y+height
	})
	return blocked
}
