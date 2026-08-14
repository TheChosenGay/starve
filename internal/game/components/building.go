package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// BuildingKind 建筑类型（单一事实来源 = proto 枚举；配置用名字，内部用枚举）。
type BuildingKind = game.BuildingKind

// 常用建筑类型常量。
const (
	BuildingCampfire = game.BuildingKind_BUILDING_KIND_CAMPFIRE
	BuildingWall     = game.BuildingKind_BUILDING_KIND_WALL
)

// BuildingKindByName 配置字符串 → 建筑类型（新建筑 = 枚举值 + 这里加一行 + 放置逻辑）。
var BuildingKindByName = map[string]BuildingKind{
	"campfire": BuildingCampfire,
	"wall":     BuildingWall,
}

// Building 建筑组件：Kind 类型 + 占格尺寸（Width×Height）+ Placed 是否已放置。
// 未放置：无 Position、不阻挡（蓝图/物品态）；Place(pos) 后补 Position 并逐格 SetBlocked。
type Building struct {
	Kind          BuildingKind
	Width, Height int
	Placed        bool
}

type buildingCodec struct{}

func (buildingCodec) Encode(v Building) ([]byte, error) {
	return pb.Marshal(&game.Building{Kind: v.Kind, Width: int32(v.Width), Height: int32(v.Height), Placed: v.Placed})
}

func (buildingCodec) Decode(b []byte) (Building, error) {
	var m game.Building
	if err := pb.Unmarshal(b, &m); err != nil {
		return Building{}, err
	}
	return Building{Kind: m.Kind, Width: int(m.Width), Height: int(m.Height), Placed: m.Placed}, nil
}
