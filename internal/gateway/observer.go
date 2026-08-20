package gateway

// RejectReason 是有界拒绝原因，禁止放入 UID、连接 ID 或自由文本。
type RejectReason string

const (
	RejectBadMessage       RejectReason = "bad_message"
	RejectUnknownRoute     RejectReason = "unknown_route"
	RejectUnauthenticated  RejectReason = "unauthenticated"
	RejectBadToken         RejectReason = "bad_token"
	RejectWorldUnavailable RejectReason = "world_unavailable"
)

// GatewayStats 是网关状态或事件的不可变观测快照。
type GatewayStats struct {
	RawConnections    int
	Sessions          int
	RejectReason      RejectReason
	FullSnapshotBytes int
}

// GatewayObserver 隔离网关业务与具体观测 SDK。
type GatewayObserver interface {
	ObserveGateway(GatewayStats)
}

type GatewayObserverFunc func(GatewayStats)

func (f GatewayObserverFunc) ObserveGateway(stats GatewayStats) { f(stats) }
