package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// CreatureState 生物行为状态（AI 状态机）。
type CreatureState int32

const (
	CreatureIdle   CreatureState = 0 // 待机/游荡（无目标）
	CreatureChase  CreatureState = 1 // 追击（目标在感知内、攻击范围外）
	CreatureAttack CreatureState = 2 // 攻击（目标在攻击范围内）
	CreatureFlee   CreatureState = 3 // 逃跑（血量低/受惊）
)

// AI 生物行为状态机 + 受击标记。
// 受击事件 = LastHitBy + LastHitAt（tick 窗口），窗口过期即失效，无需主动清空。
type AI struct {
	State          CreatureState
	Target         ecs.Entity
	FleeHP         int // 血量 <= 此值切 flee（0 = 永不逃跑）
	LastHitBy      ecs.Entity
	LastHitAt      int // 被打时的世界 tick（DayCycle.Phase）
	HitMemoryTicks int // 受击记忆窗口（tick）
	// ThreatDecayTicks 仇恨衰减间隔（tick）：每这么多 tick 仇恨 -1。
	// 0 = 每 tick 衰减（旧行为）。见 config.CreatureTemplate.ThreatDecayTicks。
	ThreatDecayTicks int
	Cooldown         int            // 攻击冷却剩余 tick
	HostileKinds     []CreatureKind // 视为敌对的生物类型（玩家隐式敌对）
	HostilePlayers   bool           // 玩家是否视为敌对（false = 友好）
	// Phase 是**多阶段 Boss** 的当前阶段（0 = 未分阶段）。
	// 由行为树的 EnterPhase 节点写入；持久化在组件里，所以阶段切换后
	// 不会因为行为树每 tick 从根重新评估而回退。
	Phase int
	// Phase2HP 进入二阶段的血量阈值（0 = 不分阶段），从模板拷贝，
	// 供行为树的 Phase2Ready 条件判断。
	Phase2HP int
	// Leash 拴绳半径（格）：目标超出这个**曼哈顿**距离就放弃追击、
	// 清空仇恨。0 = 用缺省规则（4 + AOI.Radius）。
	//
	// 为什么单独配置：缺省规则把拴绳绑在感知半径上，而感知半径通常很小
	// （狼 6 → 拴绳仅 10 格），导致"打一下就往后退两步，狼就不追了"。
	// 掠食者应当比感知范围追得更远（闻到血腥味），所以按生物类型单独给。
	Leash int
}

// WasHitRecently 窗口内是否被打（受击事件有效判断）。
func (a *AI) WasHitRecently(now int) bool {
	return a.LastHitBy != 0 && now-a.LastHitAt <= a.HitMemoryTicks
}

// MarkAttacked 受击标记：记录本 tick 被谁打（AI 仇恨/行为输入）。
// 由 Attackable.ApplyDamage 在受击结算时调用，AI 自身的受击反馈内聚在这里。
func (a *AI) MarkAttacked(w *ecs.World, e ecs.Entity, attacker ecs.Entity) {
	a.LastHitBy = attacker
	if dc, ok := ecs.TryResource[DayCycle](w); ok {
		a.LastHitAt = dc.Phase
	}
	ecs.MarkDirty[AI](w, e)
}

type aiCodec struct{}

func (aiCodec) Encode(v AI) ([]byte, error) {
	return pb.Marshal(&game.AI{
		State:            int32(v.State),
		Target:           uint64(v.Target),
		FleeHp:           int32(v.FleeHP),
		LastHitBy:        uint64(v.LastHitBy),
		LastHitAt:        int32(v.LastHitAt),
		HitMemoryTicks:   int32(v.HitMemoryTicks),
		ThreatDecayTicks: int32(v.ThreatDecayTicks),
		Cooldown:         int32(v.Cooldown),
		HostileKinds:     append([]CreatureKind(nil), v.HostileKinds...),
		HostilePlayers:   v.HostilePlayers,
		Phase:            int32(v.Phase),
		Phase2Hp:         int32(v.Phase2HP),
		Leash:            int32(v.Leash),
	})
}

func (aiCodec) Decode(b []byte) (AI, error) {
	var m game.AI
	if err := pb.Unmarshal(b, &m); err != nil {
		return AI{}, err
	}
	out := AI{
		State:            CreatureState(m.State),
		Target:           ecs.Entity(m.Target),
		FleeHP:           int(m.FleeHp),
		LastHitBy:        ecs.Entity(m.LastHitBy),
		LastHitAt:        int(m.LastHitAt),
		HitMemoryTicks:   int(m.HitMemoryTicks),
		ThreatDecayTicks: int(m.ThreatDecayTicks),
		Cooldown:         int(m.Cooldown),
	}
	out.HostileKinds = append([]CreatureKind(nil), m.HostileKinds...)
	out.HostilePlayers = m.HostilePlayers
	out.Phase = int(m.Phase)
	out.Phase2HP = int(m.Phase2Hp)
	out.Leash = int(m.Leash)
	return out, nil
}

func RegisterAI(w *ecs.World) {
	ecs.RegisterComponent(w, "AI", aiCodec{})
}
