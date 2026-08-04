package ecs

// Entity 是实体的唯一标识：一个密集分配的 uint64 ID，本身不携带数据。
//
// 刻意用定义类型而非别名（type Entity = uint64），
// 使 API 签名中的 Entity 与裸 uint64 不可混用，由编译器保证类型安全。
type Entity uint64

// NullEntity 是无效实体哨兵，CreateEntity 永远不会返回它。
const NullEntity Entity = 0
