// Package collide 是纯 Go 的碰撞检测库：图元、几何查询、宽阶段引擎。
// 不依赖 cgo，不依赖任何游戏概念，可以在服务端、客户端或离线工具里复用。
//
// # 分层
//
//	primitive  图元：向量、AABB / OBB / 球 / 胶囊 / 线段 / 三角形 / 射线 / 平面。
//	           只有数据与它自己的方法（向量运算、包围盒运算、Bounds()、Kind()）。
//	kind       图元类型标识（编译期常量，零内存零开销）。
//	collide    算法与引擎：
//	              · 最近点与距离、重叠判定、接触（法向 / 深度 / 接触点）；
//	              · 射线相交、平移扫掠（防隧穿）；
//	              · 按类型双分发（TestShapes / ContactShapes / Raycast / SweepShapes）；
//	              · 圆弧与扇形（ArcBounds / Sector）——挥砍、技能锥的查询体积；
//	              · 宽阶段：Scanner（空间扫描器，默认数组实现）+ Engine（句柄生命周期
//	                与语义化查询 Overlap / OverlapSector / Raycast / SweepHit）。
//
// 图元的定义在 primitive，collide 用类型别名重导出，因此两处名字都能用、
// 而且只有一个定义（同标准库 os.FileMode = fs.FileMode 的手法）。
//
// # 几何约定
//
//   - float64；右手坐标系；单位由调用方决定。
//   - 图元都是值类型：可直接比较、可直接放进切片，没有指针字段。
//   - 接触法向：Normal 从第二个参数（b）指向第一个参数（a），
//     即「把 a 沿 +Normal 推开」即可分离。
//   - 扇形与圆弧的角度以 +X 为 0、绕 +Y 增加（俯视）；张角按最短弧解释（≤ 180°）。
//   - 未实现的图元组合会 panic，不会静默返回 false——静默的 false 会变成漏判，
//     而漏判的症状（偶尔打不中）极难定位。
//
// # 宽阶段引擎
//
// 引擎把「图元 → 宽阶段代理」的生命周期与空间查询绑在一起：
//
//  1. 创建时声明一次图元：h := e.Add(collide.Capsule{...})
//  2. 移动时整体替换：   e.Update(h, collide.Capsule{...})
//     （还在 fat AABB 内则完全不碰索引）
//  3. 查询走语义化动词： e.Overlap / e.OverlapSector / e.Raycast / e.SweepHit
//
// 业务代码只传句柄与数字，不应该再出现 AABB / OBB / Capsule 这些词；
// 命中结果（Result）里带着碰撞部位 Point，可直接用于特效、贴花、击退方向。
//
// 详细说明、用法示例与设计取舍见本目录 README.md。
package collide
