package components

import (
	"encoding/json"
	"fmt"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

const DropChanceScale = 10000

// DropRule 是配置归一化后的单条表驱动掉落规则。
type DropRule struct {
	Kind               ItemKind
	MinCount, MaxCount int
	Chance             int
}

// UnmarshalJSON 同时接受旧 count 和新 min_count/max_count/chance。
func (r *DropRule) UnmarshalJSON(data []byte) error {
	var raw struct {
		Kind     string `json:"kind"`
		Count    int    `json:"count"`
		MinCount int    `json:"min_count"`
		MaxCount int    `json:"max_count"`
		Chance   *int   `json:"chance"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	kind, ok := ItemKindByName[raw.Kind]
	if !ok {
		return fmt.Errorf("unknown drop kind %q", raw.Kind)
	}
	if raw.Count > 0 {
		if raw.MinCount == 0 {
			raw.MinCount = raw.Count
		}
		if raw.MaxCount == 0 {
			raw.MaxCount = raw.Count
		}
	}
	chance := DropChanceScale
	if raw.Chance != nil {
		chance = *raw.Chance
	}
	if raw.MinCount <= 0 || raw.MaxCount < raw.MinCount {
		return fmt.Errorf("drop %q: invalid count range %d..%d", raw.Kind, raw.MinCount, raw.MaxCount)
	}
	if chance < 0 || chance > DropChanceScale {
		return fmt.Errorf("drop %q: chance must be 0..%d", raw.Kind, DropChanceScale)
	}
	*r = DropRule{Kind: kind, MinCount: raw.MinCount, MaxCount: raw.MaxCount, Chance: chance}
	return nil
}

type DropSourceCategory = game.DropSourceCategory

const (
	DropSourceResource = game.DropSourceCategory_DROP_SOURCE_CATEGORY_RESOURCE
	DropSourceCreature = game.DropSourceCategory_DROP_SOURCE_CATEGORY_CREATURE
)

// DropSource 持久化记录来源类别和具体 kind；掉落处理完成后消费该组件。
type DropSource struct {
	Category     DropSourceCategory
	ResourceKind ItemKind
	CreatureKind CreatureKind
}

type dropSourceCodec struct{}

func (dropSourceCodec) Encode(v DropSource) ([]byte, error) {
	return pb.Marshal(&game.DropSource{
		Category:     v.Category,
		ResourceKind: v.ResourceKind,
		CreatureKind: v.CreatureKind,
	})
}

func (dropSourceCodec) Decode(data []byte) (DropSource, error) {
	var source game.DropSource
	if err := pb.Unmarshal(data, &source); err != nil {
		return DropSource{}, err
	}
	return DropSource{
		Category:     source.Category,
		ResourceKind: source.ResourceKind,
		CreatureKind: source.CreatureKind,
	}, nil
}

func RegisterDropSource(w *ecs.World) {
	ecs.RegisterComponent(w, "DropSource", dropSourceCodec{})
}
