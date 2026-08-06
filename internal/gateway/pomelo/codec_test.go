package pomelo

import (
	"bytes"
	"errors"
	"testing"

	"github.com/TheChosenGay/combet"
)

// ---- packet 层 ----

func TestPacketRoundTrip(t *testing.T) {
	cases := []struct {
		typ  byte
		data []byte
	}{
		{PacketHandshake, []byte(`{"code":200}`)},
		{PacketHandshakeAck, nil},
		{PacketHeartbeat, nil},
		{PacketData, []byte{0x00, 0x01, 0x02}},
		{PacketKick, []byte("kick")},
	}
	for _, c := range cases {
		wire, err := EncodePacket(c.typ, c.data)
		if err != nil {
			t.Fatalf("encode %d: %v", c.typ, err)
		}
		if len(wire) != PacketHeaderSize+len(c.data) {
			t.Fatalf("wire len = %d", len(wire))
		}
		packets, err := DecodePackets(wire)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(packets) != 1 || packets[0].Type != c.typ || !bytes.Equal(packets[0].Data, c.data) {
			t.Fatalf("packet = %+v", packets)
		}
	}
}

func TestPacketMultiInOneFrame(t *testing.T) {
	var frame []byte
	for i := 0; i < 3; i++ {
		wire, _ := EncodePacket(PacketData, []byte{byte(i)})
		frame = append(frame, wire...)
	}
	packets, err := DecodePackets(frame)
	if err != nil || len(packets) != 3 {
		t.Fatalf("packets = %v, err = %v", packets, err)
	}
}

func TestPacketErrors(t *testing.T) {
	if _, err := EncodePacket(0x09, nil); !errors.Is(err, ErrInvalidPacketType) {
		t.Fatalf("err = %v", err)
	}
	if _, err := DecodePackets([]byte{0x04, 0x00, 0x00}); !errors.Is(err, ErrPacketTruncated) {
		t.Fatalf("err = %v", err)
	}
	if _, err := DecodePackets([]byte{0x00, 0x00, 0x00, 0x00}); !errors.Is(err, ErrInvalidPacketType) {
		t.Fatalf("err = %v", err)
	}
}

// ---- message 层 ----

func TestMessageRoundTrip(t *testing.T) {
	cases := []*Message{
		{Type: MsgRequest, ID: 1, Route: "gate.login", Data: []byte{0x0a, 0x03, 'a', 'b', 'c'}},
		{Type: MsgNotify, Route: "world.player.move", Data: []byte{0x08, 0x01}},
		{Type: MsgResponse, ID: 42, Data: []byte{0x08, 0x01}},
		{Type: MsgPush, Route: "world.player.move", Data: []byte{0x08, 0x01, 0x10, 0x05}},
		{Type: MsgRequest, ID: 0x1234567890, Route: "a", Data: nil},
	}
	for _, m := range cases {
		wire, err := EncodeMessage(m)
		if err != nil {
			t.Fatalf("encode %+v: %v", m, err)
		}
		got, err := DecodeMessage(wire)
		if err != nil {
			t.Fatalf("decode %+v: %v", m, err)
		}
		if got.Type != m.Type || got.ID != m.ID || got.Route != m.Route || !bytes.Equal(got.Data, m.Data) {
			t.Fatalf("roundtrip: want %+v, got %+v", m, got)
		}
	}
}

func TestMessageVarint(t *testing.T) {
	for _, id := range []uint64{0, 1, 127, 128, 300, 1 << 31, 1 << 40} {
		wire, err := EncodeMessage(&Message{Type: MsgRequest, ID: id, Route: "r"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := DecodeMessage(wire)
		if err != nil || m.ID != id {
			t.Fatalf("id %d: got %d, err %v", id, m.ID, err)
		}
	}
}

func TestMessageCompressedRouteUnsupported(t *testing.T) {
	// flag = request(0x00<<1) | routeCompress(0x01) = 0x01
	_, err := DecodeMessage([]byte{0x01, 0x00, 0x00, 0x01, 0x00})
	if !errors.Is(err, ErrRouteCompressed) {
		t.Fatalf("err = %v", err)
	}
}

// ---- Scheme（comet.MsgScheme 实现）----

func TestSchemeRoundTrip(t *testing.T) {
	s := NewScheme()
	cases := []*comet.Msg{
		{Type: comet.MsgHandshakeReq, Payload: []byte(`{"version":"1"}`)},
		{Type: comet.MsgHandshakeAck, Payload: nil},
		{Type: comet.MsgHeartbeatReq, Payload: nil},
		{Type: comet.MsgData, Payload: []byte{0x01}},
		{Type: comet.MsgKick, Payload: []byte("bye")},
	}
	for _, m := range cases {
		wire, err := s.Encode(m)
		if err != nil {
			t.Fatalf("encode %+v: %v", m, err)
		}
		got, err := s.Decode(wire)
		if err != nil {
			t.Fatalf("decode %+v: %v", m, err)
		}
		if got.Type != m.Type || !bytes.Equal(got.Payload, m.Payload) {
			t.Fatalf("scheme roundtrip: want %+v, got %+v", m, got)
		}
	}
}
