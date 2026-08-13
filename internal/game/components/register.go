package components

import "starve/internal/ecs"

// RegisterCodecs 为玩法组件注册名称 + codec（快照/存档用）。
// 必须在组件第一次被 Add/Query 之前调用（WorldActor 构造时执行）。
// 组件多了之后：每个组件文件自带 codec（见各 *_codec.go 内定义），
// 只需在这里追加一行注册。
func RegisterCodecs(w *ecs.World, debugAOI bool) {
	ecs.RegisterComponent(w, "Position", positionCodec{})
	ecs.RegisterComponent(w, "Moveable", moveableCodec{})
	ecs.RegisterComponent(w, "Health", healthCodec{})
	ecs.RegisterComponent(w, "Hunger", hungerCodec{})
	ecs.RegisterComponent(w, "Growable", growableCodec{})
	ecs.RegisterComponent(w, "Dead", deadCodec{})
	ecs.RegisterComponent(w, "Player", playerCodec{})
	ecs.RegisterComponent(w, "Offline", offlineCodec{})
	ecs.RegisterComponent(w, "Workable", workableCodec{})
	ecs.RegisterComponent(w, "Equipped", equippedCodec{})
	ecs.RegisterComponent(w, "Respawn", respawnCodec{})
	ecs.RegisterComponent(w, "Workstation", workstationCodec{})
	ecs.RegisterComponent(w, "Crafting", craftingCodec{})
	ecs.RegisterComponent(w, "Inventory", inventoryCodec{})
	ecs.RegisterComponent(w, "Loot", lootCodec{})
	ecs.RegisterComponent(w, "Effects", effectsCodec{})
	ecs.RegisterComponent(w, "EffectEmitter", effectEmitterCodec{})
	ecs.RegisterComponent(w, "Fan", fanCodec{})
	ecs.RegisterComponent(w, "HeatSource", heatSourceCodec{})
	ecs.RegisterComponent(w, "Creature", creatureCodec{})
	ecs.RegisterComponent(w, "AI", aiCodec{})
	ecs.RegisterComponent(w, "Weapon", weaponCodec{})
	ecs.RegisterComponent(w, "AOI", aoiCodec{includeVisible: debugAOI}) // debug 才编码 Visible
}
