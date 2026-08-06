package pomelo

import (
	"errors"

	"github.com/TheChosenGay/combet"
)

// Scheme 实现 comet.MsgScheme（包名 comet，模块路径 github.com/TheChosenGay/combet）：
// pomelo packet 层 ⇄ 语义消息。
//
// 注意：pomelo 的"握手纯净 + 之后 login"流程与 combet Core 的"握手即鉴权"
// 模型不匹配，M4 网关由 agent actor 直接使用本包编解码，不经 combet.Core；
// 实现 MsgScheme 是为了协议组件复用与测试。
type Scheme struct{}

// NewScheme 创建 pomelo 协议方案。
func NewScheme() *Scheme { return &Scheme{} }

// Decode 把线路字节解析为语义消息（单包；多包场景由上层拆帧）。
func (s *Scheme) Decode(data []byte) (*comet.Msg, error) {
	packets, err := DecodePackets(data)
	if err != nil {
		return nil, err
	}
	if len(packets) != 1 {
		return nil, errors.New("pomelo: 一个 WS 帧应只含一个 packet")
	}
	t, err := semanticType(packets[0].Type)
	if err != nil {
		return nil, err
	}
	return &comet.Msg{Type: t, Payload: packets[0].Data}, nil
}

// Encode 把语义消息编码为线路字节。
func (s *Scheme) Encode(msg *comet.Msg) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("pomelo: nil msg")
	}
	t, err := packetType(msg.Type)
	if err != nil {
		return nil, err
	}
	return EncodePacket(t, msg.Payload)
}

func semanticType(t byte) (comet.MsgType, error) {
	switch t {
	case PacketHandshake:
		return comet.MsgHandshakeReq, nil
	case PacketHandshakeAck:
		return comet.MsgHandshakeAck, nil
	case PacketHeartbeat:
		return comet.MsgHeartbeatReq, nil
	case PacketData:
		return comet.MsgData, nil
	case PacketKick:
		return comet.MsgKick, nil
	default:
		return comet.MsgNone, ErrInvalidPacketType
	}
}

func packetType(t comet.MsgType) (byte, error) {
	switch t {
	case comet.MsgHandshakeReq, comet.MsgHandshakeResp:
		return PacketHandshake, nil
	case comet.MsgHandshakeAck:
		return PacketHandshakeAck, nil
	case comet.MsgHeartbeatReq, comet.MsgHeartbeatAck:
		return PacketHeartbeat, nil
	case comet.MsgData:
		return PacketData, nil
	case comet.MsgKick:
		return PacketKick, nil
	default:
		return 0, ErrInvalidPacketType
	}
}
