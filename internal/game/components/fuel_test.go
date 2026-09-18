package components

import "testing"

// Fuel 的编解码是**手写定长二进制**（协议层本次冻结，见 fuel.go），
// 没有 protobuf 的字段号兜底：字段顺序/宽度写错不会报错，只会静默读出垃圾，
// 所以这里钉住一次往返 + 载荷长度。
func TestFuelCodecRoundTrip(t *testing.T) {
	c := fuelCodec{}
	in := Fuel{Cur: 7, Max: 4800, HeatStrength: 10, HeatRadius: 3}
	b, err := c.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != fuelCodecSize {
		t.Fatalf("载荷 %d 字节, want %d", len(b), fuelCodecSize)
	}
	out, err := c.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("往返后 = %+v, want %+v", out, in)
	}
}

// 截断载荷必须报错，而不是把半截数据当成合法燃料（否则存档损坏 = 火堆凭空复燃）。
func TestFuelCodecRejectsShortPayload(t *testing.T) {
	if _, err := (fuelCodec{}).Decode(make([]byte, fuelCodecSize-1)); err == nil {
		t.Fatal("短载荷应报错")
	}
}
