package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

// Crafting 制作中的状态（挂在玩家身上）：配方 + 剩余 tick + 材料（打断退款用）。
// 由 CraftSystem 每 tick 倒计时，到点世界产出并移除。
type Crafting struct {
	RecipeID    string
	TicksLeft   int
	Ingredients []ItemStack // craft 开始时从配方拷贝，被打断时按此退款
}

func init() { RegisterInterruptable[Crafting]() }

// Resume 实现 Interruptable：退回制作材料（优先进背包，放不下的原地生成掉落物）。
func (c *Crafting) Resume(w *ecs.World, e ecs.Entity, reason InterruptReason) {
	inv := ecs.Ensure[Inventory](w, e)
	var drop []ItemStack
	for _, ing := range c.Ingredients {
		added := inv.Add(ing.Kind, ing.Count, ing.MaxStack, 0)
		if added < ing.Count {
			drop = append(drop, ItemStack{Kind: ing.Kind, Count: ing.Count - added})
		}
	}
	ecs.MarkDirty[Inventory](w, e)
	if len(drop) > 0 {
		pos := ecs.Get[Position](w, e)
		loot := w.CreateEntity()
		ecs.Add(w, loot, *pos)
		ecs.Add(w, loot, Lootable{Items: drop})
	}
	// 通知取消（客户端停动画）：副作用走世界队列，tick 边界由 world actor 翻译成推送。
	// 这里按道理crafting不应该知道Player的存在，后续要想办法优化下这里
	uid := ""
	if ecs.Has[Player](w, e) {
		uid = ecs.Get[Player](w, e).UID
	}
	w.Emit(&proto.CraftDone{Uid: uid, RecipeId: c.RecipeID, Success: false})
}

// 编译期断言：Crafting 必须实现 Interruptable。
var _ Interruptable = (*Crafting)(nil)

type craftingCodec struct{}

func (craftingCodec) Encode(v Crafting) ([]byte, error) {
	return pb.Marshal(&game.Crafting{
		RecipeId:    v.RecipeID,
		TicksLeft:   int64(v.TicksLeft),
		Ingredients: slotsToProto(v.Ingredients),
	})
}

func (craftingCodec) Decode(b []byte) (Crafting, error) {
	var c game.Crafting
	if err := pb.Unmarshal(b, &c); err != nil {
		return Crafting{}, err
	}
	ingredients := make([]ItemStack, 0, len(c.Ingredients))
	for _, s := range c.Ingredients {
		ingredients = append(ingredients, ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack)})
	}
	return Crafting{RecipeID: c.RecipeId, TicksLeft: int(c.TicksLeft), Ingredients: ingredients}, nil
}

func RegisterCrafting(w *ecs.World) {
	ecs.RegisterComponent(w, "Crafting", craftingCodec{})
}
