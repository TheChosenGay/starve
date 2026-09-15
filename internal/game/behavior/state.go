package behavior

// MemoryState 是一个纯内存的 NodeStateStore 实现（测试与小工具用）。
//
// 生产环境的实现是 ECS 组件（见 components.BehaviorTree），它把同样的
// 数据编码进快照；这里只服务单元测试，让行为树能脱离 ECS 单独验证。
type MemoryState struct {
	child map[NodeID]uint8
	ints  map[NodeID]int
}

// NewMemoryState 建一个空的运行态。
func NewMemoryState() *MemoryState {
	return &MemoryState{child: map[NodeID]uint8{}, ints: map[NodeID]int{}}
}

// RunningChild 实现 NodeStateStore。
func (m *MemoryState) RunningChildOf(id NodeID) (uint8, bool) {
	v, ok := m.child[id]
	return v, ok
}

// SetRunningChild 实现 NodeStateStore。
func (m *MemoryState) SetRunningChildOf(id NodeID, idx uint8) { m.child[id] = idx }

// ClearRunningChild 实现 NodeStateStore。
func (m *MemoryState) ClearRunningChildOf(id NodeID) { delete(m.child, id) }

// Int 实现 NodeStateStore。
func (m *MemoryState) IntOf(id NodeID) int { return m.ints[id] }

// SetInt 实现 NodeStateStore。
func (m *MemoryState) SetIntOf(id NodeID, v int) { m.ints[id] = v }

// ClearInt 实现 NodeStateStore。
func (m *MemoryState) ClearIntOf(id NodeID) { delete(m.ints, id) }
