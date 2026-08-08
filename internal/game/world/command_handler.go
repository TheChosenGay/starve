package world

import (
	"encoding/json"
	"log/slog"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/pkg/proto"
)

// CommandHandler 处理玩家命令（命令是应用逻辑，世界 actor 负责世界本身）。
// 每个命令：所有权校验 → 目标/距离校验 → 语义执行 → 变更进 dirty（快照/存档）。
type CommandHandler struct {
	a *WorldActor
}

// bareHandsEfficiency 徒手效率：所有动作都能做但很慢（效率 1）。
// 不能收紧为"砍/挖必须工具"——否则木头/燧石只能由工具产出，形成"没工具→没材料"死锁；
// 工具的意义是效率提升（如斧头 5 倍）。后续可给特定资源加 requires_tool + 新手工具投放。
const bareHandsEfficiency = 1

// Handle 按命令类型分发。
func (h *CommandHandler) Handle(c Command) {
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
	case CommandDrop:
		h.drop(c)
	case CommandCancelCraft:
		h.cancelCraft(c)
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
	p := ecs.Get[components.Position](h.a.sim, m.Entity)
	ecs.Set(h.a.sim, m.Entity, components.Position{X: p.X + m.DX, Y: p.Y + m.DY})
	// 走动打断制作（客户端主动取消的一种）
	if k, key, ok := h.checkInterrupt(m.Entity); ok {
		h.onInterrupt(m.Entity, k, key)
	}
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
	if ecs.Has[components.Dead](h.a.sim, at.Target) {
		slog.Debug("attack rejected: target dead", "uid", c.UID, "target", at.Target)
		return // 尸体不可攻击
	}
	if !ecs.Has[components.Health](h.a.sim, at.Target) {
		slog.Debug("attack rejected: target has no health", "uid", c.UID, "target", at.Target)
		return // 只有带 Health 的实体（生物/玩家）可被攻击；环境物走 Workable
	}
	if !h.withinRange(at.Attacker, at.Target, 2) {
		slog.Debug("attack rejected: out of range", "uid", c.UID, "attacker", at.Attacker, "target", at.Target)
		return // 距离不够
	}
	hp := ecs.Get[components.Health](h.a.sim, at.Target)
	cur := hp.Cur - h.a.cfg.AttackDamage
	if cur < 0 {
		cur = 0
	}
	ecs.Set(h.a.sim, at.Target, components.Health{Cur: cur, Max: hp.Max})

	// 受击打断：有可打断组件（如制作）则移除并统一处理（退款 + 推送取消）。
	if k, key, ok := h.checkInterrupt(at.Target); ok {
		h.onInterrupt(at.Target, k, key)
	}
}

// interruptibleKind 描述一种可打断组件：interrupt 检查/移除并取恢复标识，resume 执行恢复逻辑。
type interruptibleKind struct {
	interrupt func(e ecs.Entity) (key string, ok bool)
	resume    func(h *CommandHandler, e ecs.Entity, key string)
}

// interruptibleKinds 可打断组件清单：新增可打断行为（蓄力、吟唱等）在这里加一项。
func (h *CommandHandler) interruptibleKinds() []interruptibleKind {
	return []interruptibleKind{
		{
			interrupt: func(e ecs.Entity) (string, bool) {
				if !ecs.Has[components.Crafting](h.a.sim, e) {
					return "", false
				}
				c := ecs.Get[components.Crafting](h.a.sim, e)
				ecs.Remove[components.Crafting](h.a.sim, e)
				return c.InterruptKey(), true
			},
			resume: func(h *CommandHandler, e ecs.Entity, key string) { h.resumeCraft(e, key) },
		},
	}
}

// checkInterrupt 检查实体是否有可打断组件：有则移除并返回命中的 kind 与恢复标识。
func (h *CommandHandler) checkInterrupt(e ecs.Entity) (interruptibleKind, string, bool) {
	for _, k := range h.interruptibleKinds() {
		if key, ok := k.interrupt(e); ok {
			return k, key, true
		}
	}
	return interruptibleKind{}, "", false
}

// onInterrupt 打断后的统一入口：按命中的 kind 执行对应恢复逻辑。
func (h *CommandHandler) onInterrupt(e ecs.Entity, kind interruptibleKind, key string) {
	kind.resume(h, e, key)
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
	if k, key, ok := h.checkInterrupt(d.Player); ok {
		h.onInterrupt(d.Player, k, key)
	}
}

// resumeCraft 制作的恢复逻辑：退回材料（优先进背包，放不下的丢成掉落物）+ 推送取消。
func (h *CommandHandler) resumeCraft(player ecs.Entity, recipeID string) {
	a := h.a
	recipe, ok := a.recipes[recipeID]
	if !ok {
		return
	}
	inv := ecs.Ensure[components.Inventory](a.sim, player)
	var drop []components.ItemStack
	for _, ing := range recipe.Ingredients {
		t := a.template(ing.Kind)
		durability := 0
		if t.Tool != nil {
			durability = t.Tool.Durability
		}
		if added := inv.Add(ing.Kind, ing.Count, t.StackSize, durability); added < ing.Count {
			drop = append(drop, components.ItemStack{Kind: ing.Kind, Count: ing.Count - added})
		}
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
	if len(drop) > 0 {
		pos := ecs.Get[components.Position](a.sim, player)
		e := a.sim.CreateEntity()
		ecs.Add(a.sim, e, *pos)
		ecs.Add(a.sim, e, components.Loot{Items: drop})
	}
	h.a.outbox = append(h.a.outbox, PushEffect{
		Route:   proto.RouteCraftDone,
		Payload: &proto.CraftDone{Uid: h.a.players[player], RecipeId: recipeID, Success: false},
	})
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
	inv := ecs.Ensure[components.Inventory](a.sim, d.Player)
	if !inv.Take(d.Kind, d.Count) {
		return // 数量不足
	}
	ecs.MarkDirty[components.Inventory](a.sim, d.Player)

	pos := ecs.Get[components.Position](a.sim, d.Player)
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, *pos)
	ecs.Add(a.sim, e, components.Loot{Items: []components.ItemStack{{Kind: d.Kind, Count: d.Count}}})
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
	inv := ecs.Ensure[components.Inventory](a.sim, player)
	for _, ing := range recipe.Ingredients {
		if inv.Stack(ing.Kind).Count < ing.Count {
			return CraftResult{Message: "insufficient materials"}
		}
	}
	if cur := inv.Stack(recipe.Output.Kind); cur.Count > 0 {
		if max := a.template(recipe.Output.Kind).StackSize; max > 0 && cur.Count+recipe.Output.Count > max {
			return CraftResult{Message: "output stack full"}
		}
	}
	for _, ing := range recipe.Ingredients {
		inv.Take(ing.Kind, ing.Count)
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
	ecs.Add(a.sim, player, components.Crafting{RecipeID: recipe.ID, TicksLeft: recipe.Ticks})
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
//   - 目标带 Workable 且 Action 匹配命令期望动作；
//   - 工具（Equipped → 模板 Tool）动作匹配才用工具效率并扣耐久；徒手效率 1；
//   - PICK 产物直接进背包，采空原地保留；CHOP/MINE 归零 → Dead → processDrops 掉落。
func (h *CommandHandler) work(uid string, player, target ecs.Entity, want components.WorkAction) {
	a := h.a
	if a.players[player] != uid {
		return // 只能控制自己的实体
	}
	if !ecs.Has[components.Workable](a.sim, target) || !a.sim.IsAlive(target) {
		return // 目标不可工作
	}
	if !h.withinRange(player, target, 2) {
		return // 距离不够
	}
	w := ecs.Get[components.Workable](a.sim, target)
	if w.Action != want {
		return // 动作不匹配（chop 砍矿/挖树无效）
	}
	if w.WorkLeft <= 0 {
		return // 已耗尽（重复采集/砍挖不再生效，避免重复挂 Respawn/Dead）
	}
	eff, isTool := h.toolEfficiency(uid, player, want)
	if eff <= 0 {
		return // 工具不对口（如拿斧头挖矿）
	}

	if want == components.WorkPick {
		// 采集产物直接进背包
		h.addItem(player, w.Kind, 1)
		ecs.MarkDirty[components.Inventory](a.sim, player)
	}

	w.WorkLeft -= eff
	if w.WorkLeft <= 0 {
		w.WorkLeft = 0
		ecs.Set(a.sim, target, *w)
		if want == components.WorkPick {
			// 采空：原地保留；模板配了 respawn_ticks 则挂重生标记（RespawnSystem 到点恢复）
			if t := a.template(w.Kind); t.RespawnTicks > 0 {
				ecs.Add(a.sim, target, components.Respawn{Ticks: t.RespawnTicks})
			}
		} else {
			// 砍/挖完 → Dead → 同 tick processDrops 就地掉落
			ecs.Add(a.sim, target, components.Dead{Reason: "worked"})
		}
	} else {
		ecs.Set(a.sim, target, *w)
	}

	if isTool {
		h.degradeTool(uid, player)
	}
}

// toolEfficiency 返回本次工作效率与是否使用工具。
// 徒手效率 1（过渡）；装备工具动作匹配才用工具效率，不匹配拒绝。
func (h *CommandHandler) toolEfficiency(uid string, player ecs.Entity, want components.WorkAction) (int, bool) {
	a := h.a
	if !ecs.Has[components.Equipped](a.sim, player) {
		return bareHandsEfficiency, false // 徒手慢做（避免材料死锁）
	}
	eq := ecs.Get[components.Equipped](a.sim, player)
	if eq.Kind == 0 {
		return bareHandsEfficiency, false
	}
	t := a.template(eq.Kind)
	if t.Tool == nil || t.Tool.Action != want {
		return 0, false // 装备了不匹配工具 → 拒绝（拿斧头挖矿无效）
	}
	return t.Tool.Efficiency, true
}

// degradeTool 扣工具耐久（物品实例状态）；归零移除并自动卸下。
func (h *CommandHandler) degradeTool(uid string, player ecs.Entity) {
	a := h.a
	eq := ecs.Get[components.Equipped](a.sim, player)
	inv := ecs.Ensure[components.Inventory](a.sim, player)
	if inv.Degrade(eq.Kind) {
		ecs.Remove[components.Equipped](a.sim, player)
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
}

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
		if ecs.Has[components.Equipped](a.sim, e.Player) {
			ecs.Remove[components.Equipped](a.sim, e.Player)
		}
		return
	}
	if a.template(e.Kind).Tool == nil {
		return // 不是工具
	}
	inv := ecs.Ensure[components.Inventory](a.sim, e.Player)
	if inv.Stack(e.Kind).Count <= 0 {
		return // 背包里没有
	}
	ecs.Ensure[components.Equipped](a.sim, e.Player)
	ecs.Set(a.sim, e.Player, components.Equipped{Kind: e.Kind})
}

func (h *CommandHandler) pickup(c Command) {
	p, ok := c.Data.(PickupData)
	if !ok {
		return
	}
	a := h.a
	if a.players[p.Player] != c.UID {
		return
	}
	if !ecs.Has[components.Loot](a.sim, p.Target) || !a.sim.IsAlive(p.Target) {
		return
	}
	if !h.withinRange(p.Player, p.Target, 2) {
		return
	}
	loot := ecs.Get[components.Loot](a.sim, p.Target)
	inv := ecs.Ensure[components.Inventory](a.sim, p.Player)
	for _, s := range loot.Items {
		inv.Add(s.Kind, s.Count, h.a.template(s.Kind).StackSize, 0)
	}
	ecs.MarkDirty[components.Inventory](a.sim, p.Player)
	a.sim.DestroyEntity(p.Target)
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
	inv := ecs.Ensure[components.Inventory](a.sim, u.Player)
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
	inv := ecs.Ensure[components.Inventory](a.sim, player)
	inv.Add(kind, count, t.StackSize, durability)
}

func (h *CommandHandler) withinRange(a, b ecs.Entity, r int) bool {
	sim := h.a.sim
	if !ecs.Has[components.Position](sim, a) || !ecs.Has[components.Position](sim, b) {
		return false
	}
	return ecs.Get[components.Position](sim, a).WithinRange(*ecs.Get[components.Position](sim, b), r)
}
