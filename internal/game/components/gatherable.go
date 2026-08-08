package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// ResourceKind 资源类型（单一事实来源 = proto 枚举）。
// 新资源：加枚举值 + 加模板表条目（样式/颜色/掉落等静态属性），游戏代码用常量名。
type ResourceKind = game.ResourceKind

// 常用资源类型常量（配置表/命令里用这些名字）。
const (
	ResourceBerry = game.ResourceKind_RESOURCE_KIND_BERRY
	ResourceWood  = game.ResourceKind_RESOURCE_KIND_WOOD
	ResourceFlint = game.ResourceKind_RESOURCE_KIND_FLINT
)

// Gatherable 可采集目标（浆果丛/矿脉等）：采一次 Count--，耗尽后组件被移除。
type Gatherable struct {
	Kind  ResourceKind
	Count int
}

type gatherableCodec struct{}

func (gatherableCodec) Encode(v Gatherable) ([]byte, error) {
	return pb.Marshal(&game.Gatherable{Kind: v.Kind, Count: int32(v.Count)})
}

func (gatherableCodec) Decode(b []byte) (Gatherable, error) {
	var g game.Gatherable
	if err := pb.Unmarshal(b, &g); err != nil {
		return Gatherable{}, err
	}
	return Gatherable{Kind: g.Kind, Count: int(g.Count)}, nil
}
