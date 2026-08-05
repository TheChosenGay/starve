package actor

import "fmt"

// LocalLookupAddr 是本地节点的地址。PID.Address 等于它时走本地注册表；
// 未来 M6（Cluster）中，其他节点地址（如 "node-2:8080"）走远程传输。
const LocalLookupAddr = "local"

// PID 是 actor 的进程标识，寻址单位：{Address, ID}。
// ID 形如 "kind/name"，如 "world/room-1"、"agent/abc123"。
// 设计为值类型，拷贝安全，可在消息间自由传递。
type PID struct {
	Address string
	ID      string
}

// String 返回可读形式，如 "local/world/room-1"。
func (p *PID) String() string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s/%s", p.Address, p.ID)
}
