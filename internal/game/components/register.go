package components

import (
	"starve/internal/ecs"
	"starve/internal/game/components/interactive"
)

// RegisterCodecs 为玩法组件注册名称 + codec（快照/存档用）。
// 必须在组件第一次被 Add/Query 之前调用（WorldActor 构造时执行）。
// 每个组件的注册调用放在各自的文件里（RegisterX），这里只做统一入口。
func RegisterCodecs(w *ecs.World, debugAOI bool) {
	RegisterPosition(w)
	RegisterMoveable(w)
	RegisterHealth(w)
	RegisterHunger(w)
	RegisterGrowable(w)
	RegisterDead(w)
	RegisterPlayer(w)
	RegisterOffline(w)
	RegisterWorkable(w)
	RegisterEquipped(w)
	RegisterRespawn(w)
	RegisterWorkstation(w)
	RegisterCrafting(w)
	RegisterInventory(w)
	RegisterLoot(w)
	RegisterEffects(w)
	RegisterEffectEmitter(w)
	RegisterFan(w)
	RegisterHeatSource(w)
	RegisterCreature(w)
	RegisterAI(w)
	RegisterWeapon(w)
	interactive.RegisterEquip(w)
	interactive.RegisterChopper(w)
	interactive.RegisterMiner(w)
	interactive.RegisterPicker(w)
	interactive.RegisterChoppable(w)
	interactive.RegisterMinable(w)
	interactive.RegisterPickable(w)
	interactive.RegisterAttacker(w)
	RegisterRespawnable(w)
	RegisterDefense(w)
	RegisterAttackable(w)
	RegisterAOI(w, debugAOI)
	RegisterBuilding(w)
	RegisterBlock(w)
}
