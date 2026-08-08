package world

import (
	"log/slog"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// CommandHandler 处理玩家命令（命令是应用逻辑，世界 actor 负责世界本身）。
// 每个命令：所有权校验 → 目标/距离校验 → 语义执行 → 变更进 dirty（快照/存档）。
type CommandHandler struct {
	a *WorldActor
}

// bareHandsEfficiency 徒手效率：只能采集（PICK），砍/挖必须装备匹配工具。
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
	if want == components.WorkPick {
		return bareHandsEfficiency, false // 采集永远徒手（手持工具不影响）
	}
	if !ecs.Has[components.Equipped](a.sim, player) {
		return 0, false // 砍/挖必须工具
	}
	eq := ecs.Get[components.Equipped](a.sim, player)
	if eq.Kind == 0 {
		return 0, false
	}
	t := a.template(eq.Kind)
	if t.Tool == nil || t.Tool.Action != want {
		return 0, false
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
