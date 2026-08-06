package components

// Position 是世界坐标（模拟层数据，服务器权威）。
// 端上只消费它做渲染，不参与计算。
type Position struct {
	X, Y int
}
