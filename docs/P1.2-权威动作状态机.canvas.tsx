import {
  Callout, Card, CardBody, CardHeader, Code, Grid, H1, H2,
  Pill, Row, Stack, Table, Text, useHostTheme,
} from "cursor/canvas";

function Node({ title, detail, active = false }: { title: string; detail: string; active?: boolean }) {
  const theme = useHostTheme();
  return (
    <div style={{
      flex: "1 1 150px", padding: 12, borderRadius: 6,
      border: `1px solid ${active ? theme.accent.primary : theme.stroke.secondary}`,
      background: active ? theme.fill.secondary : theme.bg.elevated,
    }}>
      <Text weight="semibold">{title}</Text>
      <Text size="small" tone="secondary" style={{ marginTop: 4 }}>{detail}</Text>
    </div>
  );
}

export default function P12ActionDesign() {
  const theme = useHostTheme();
  const mono = {
    whiteSpace: "pre-wrap" as const, fontFamily: "var(--font-mono, monospace)",
    fontSize: 12, lineHeight: 1.55, padding: 12, borderRadius: 6,
    background: theme.bg.editor, border: `1px solid ${theme.stroke.tertiary}`,
  };
  return (
    <Stack gap={20} style={{ padding: 24, maxWidth: 1120, margin: "0 auto" }}>
      <div>
        <Row gap={8}><Pill active>P1.2</Pill><Pill>服务端权威</Pill><Pill>源码归档</Pill></Row>
        <H1 style={{ marginTop: 12 }}>starve 权威动作状态机</H1>
        <Text tone="secondary">Attack、Chop、Mine、Pick 与 Craft 已统一接入权威时间线；协议版本 1.2。</Text>
      </div>

      <Callout tone="info" title="职责边界">
        <Text>
          Behavior 决定做什么；ControlSystem 仲裁移动与动作；ActionSystem 推进时序并 commit；
          SnapshotDelta 是组件状态事实来源，并将本 tick 的命中/血量领域事件原子同批下发。
        </Text>
      </Callout>

      <Row gap={8} align="stretch" wrap>
        <Node title="Player Command" detail="CommandHandler 转为 Intent" />
        <Node title="NPC Behavior" detail="AISystem 选择动作和目标" />
        <Node title="ActionControlQueue" detail="Start / Interrupt 保序" active />
        <Node title="ControlSystem · 93" detail="Move / Start / Cancel 仲裁" active />
        <Node title="ActionSystem · 94" detail="phase / commit / complete" active />
        <Node title="ActionExecutor" detail="Validate / Commit + 中断策略" active />
        <Node title="SnapshotDelta" detail="组件 delta + WorldEvent[]" active />
        <Node title="ActionOutcome" detail="完成 / 取消 / 拒绝语义" active />
      </Row>

      <section>
        <H2>稳定接口</H2>
        <Grid columns="1fr 1fr" gap={14}>
          <div style={mono}>
{`ActionIntent {
  Actor, Target
  Kind, Source
  RequestID
}

Executor {
  Timing(duration) ActionTiming
  Validate(world, actor, target) RejectReason
  Commit(world, actor, state) CommitResult
}`}
          </div>
          <div style={mono}>
{`ActionState {
  action_id, kind, target
  owner_request_id
  phase
  phase_start_tick
  phase_end_tick
  commit_tick, end_tick
}

ActionOutcome {
  entity_id, action_id
  request_id, kind
  result, reason, tick
}

WorldEvent {
  event_id, tick
  oneof impact | health_changed | outcome
}`}
          </div>
        </Grid>
        <Text size="small" tone="secondary" style={{ marginTop: 8 }}>
          ActionState 只在开始、阶段切换和移除时 dirty；客户端结合 SnapshotDelta.tick 推导动画进度。
        </Text>
      </section>

      <section>
        <H2>移动与动作如何配合</H2>
        <Callout tone="warning" title="移动不依赖具体动作类型">
          <Text>
            ActionIntent 是瞬时队列项，开始后已被消费。正在执行的动作通过 actor 身上的
            <Code> ActionState </Code>定位，因此无需反查 Intent。
          </Text>
        </Callout>
        <Table
          headers={["步骤", "负责模块", "行为"]}
          rows={[
            ["1", "CommandMove / AI Move", "只生成 MoveIntent / PathIntent；不查询具体动作类型"],
            ["2", "ControlSystem order 93", "按队列顺序处理 MoveIntent；移动量非零时用 MOVED 尝试中断 actor"],
            ["3", "TryInterrupt + ControlSystem", "移除 ActionState，发 ActionOutcome(CANCELED, MOVED)，再把方向或路径写入 Moveable"],
            ["4", "MoveSystem order 95", "同 tick 执行移动；完全不知道 Attack、Craft 等具体类型"],
          ]}
          rowTone={["neutral", "info", "info", "success"]}
          striped
        />
        <Text size="small" tone="secondary" style={{ marginTop: 8 }}>
          移动键不额外发送 ActionCancel；Move 本身就是中断原因。显式取消按钮发送 Cancel 控制意图。
          当前实现取消 actor 的现行动作；若未来允许跨 tick 延迟取消，协议应携带 action_id 防止误伤新动作。
        </Text>
      </section>

      <section>
        <H2>中断策略</H2>
        <Table
          headers={["动作阶段", "MOVED", "DAMAGED", "DEAD", "说明"]}
          rows={[
            ["Attack.Windup", "取消并移动", "取消", "取消", "尚未 commit，不产生伤害"],
            ["Attack.Recovery", "取消后摇并移动", "取消", "取消", "伤害已结算，不回滚"],
            ["Craft.Channeling", "取消并退款", "取消并退款", "取消", "复用现有 Crafting.Resume 规则"],
            ["不可打断阶段（未来）", "拒绝或延后", "按策略", "强制取消", "由 ActionPolicy 决定，MoveSystem 不分支"],
          ]}
          striped
        />
      </section>

      <section>
        <H2>完整输入到结算时序</H2>
        <Table
          headers={["序号 / tick 内顺序", "模块", "输入与输出"]}
          rows={[
            ["1", "Godot Input", "移动键更新本地 OwnMovementSim；Space 发 Automate(ANY)，F 发 Automate(ATTACK_ONLY)，点击操作发目标命令"],
            ["2", "CommandService → Gateway", "携带 input_epoch、seq、实体与目标；客户端可先播放自己的动作预测，但不预测伤害、掉落或资源"],
            ["3", "WorldActor / CommandHandler", "鉴权并把命令适配为 MoveIntent、PathIntent 或 StartActionIntent；Automate 在 AOI 中选目标，超距时先入 PathIntent"],
            ["4 · order 91", "AOISystem", "刷新 NPC 和自动行为使用的可见实体集合"],
            ["5 · order 92", "AISystem", "NPC 只做决策：移动、追击或 StartActionIntent；不直接扣血"],
            ["6 · order 93", "ControlSystem", "保序仲裁 Move / StartAction / Cancel；Validate 成功后停移动并创建 ActionState(WINDUP)"],
            ["7 · order 94", "ActionSystem", "冻结本 tick 到达 commit_tick 的动作集合，按 action_id/actor 排序后调用 Executor.Commit"],
            ["8", "ActionExecutor", "按 kind 选择 Attack / Work / Craft 策略；调用 Behavior 的 CanDo/Execute 完成具体业务"],
            ["9", "Behavior / Component", "Attackable、WorkTarget、Crafting 等只改变业务状态并 MarkDirty；不知道网络动画"],
            ["10 · order 95+", "Move / 生存 / Death systems", "推进已批准的移动、饥饿和死亡等持续状态；Dead 是死亡事实源"],
            ["11", "WorldActor tick 尾部", "处理 ActionCommitQueue，DrainTickEvents；生成同批 SnapshotDelta(component changes + WorldEvent[])"],
            ["12", "Godot WorldService", "先应用组件 delta，再发布去重事件；ActionState/Outcome 驱动动作，CombatImpact 驱动受击，Health/Dead 驱动 HUD 与魂魄"],
          ]}
          rowTone={["neutral", "neutral", "info", "neutral", "neutral", "info", "info", "info", "neutral", "neutral", "success", "success"]}
          striped
        />
        <Callout tone="info" title="攻击 800ms 的权威时间线">
          <Text>
            当前 50ms/tick：Attack Windup 8 tick，在第 400ms 的 commit_tick 权威命中并扣血；
            Recovery 再持续 8 tick，第 800ms 完成。网络延迟只影响“确认到达时间”，不会改变服务端结算 tick；
            客户端动画可以预测开始，收到 ActionState / Outcome 后校准或收尾。
          </Text>
        </Callout>
      </section>

      <section>
        <H2>取消、受击与死亡的单一事实源</H2>
        <Table
          headers={["场景", "谁发起", "服务端状态变化", "客户端表现"]}
          rows={[
            ["开始移动", "MoveIntent", "TryInterrupt(MOVED) 删除 ActionState；Crafting 按自身 Resume 规则退款；Moveable 同 tick 获批", "本地输入先停预测动作；Outcome 确认取消；移动动画继续"],
            ["显式取消", "CancelAction / CancelCraft", "ControlSystem 校验当前 ActionState/Crafting 后 TryInterrupt(EXPLICIT)", "Outcome(CANCELED, EXPLICIT) 立即清动作，不自然收尾"],
            ["受到有效攻击", "AttackExecutor.Commit", "先应用权威伤害；Applied > 0 时 TryInterrupt(DAMAGED)，并发 Impact + HealthChanged", "Impact(HIT) 播受击并让本地玩家红屏；HealthChanged 只更新数值和原因"],
            ["格挡 / 未命中", "AttackExecutor.Commit", "不扣血、不用 DAMAGED 中断；发 BLOCKED / MISS", "不播放 HIT 红屏；可接独立格挡/未命中表现"],
            ["死亡", "DeathSystem / Dead 组件", "Dead 持续存在，动作以 DEAD 原因取消；后续 StartAction 被 INVALID_ACTOR 拒绝", "Dead 驱动鱼人白蓝半透明漂浮魂魄和 HUD 灵魂状态；交互禁用但可移动观察"],
            ["自然完成", "ActionSystem", "Recovery 到 end_tick 后 CompleteAction，移除 ActionState 并发 COMPLETED", "当前非循环动画自然播到尾帧，不粗暴截断"],
          ]}
          rowTone={["neutral", "neutral", "warning", "neutral", "warning", "success"]}
          striped
        />
        <Text size="small" tone="secondary" style={{ marginTop: 8 }}>
          ActionIntent 仅在当前 tick 的 ControlQueue 中存在；动作开始后唯一可定位对象是 actor 身上的
          ActionState，因此移动、受击和死亡都不需要反查、删除旧 Intent。
        </Text>
      </section>

      <section>
        <H2>客户端预测与权威呈现边界</H2>
        <Grid columns="1fr 1fr" gap={14}>
          <Callout tone="info" title="允许预测">
            <Text>
              本地移动、自己动作动画开始、攻击命中时刻附近的视觉预备。预测只生成可撤销表现，
              500ms 未见 ActionState 会自动回退；移动输入立即取消本地动作预测。
            </Text>
          </Callout>
          <Callout tone="warning" title="禁止预测">
            <Text>
              伤害数值、资源扣除、掉落、制作产物、目标中断与死亡。它们只能由组件 delta 和
              WorldEvent 确认；event_id、source_action_id 与 target 用于去重和修正。
            </Text>
          </Callout>
        </Grid>
      </section>

      <section>
        <H2>Outcome 与可观测性</H2>
        <Grid columns="1fr 1fr" gap={14}>
          <Callout tone="info" title="协议只承载语义">
            <Text>
              <Code>world.action.outcome</Code> 只发送 COMPLETED、CANCELED、REJECTED
              及有界 reason；CombatImpact / HealthChanged 则进入 SnapshotDelta.events，
              与 Health dirty、ActionState removal 同批且只 drain 一次。
            </Text>
          </Callout>
          <Callout tone="info" title="低基数指标">
            <Text>
              started / committed / completed / canceled / rejected 按 kind、reason 聚合，
              impact 按 result、health change 按 cause 聚合；禁止 UID、entity_id、action_id 标签。
            </Text>
          </Callout>
        </Grid>
      </section>

      <Callout tone="warning" title="当前 AOI 边界">
        <Text>
          现有增量快照仍按世界广播，尚无逐连接事件过滤入口；WorldEvent 因此只携带 entity ID，
          不含 UID。未来在快照个性化层按“自己或可见实体”过滤，不回流到 ActionExecutor。
        </Text>
      </Callout>

      <section>
        <H2>实现状态</H2>
        <Grid columns={2} gap={12}>
          <Card><CardHeader>P1.2-A · 完成</CardHeader><CardBody><Text size="small">玩家与 NPC 攻击纵切；同一 AttackExecutor；commit tick 结算；移动/受击/死亡打断。</Text></CardBody></Card>
          <Card><CardHeader>P1.2-B · 完成</CardHeader><CardBody><Text size="small">Chop/Mine/Pick/Automate；入包、工具耐久和掉落副作用由提交链处理。</Text></CardBody></Card>
          <Card><CardHeader>P1.2-C · 完成</CardHeader><CardBody><Text size="small">制作收口；ActionState 管角色时序，Crafting 保留配方和退款数据。</Text></CardBody></Card>
          <Card><CardHeader>P1.2-D · 完成</CardHeader><CardBody><Text size="small">ActionExecutor、WorldEvent 原子增量、伤害原因与低基数指标已收口；AOI 事件过滤留在个性化层。</Text></CardBody></Card>
        </Grid>
      </section>
    </Stack>
  );
}
