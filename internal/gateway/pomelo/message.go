package pomelo

import "errors"

// 消息类型（flag 的 2-4 位，即 type<<1）。
const (
	MsgRequest  byte = 0x00 // ----000-  <mid> <route>
	MsgNotify   byte = 0x01 // ----001-  <route>
	MsgResponse byte = 0x02 // ----010-  <mid>
	MsgPush     byte = 0x03 // ----011-  <route>
)

const (
	msgTypeMask       = 0x07
	routeCompressMask = 0x01
	maxRouteLen       = 255 // route 用 1 字节长度前缀
)

var (
	ErrInvalidMessageType = errors.New("pomelo: invalid message type")
	ErrMessageTooShort    = errors.New("pomelo: message too short")
	ErrRouteTooLong       = errors.New("pomelo: route too long")
	ErrRouteCompressed    = errors.New("pomelo: compressed route not supported yet（二期字典）")
)

// Message 是解析后的 pomelo 业务消息。
type Message struct {
	Type  byte   // Request / Notify / Response / Push
	ID    uint64 // request/response 的 mid；notify/push 为 0
	Route string // request/notify/push 有；response 无
	Data  []byte // 消息体（protobuf 编码）
}

func routable(t byte) bool {
	return t == MsgRequest || t == MsgNotify || t == MsgPush
}

// EncodeMessage 编码：flag(1B) [+ mid varint] [+ route(1B 长度+字符串)] + data。
// MVP 不做 route 字典压缩（flag 压缩位为 0）。
func EncodeMessage(m *Message) ([]byte, error) {
	if m == nil || m.Type > MsgPush {
		return nil, ErrInvalidMessageType
	}
	buf := make([]byte, 0, 32+len(m.Data))
	buf = append(buf, m.Type<<1)
	if m.Type == MsgRequest || m.Type == MsgResponse {
		buf = appendVarint(buf, m.ID)
	}
	if routable(m.Type) {
		if len(m.Route) > maxRouteLen {
			return nil, ErrRouteTooLong
		}
		buf = append(buf, byte(len(m.Route)))
		buf = append(buf, m.Route...)
	}
	buf = append(buf, m.Data...)
	return buf, nil
}

// DecodeMessage 解析消息。
func DecodeMessage(data []byte) (*Message, error) {
	if len(data) < 1 {
		return nil, ErrMessageTooShort
	}
	flag := data[0]
	m := &Message{Type: (flag >> 1) & msgTypeMask}
	if m.Type > MsgPush {
		return nil, ErrInvalidMessageType
	}
	off := 1
	if m.Type == MsgRequest || m.Type == MsgResponse {
		id, n, err := decodeVarint(data[off:])
		if err != nil {
			return nil, err
		}
		m.ID = id
		off += n
	}
	if routable(m.Type) {
		if flag&routeCompressMask != 0 {
			return nil, ErrRouteCompressed
		}
		if off >= len(data) {
			return nil, ErrMessageTooShort
		}
		rl := int(data[off])
		off++
		if off+rl > len(data) {
			return nil, ErrMessageTooShort
		}
		m.Route = string(data[off : off+rl])
		off += rl
	}
	m.Data = data[off:]
	return m, nil
}

func appendVarint(buf []byte, n uint64) []byte {
	for n >= 0x80 {
		buf = append(buf, byte(n)|0x80)
		n >>= 7
	}
	return append(buf, byte(n))
}

func decodeVarint(data []byte) (uint64, int, error) {
	var n uint64
	for i, b := range data {
		if i >= 10 {
			return 0, 0, ErrMessageTooShort
		}
		n |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return n, i + 1, nil
		}
	}
	return 0, 0, ErrMessageTooShort
}
