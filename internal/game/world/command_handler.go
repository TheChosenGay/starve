package world

import (
	"encoding/json"
	"log/slog"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/world/behavior"
	"starve/internal/game/worldmap"
)

// CommandHandler 处理玩家命令（命令是应用逻辑，世界 actor 负责世界本身）。
// 每个命令：所有权校验 → 目标/距离校验 → 语义执行 → 变更进 dirty（快照/存档）。
type CommandHandler struct {
	a *WorldActor
}

// Handle 按命令类型分发。
func (h *CommandHandler) Handle(c Command) {
	// 幽灵守卫：死亡玩家（灵魂）只能移动观察，不能与世界交互（采集/攻击/制作等一律忽略）。
	if c.Kind != CommandMove {
		if e, ok := h.a.findPlayer(c.UID); ok && ecs.Has[components.Dead](h.a.sim, e) {
			return
		}
	}
	switch c.Kind {
	case CommandMove:
		h.move(c)
	case CommandAttack:
		h.attack(c)
	case CommandGather:
		h.gather(c)
	case CommandPickup:
		h.pickup(c)
	case CommandUse:
		h.use(c)
	case CommandEquip:
		h.equip(c)
	case CommandChop:
		h.chop(c)
	case CommandMine:
		h.mine(c)
	case CommandAutomate:
		h.automate(c)
	case CommandDrop:
		h.drop(c)
	case CommandCancelCraft:
		h.cancelCraft(c)
	case CommandSplit:
		h.split(c)
	case CommandPlace:
		h.place(c)
	case CommandDemolish:
		h.demolish(c)
	}
}

func (h *CommandHandler) move(c Command) {
	m, ok := c.Data.(MoveData)
	if !ok {
		return
	}
	if h.a.players[m.Entity] != c.UID {
		return // 只能移动自己的实体
	}
	if !ecs.Has[components.Position](h.a.sim, m.Entity) {
		return
	}
	// tick 制移动：命令是方向步进，进 Moveable.Queue 缓存（顺序应用），
	// MoveSystem 每 tick 按步进间隔消费。兼容旧实体/旧档：没有 Moveable 自动补。
	mv := ecs.Ensure[components.Moveable](h.a.sim, m.Entity)
	if mv.Interval <= 0 {
		mv.Interval = h.a.cfg.MoveInterval
		if mv.Interval <= 0 {
			mv.Interval = 2
		}
	}
	dx, dy := clampDir(m.DX), clampDir(m.DY)
	if dx == 0 && dy == 0 {
		// 停止：清空待执行队列（客户端松键立即停）
		mv.Queue = nil
		mv.Elapsed = 0
	} else {
		if len(mv.Queue) >= maxMoveQueue {
			return // 队列满：丢弃新指令（防客户端无节流积压）
		}
		started := len(mv.Queue) == 0
		mv.Queue = append(mv.Queue, components.MoveDir{DX: dx, DY: dy})
		// 从停止到开始移动：走动打断制作（客户端主动取消的一种）
		if started {
			if it, ok := h.checkInterrupt(m.Entity); ok {
				h.onInterrupt(m.Entity, it)
			}
		}
	}
	ecs.MarkDirty[components.Moveable](h.a.sim, m.Entity)
}

// maxMoveQueue 移动命令队列上限（客户端长按节流发送，服务器按速度消费）。
const maxMoveQueue = 32

// clampDir 把方向值约束到 -1/0/1（MoveData.DX/DY 现在是方向意图，不是位移）。
func clampDir(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// absInt 绝对值（曼哈顿距离计算用）。
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (h *CommandHandler) attack(c Command) {
	at, ok := c.Data.(AttackData)
	if !ok {
		return
	}
	if h.a.players[at.Attacker] != c.UID {
		slog.Debug("attack rejected: not owner", "uid", c.UID, "attacker", at.Attacker)
		return // 只能控制自己的实体
	}
	if !interactive.Do(h.a.sim, at.Attacker, at.Target, interactive.IntentAttack) {
		return // 前置条件不满足（无攻击能力/目标不可攻击/距离不够）
	}
}

// checkInterrupt 检查实体是否有可打断组件（实现 Interruptable 的已登记类型）：
// 有则移除并返回其实例（组件自己负责恢复）。
func (h *CommandHandler) checkInterrupt(e ecs.Entity) (components.Interruptable, bool) {
	for _, probe := range components.Interruptibles() {
		if it, ok := probe(h.a.sim, e); ok {
			return it, true
		}
	}
	return nil, false
}

// onInterrupt 打断后的统一入口：组件自己恢复状态 + 发射通知（Resume 内部处理）。
func (h *CommandHandler) onInterrupt(e ecs.Entity, it components.Interruptable) {
	it.Resume(h.a.sim, e)
}

// cancelCraft 主动取消制作命令（客户端发起）。
func (h *CommandHandler) cancelCraft(c Command) {
	d, ok := c.Data.(CancelCraftData)
	if !ok {
		return
	}
	if h.a.players[d.Player] != c.UID {
		return // 只能取消自己的制作
	}
	if it, ok := h.checkInterrupt(d.Player); ok {
		h.onInterrupt(d.Player, it)
	}
}

func (h *CommandHandler) gather(c Command) {
	g, ok := c.Data.(GatherData)
	if !ok {
		return
	}
	h.work(c.UID, g.Player, g.Target, components.WorkPick)
}

func (h *CommandHandler) chop(c Command) {
	cd, ok := c.Data.(ChopData)
	if !ok {
		return
	}
	h.work(c.UID, cd.Player, cd.Target, components.WorkChop)
}

func (h *CommandHandler) mine(c Command) {
	md, ok := c.Data.(MineData)
	if !ok {
		return
	}
	h.work(c.UID, md.Player, md.Target, components.WorkMine)
}

// defaultAutomateRadius 玩家无 AOI 组件时的自动行为搜索半径（AOI 存在时用 AOI.Radius）。
const defaultAutomateRadius = 8

// automate 空格自动行为：在玩家 AOI 范围内按距离找最近的可执行行为并执行一次；
// 若交互距离内没有可操作对象，则找 AOI 内最近的匹配目标，把寻路结果压进移动队列走过去
// （复用生物追击的 worldmap.FindPath，走完即停；不自动执行，需再按一次或等寻路二期）。
// 目标选取（er→able 匹配 + 距离）在 behavior 包；执行与副作用复用既有 work/attack 路径。
func (h *CommandHandler) automate(c Command) {
	d, ok := c.Data.(AutomateData)
	if !ok {
		return
	}
	a := h.a
	if a.players[d.Player] != c.UID {
		return // 只能控制自己的实体
	}
	intent, target, ok := behavior.FindBest(a.sim, d.Player, h.automateRadius(d.Player))
	if ok {
		h.executeIntent(c.UID, d.Player, target, intent)
		return
	}
	// 兜底：AOI 内有匹配目标但超出交互距离 → 走过去（寻路入队，走完即停）
	if _, target, ok := behavior.FindWalkTarget(a.sim, d.Player, h.automateRadius(d.Player)); ok {
		if h.walkTo(d.Player, target) {
			if it, ok := h.checkInterrupt(d.Player); ok { // 开始走动打断制作
				h.onInterrupt(d.Player, it)
			}
		}
	}
}

// walkTo 朝目标走过去：把 worldmap.FindPath（A*，与生物追击同一套）的结果压进 Moveable.Queue。
// 目标自身占格不可走（树/岩带 Block）时，改寻路到最近的相邻可走格；
// 队列非空（已在移动）不重复压路。返回是否真的开始走。
func (h *CommandHandler) walkTo(player, target ecs.Entity) bool {
	a := h.a
	if !ecs.Has[components.Position](a.sim, player) || !ecs.Has[components.Position](a.sim, target) {
		return false
	}
	md, ok := ecs.TryResource[MapData](a.sim)
	if !ok {
		return false // 无地图不可寻路（正式环境必有地图，防御性兜底）
	}
	mv := ecs.Ensure[components.Moveable](a.sim, player)
	if len(mv.Queue) > 0 {
		return true // 已在移动（含寻路中）：不重复压路
	}
	pp := ecs.Get[components.Position](a.sim, player)
	tp := ecs.Get[components.Position](a.sim, target)
	gx, gy := tp.X, tp.Y
	if !md.Walkable(gx, gy) {
		if !h.nearestWalkableGoal(md, tp.X, tp.Y, &gx, &gy) {
			return false // 目标被完全围死：不可达
		}
	}
	steps := worldmap.FindPath(md, pp.X, pp.Y, gx, gy)
	if len(steps) == 0 {
		return false // 同格或不可达
	}
	if len(steps) > maxMoveQueue {
		steps = steps[:maxMoveQueue]
	}
	mv.Queue = append(mv.Queue, steps...)
	ecs.MarkDirty[components.Moveable](a.sim, player)
	return true
}

// nearestWalkableGoal 目标格不可走时，找离目标最近的可走格（半径 2 内，确定性顺序）。
func (h *CommandHandler) nearestWalkableGoal(md *MapData, tx, ty int, gx, gy *int) bool {
	best := -1
	for r := 1; r <= 2; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				x, y := tx+dx, ty+dy
				if !md.Walkable(x, y) {
					continue
				}
				d := absInt(tx-x) + absInt(ty-y)
				if best < 0 || d < best {
					best, *gx, *gy = d, x, y
				}
			}
		}
	}
	return best >= 0
}

// executeIntent 对选定目标执行一次行为：工作类走 work（PICK 入包/工具损坏卸下），攻击直接 Do。
// 执行前清掉剩余移动队列：行动开始即停下（按住空格持续评估时，不会执行完还往目标方向继续走）。
func (h *CommandHandler) executeIntent(uid string, player, target ecs.Entity, intent interactive.Intent) {
	if ecs.Has[components.Moveable](h.a.sim, player) {
		mv := ecs.Get[components.Moveable](h.a.sim, player)
		mv.Queue = nil
		ecs.MarkDirty[components.Moveable](h.a.sim, player)
	}
	switch intent {
	case interactive.IntentChop, interactive.IntentMine, interactive.IntentPick:
		h.work(uid, player, target, intent) // 复用：PICK 入包 / 工具损坏卸下
	case interactive.IntentAttack:
		interactive.Do(h.a.sim, player, target, interactive.IntentAttack)
	case interactive.IntentPickup:
		if interactive.Do(h.a.sim, player, target, interactive.IntentPickup) {
			h.applyPickup(player, target)
		}
	}
}

// automateRadius 自动行为的搜索半径 = AOI 感知半径；无 AOI（旧档实体）用默认值。
func (h *CommandHandler) automateRadius(player ecs.Entity) int {
	if ecs.Has[components.AOI](h.a.sim, player) {
		if r := ecs.Get[components.AOI](h.a.sim, player).Radius; r > 0 {
			return r
		}
	}
	return defaultAutomateRadius
}

// drop 丢弃背包物品：移除 count 个，在玩家位置生成可拾取的 Loot 实体。
func (h *CommandHandler) drop(c Command) {
	d, ok := c.Data.(DropData)
	if !ok || d.Count <= 0 {
		return
	}
	a := h.a
	if a.players[d.Player] != c.UID {
		return // 只能操作自己的背包
	}
	inv := h.ensureInventory(d.Player)
	if !inv.Take(d.Kind, d.Count) {
		return // 数量不足
	}
	ecs.MarkDirty[components.Inventory](a.sim, d.Player)

	pos := ecs.Get[components.Position](a.sim, d.Player)
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, *pos)
	ecs.Add(a.sim, e, components.Lootable{Items: []components.ItemStack{{Kind: d.Kind, Count: d.Count}}})
}

// split 拆分：从源槽取 count 个放入第一个空槽（堆叠上限/耐久随物品）。
func (h *CommandHandler) split(c Command) {
	d, ok := c.Data.(SplitData)
	if !ok || d.Count <= 0 {
		return
	}
	a := h.a
	if a.players[d.Player] != c.UID {
		return // 只能操作自己的背包
	}
	inv := h.ensureInventory(d.Player)
	from := inv.Slot(d.FromSlot)
	if from.Kind == 0 || from.Count < d.Count {
		return // 源槽无效或数量不足
	}
	to := inv.FirstEmptySlot()
	if to < 0 || to == d.FromSlot {
		return // 没有空槽
	}
	from.Count -= d.Count
	if from.Count <= 0 {
		inv.SetSlot(d.FromSlot, components.ItemStack{})
	} else {
		inv.SetSlot(d.FromSlot, from)
	}
	inv.SetSlot(to, components.ItemStack{Kind: from.Kind, Count: d.Count, MaxStack: from.MaxStack, Durability: from.Durability})
	ecs.MarkDirty[components.Inventory](a.sim, d.Player)
}

// build 建造请求（request/response）：校验执行者 → 按模板尺寸创建未放置建筑实体。
// 确定性：同步记日志（JournalBuild），重放走同一路径（kind 幂等，实体 id 由重放分配）。
func (h *CommandHandler) build(uid string, kind components.BuildingKind) BuildResult {
	a := h.a
	player, ok := a.findPlayer(uid)
	if !ok {
		return BuildResult{Message: "player not found"}
	}
	if ecs.Has[components.Dead](a.sim, player) {
		return BuildResult{Message: "player dead"}
	}
	w, hh := 1, 1
	if tpl, ok := a.config.Buildings[kind]; ok {
		w, hh = tpl.Width, tpl.Height
	}
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, components.Building{Kind: kind, Width: w, Height: hh})
	if raw, err := json.Marshal(int32(kind)); err == nil {
		a.recordJournal(JournalBuild, uid, 0, raw)
	}
	return BuildResult{Entity: e, Started: true}
}

// place 放置：把已创建（未放置）的建筑放到坐标。
func (h *CommandHandler) place(c Command) {
	d, ok := c.Data.(PlaceData)
	if !ok {
		return
	}
	if h.a.players[d.Actor] != c.UID {
		return
	}
	PlaceBuilding(h.a.sim, d.Entity, d.X, d.Y)
}

// demolish 拆除：目标必须是附近（范围 2）的建筑。
func (h *CommandHandler) demolish(c Command) {
	d, ok := c.Data.(DemolishData)
	if !ok {
		return
	}
	if h.a.players[d.Actor] != c.UID {
		return
	}
	if !ecs.Has[components.Building](h.a.sim, d.Target) {
		return
	}
	if !h.withinRange(d.Actor, d.Target, 2) {
		return
	}
	DemolishBuilding(h.a.sim, d.Target)
}

// craft 制作开始（request/response）：校验配方/工作站/材料/产物空间 → 扣材料 → 挂 Crafting。
// 确定性：同步记日志（JournalCraft），重放走同一路径。
func (h *CommandHandler) craft(uid, recipeID string) CraftResult {
	a := h.a
	player, ok := a.findPlayer(uid)
	if !ok {
		return CraftResult{Message: "player not found"}
	}
	if ecs.Has[components.Dead](a.sim, player) {
		return CraftResult{Message: "player dead"}
	}
	recipe, ok := a.recipes[recipeID]
	if !ok {
		return CraftResult{Message: "unknown recipe"}
	}
	if ecs.Has[components.Crafting](a.sim, player) {
		return CraftResult{Message: "already crafting"}
	}
	if recipe.Workstation != 0 && !h.nearWorkstation(player, recipe.Workstation) {
		return CraftResult{Message: "need workstation nearby"}
	}
	inv := h.ensureInventory(player)
	for _, ing := range recipe.Ingredients {
		if inv.CountOf(ing.Kind) < ing.Count {
			return CraftResult{Message: "insufficient materials"}
		}
	}
	if inv.SpaceFor(recipe.Output.Kind, a.template(recipe.Output.Kind).StackSize) < recipe.Output.Count {
		return CraftResult{Message: "output stack full"}
	}
	for _, ing := range recipe.Ingredients {
		inv.Take(ing.Kind, ing.Count)
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
	ingredients := make([]components.ItemStack, 0, len(recipe.Ingredients))
	for _, ing := range recipe.Ingredients {
		ingredients = append(ingredients, components.ItemStack{Kind: ing.Kind, Count: ing.Count, MaxStack: a.template(ing.Kind).StackSize})
	}
	ecs.Add(a.sim, player, components.Crafting{RecipeID: recipe.ID, TicksLeft: recipe.Ticks, Ingredients: ingredients})
	if raw, err := json.Marshal(recipeID); err == nil {
		a.recordJournal(JournalCraft, uid, 0, raw)
	}
	return CraftResult{Started: true, Ticks: recipe.Ticks}
}

// nearWorkstation 判断玩家附近（范围 3）是否有指定类型的工作站。
func (h *CommandHandler) nearWorkstation(player ecs.Entity, typ components.WorkstationType) bool {
	a := h.a
	if !ecs.Has[components.Position](a.sim, player) {
		return false
	}
	pp := ecs.Get[components.Position](a.sim, player)
	found := false
	ecs.Query2[components.Workstation, components.Position](a.sim, func(e ecs.Entity, w *components.Workstation, p *components.Position) {
		if !found && w.Type == typ && pp.WithinRange(*p, 3) {
			found = true
		}
	})
	return found
}

// work 执行一次工作（采集/砍伐/挖掘共用）：
//   - 匹配与执行委托给 interactive.Do（意图 → 注册的主动↔受激能力对）；
//   - 砍/挖需要工具（装备实体携带能力并复制到玩家），采集裸手可做；
//   - PICK 产物直接进背包，采空原地保留；CHOP/MINE 归零 → Dead → processDrops 掉落。
func (h *CommandHandler) work(uid string, player, target ecs.Entity, want components.WorkAction) {
	a := h.a
	if a.players[player] != uid {
		return // 只能控制自己的实体
	}
	if !a.sim.IsAlive(target) {
		return // 目标不可作用
	}
	if !interactive.Do(a.sim, player, target, want) {
		return // 前置条件不满足（能力不匹配/距离不够/已耗尽）
	}
	if want == components.WorkPick {
		// 采集产物直接进背包
		p := ecs.Get[interactive.Pickable](a.sim, target)
		h.addItem(player, p.Kind, 1)
		ecs.MarkDirty[components.Inventory](a.sim, player)
	}
	if tool := handToolOf(a.sim, player); tool != 0 && brokenTool(a.sim, tool) {
		h.unequipTool(player) // 工具耐久耗尽：自动卸下（耐久 ≤0 不回收）
	}
}

// spawnToolEntity 按工具 kind 生成工具实体（携带 Chopper/Miner 能力 + 初始耐久）。
func (h *CommandHandler) spawnToolEntity(kind components.ItemKind) ecs.Entity {
	t := h.a.template(kind)
	if t.Tool == nil {
		return 0
	}
	e := h.a.sim.CreateEntity()
	ecs.Add(h.a.sim, e, interactive.Equipment{Kind: kind})
	switch t.Tool.Action {
	case components.WorkChop:
		ecs.Add(h.a.sim, e, interactive.Chopper{Efficiency: t.Tool.Efficiency, Range: 2, Durability: t.Tool.Durability})
	case components.WorkMine:
		ecs.Add(h.a.sim, e, interactive.Miner{Efficiency: t.Tool.Efficiency, Range: 2, Durability: t.Tool.Durability})
	default:
		h.a.sim.DestroyEntity(e)
		return 0
	}
	return e
}

// handTool 玩家手持槽位的工具实体（0 = 空手）。
func (h *CommandHandler) handTool(player ecs.Entity) ecs.Entity {
	if !ecs.Has[interactive.Equip](h.a.sim, player) {
		return 0
	}
	return ecs.Get[interactive.Equip](h.a.sim, player).Item(interactive.SlotHand)
}

// setHandTool 装备：槽位挂工具实体 + 把主动能力复制到玩家（覆盖语义；耐久留在工具实体上）。
func (h *CommandHandler) setHandTool(player, tool ecs.Entity) {
	eq := ecs.Ensure[interactive.Equip](h.a.sim, player)
	eq.Set(interactive.SlotHand, tool)
	ecs.MarkDirty[interactive.Equip](h.a.sim, player)
	if ecs.Has[interactive.Chopper](h.a.sim, tool) {
		c := ecs.Get[interactive.Chopper](h.a.sim, tool)
		v := interactive.Chopper{Efficiency: c.Efficiency, Range: c.Range, Durability: -1}
		if ecs.Has[interactive.Chopper](h.a.sim, player) {
			ecs.Set(h.a.sim, player, v)
		} else {
			ecs.Add(h.a.sim, player, v)
		}
	}
	if ecs.Has[interactive.Miner](h.a.sim, tool) {
		c := ecs.Get[interactive.Miner](h.a.sim, tool)
		v := interactive.Miner{Efficiency: c.Efficiency, Range: c.Range, Durability: -1}
		if ecs.Has[interactive.Miner](h.a.sim, player) {
			ecs.Set(h.a.sim, player, v)
		} else {
			ecs.Add(h.a.sim, player, v)
		}
	}
}

// clearHandCapability 卸下后清掉工具能力（恢复裸手 Picker）。
func (h *CommandHandler) clearHandCapability(player ecs.Entity) {
	ecs.Remove[interactive.Chopper](h.a.sim, player)
	ecs.Remove[interactive.Miner](h.a.sim, player)
}

// unequipSlot 卸下指定槽位：物品（按 kind + 耐久）放回背包 → 销毁实体 → 清槽位 → 重算防御。
// 工具耐久 ≤0 视为损坏不回收；护甲无耐久始终回收。
func (h *CommandHandler) unequipSlot(player ecs.Entity, slot interactive.Slot) {
	a := h.a
	if !ecs.Has[interactive.Equip](a.sim, player) {
		return
	}
	eq := ecs.Get[interactive.Equip](a.sim, player)
	item := eq.Item(slot)
	if item == 0 {
		return
	}
	kind, dur, hasDur := h.itemState(item)
	if kind != 0 && (!hasDur || dur > 0) {
		inv := h.ensureInventory(player)
		inv.Add(kind, 1, a.template(kind).StackSize, dur)
		ecs.MarkDirty[components.Inventory](a.sim, player)
	}
	if slot == interactive.SlotHand {
		h.clearHandCapability(player)
	}
	eq.Set(slot, 0)
	ecs.MarkDirty[interactive.Equip](a.sim, player)
	a.sim.DestroyEntity(item)
	h.refreshDefense(player)
}

// unequipTool 卸下手持工具（兼容旧入口）。
func (h *CommandHandler) unequipTool(player ecs.Entity) {
	h.unequipSlot(player, interactive.SlotHand)
}

// unequipAll 卸下全部槽位（equip kind=0 语义：徒手/卸甲）。
func (h *CommandHandler) unequipAll(player ecs.Entity) {
	for _, slot := range interactive.All() {
		h.unequipSlot(player, slot)
	}
}

// itemState 装备实体的（物品 kind, 当前耐久, 是否有耐久）。
// kind 来自 Equipment 标记；工具耐久在能力组件（Chopper/Miner），护甲无耐久。
func (h *CommandHandler) itemState(item ecs.Entity) (components.ItemKind, int, bool) {
	a := h.a
	if !ecs.Has[interactive.Equipment](a.sim, item) {
		return 0, 0, false // 旧档由 migrateEquipment 补挂，这里不做运行时反推
	}
	kind := ecs.Get[interactive.Equipment](a.sim, item).Kind
	if ecs.Has[interactive.Chopper](a.sim, item) {
		return kind, ecs.Get[interactive.Chopper](a.sim, item).Durability, true
	}
	if ecs.Has[interactive.Miner](a.sim, item) {
		return kind, ecs.Get[interactive.Miner](a.sim, item).Durability, true
	}
	return kind, 0, false // 护甲等无耐久装备
}

// equip 装备：kind 非 0 时按模板分派（工具 → 手持；护甲 → head/body 槽位）；kind=0 卸下全部。
// 工具砍/挖能力来自装备实体（Chopper/Miner）；护甲防御复制到玩家（Attackable 受击时读取）。
func (h *CommandHandler) equip(c Command) {
	e, ok := c.Data.(EquipData)
	if !ok {
		return
	}
	a := h.a
	if a.players[e.Player] != c.UID {
		return
	}
	if e.Kind == 0 {
		h.unequipAll(e.Player)
		return
	}
	t := a.template(e.Kind)
	if t.Armor != nil {
		h.equipArmor(e.Player, e.Kind, t)
		return
	}
	if t.Tool == nil || (t.Tool.Action != components.WorkChop && t.Tool.Action != components.WorkMine) {
		return // 只支持砍/挖工具
	}
	inv := h.ensureInventory(e.Player)
	if inv.CountOf(e.Kind) <= 0 {
		return // 背包里没有
	}
	h.unequipTool(e.Player) // 先卸下旧的
	inv.Take(e.Kind, 1)
	ecs.MarkDirty[components.Inventory](a.sim, e.Player)
	if tool := h.spawnToolEntity(e.Kind); tool != 0 {
		h.setHandTool(e.Player, tool)
	}
}

// equipArmor 装备护甲：目标槽位（模板 armor.slot）→ 卸下旧物 → 生成护甲实体（Defense）
// → 挂槽位 + 把防御复制到玩家（卸下时 refreshDefense 重算）。
func (h *CommandHandler) equipArmor(player ecs.Entity, kind components.ItemKind, t ItemTemplate) {
	a := h.a
	slot := armorSlot(t.Armor.Slot)
	if slot == 0 {
		return
	}
	inv := h.ensureInventory(player)
	if inv.CountOf(kind) <= 0 {
		return // 背包里没有
	}
	h.unequipSlot(player, slot) // 先卸下同槽旧物（放回背包）
	inv.Take(kind, 1)
	ecs.MarkDirty[components.Inventory](a.sim, player)

	item := a.sim.CreateEntity()
	ecs.Add(a.sim, item, interactive.Equipment{Kind: kind})
	ecs.Add(a.sim, item, components.Defense{Percent: t.Armor.Percent})
	eq := ecs.Ensure[interactive.Equip](a.sim, player)
	eq.Set(slot, item)
	ecs.MarkDirty[interactive.Equip](a.sim, player)
	h.refreshDefense(player)
}

// armorSlot 模板护甲槽位字符串 → 槽位（head/body；未知 = 0）。
func armorSlot(s string) interactive.Slot {
	switch s {
	case "head":
		return interactive.SlotHead
	case "body":
		return interactive.SlotBody
	}
	return 0
}

// refreshDefense 按头/身槽位护甲实体重算玩家防御百分比（叠加），写回或移除玩家 Defense。
func (h *CommandHandler) refreshDefense(player ecs.Entity) {
	a := h.a
	total := 0
	if ecs.Has[interactive.Equip](a.sim, player) {
		eq := ecs.Get[interactive.Equip](a.sim, player)
		for _, slot := range []interactive.Slot{interactive.SlotHead, interactive.SlotBody} {
			if item := eq.Item(slot); item != 0 && ecs.Has[components.Defense](a.sim, item) {
				total += ecs.Get[components.Defense](a.sim, item).Percent
			}
		}
	}
	if total > 0 {
		if ecs.Has[components.Defense](a.sim, player) {
			ecs.Set(a.sim, player, components.Defense{Percent: total})
		} else {
			ecs.Add(a.sim, player, components.Defense{Percent: total})
		}
	} else {
		ecs.Remove[components.Defense](a.sim, player)
	}
}

// pickup 拾取：Looter ↔ Lootable 匹配由 behavior 完成；成功后物品入包 + 销毁掉落物。
func (h *CommandHandler) pickup(c Command) {
	p, ok := c.Data.(PickupData)
	if !ok {
		return
	}
	a := h.a
	if a.players[p.Player] != c.UID {
		return
	}
	if !interactive.Do(a.sim, p.Player, p.Target, interactive.IntentPickup) {
		return
	}
	h.applyPickup(p.Player, p.Target)
}

// applyPickup 拾取副作用：物品入包（堆叠上限来自模板）+ 销毁掉落物。
// 与 Pick 的"产物入包"同一分工：行为只做匹配校验，模板/生命周期留在世界层。
func (h *CommandHandler) applyPickup(player, target ecs.Entity) {
	a := h.a
	loot := ecs.Get[components.Lootable](a.sim, target)
	inv := h.ensureInventory(player)
	for _, s := range loot.Items {
		inv.Add(s.Kind, s.Count, a.template(s.Kind).StackSize, 0)
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
	a.sim.DestroyEntity(target)
}

func (h *CommandHandler) use(c Command) {
	u, ok := c.Data.(UseData)
	if !ok {
		return
	}
	a := h.a
	if a.players[u.Player] != c.UID {
		return
	}
	t, ok := a.templates[u.Kind]
	if !ok || t.UseEffect == nil {
		return // 该物品不可使用
	}
	inv := h.ensureInventory(u.Player)
	if !inv.Take(u.Kind, 1) {
		return
	}
	ecs.MarkDirty[components.Inventory](a.sim, u.Player)

	if ef := t.UseEffect; ef.Hunger != 0 {
		h := ecs.Get[components.Hunger](a.sim, u.Player)
		h.Level += ef.Hunger
		if h.Level < 0 {
			h.Level = 0
		}
		if h.Level > 100 {
			h.Level = 100
		}
		ecs.MarkDirty[components.Hunger](a.sim, u.Player)
	}
	if ef := t.UseEffect; ef.Health != 0 {
		hp := ecs.Get[components.Health](a.sim, u.Player)
		hp.Cur += ef.Health
		if hp.Cur < 0 {
			hp.Cur = 0
		}
		if hp.Cur > hp.Max {
			hp.Cur = hp.Max
		}
		ecs.MarkDirty[components.Health](a.sim, u.Player)
	}
}

// addItem 按模板属性给玩家加物品（堆叠上限/工具耐久来自模板）。
func (h *CommandHandler) addItem(player ecs.Entity, kind components.ItemKind, count int) {
	a := h.a
	t := a.template(kind)
	durability := 0
	if t.Tool != nil {
		durability = t.Tool.Durability
	}
	inv := h.ensureInventory(player)
	inv.Add(kind, count, t.StackSize, durability)
}

// ensureInventory 返回玩家背包并补齐到配置容量（新背包/旧档）。
func (h *CommandHandler) ensureInventory(e ecs.Entity) *components.Inventory {
	inv := ecs.Ensure[components.Inventory](h.a.sim, e)
	if cap := h.a.cfg.InventorySlots; len(inv.Slots) < cap {
		grown := make([]components.ItemStack, cap)
		copy(grown, inv.Slots)
		inv.Slots = grown
	}
	return inv
}

func (h *CommandHandler) withinRange(a, b ecs.Entity, r int) bool {
	sim := h.a.sim
	if !ecs.Has[components.Position](sim, a) || !ecs.Has[components.Position](sim, b) {
		return false
	}
	return ecs.Get[components.Position](sim, a).WithinRange(*ecs.Get[components.Position](sim, b), r)
}
