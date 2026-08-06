// Package pomelo 实现 pomelo 协议编解码（蓝本：cherry net/parser/pomelo 与 pomelo 官方协议）。
// 分层：
//   - packet 层：信封（1B type + 3B 长度 + body），握手/心跳/数据/踢线；
//   - message 层：业务消息（flag/mid/route），request/notify/response/push。
package pomelo

import "errors"

// packet 类型（1 字节）。
const (
	PacketHandshake    byte = 0x01
	PacketHandshakeAck byte = 0x02
	PacketHeartbeat    byte = 0x03
	PacketData         byte = 0x04
	PacketKick         byte = 0x05
)

const (
	PacketHeaderSize = 4       // 1B type + 3B length（大端）
	MaxPacketSize    = 1 << 24 // 16MB
)

var (
	ErrInvalidPacketType = errors.New("pomelo: invalid packet type")
	ErrPacketTooLarge    = errors.New("pomelo: packet too large")
	ErrPacketTruncated   = errors.New("pomelo: packet truncated")
)

// Packet 是一个 pomelo 包：类型 + 负载。
type Packet struct {
	Type byte
	Data []byte
}

func validPacketType(t byte) bool {
	return t >= PacketHandshake && t <= PacketKick
}

// EncodePacket 组包：1B type + 3B 大端长度 + body。
func EncodePacket(t byte, data []byte) ([]byte, error) {
	if !validPacketType(t) {
		return nil, ErrInvalidPacketType
	}
	if len(data) > MaxPacketSize {
		return nil, ErrPacketTooLarge
	}
	buf := make([]byte, PacketHeaderSize+len(data))
	buf[0] = t
	buf[1] = byte(len(data) >> 16)
	buf[2] = byte(len(data) >> 8)
	buf[3] = byte(len(data))
	copy(buf[PacketHeaderSize:], data)
	return buf, nil
}

// DecodePackets 拆包：一个 WS 帧可能包含多个 pomelo 包。
func DecodePackets(buf []byte) ([]*Packet, error) {
	var packets []*Packet
	for len(buf) >= PacketHeaderSize {
		t := buf[0]
		if !validPacketType(t) {
			return nil, ErrInvalidPacketType
		}
		size := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		if size > MaxPacketSize {
			return nil, ErrPacketTooLarge
		}
		if len(buf) < PacketHeaderSize+size {
			return nil, ErrPacketTruncated
		}
		packets = append(packets, &Packet{Type: t, Data: buf[PacketHeaderSize : PacketHeaderSize+size]})
		buf = buf[PacketHeaderSize+size:]
	}
	if len(buf) != 0 {
		return nil, ErrPacketTruncated
	}
	return packets, nil
}
