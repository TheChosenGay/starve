package systems

import (
	"fmt"
	"os"
	"testing"
)

// 确认缺省走 OrcaAOI，且可用环境变量退回 BVH。
func TestDefaultBackend(t *testing.T) {
	os.Unsetenv("GATE_NEIGHBOR_BACKEND")
	s := NewDefaultMoveSolver()
	fmt.Printf("缺省: AOI=%v\n", s.AOI != nil)
	if s.AOI == nil {
		t.Fatal("缺省应启用 OrcaAOI")
	}
	os.Setenv("GATE_NEIGHBOR_BACKEND", "bvh")
	defer os.Unsetenv("GATE_NEIGHBOR_BACKEND")
	s2 := NewDefaultMoveSolver()
	fmt.Printf("GATE_NEIGHBOR_BACKEND=bvh: AOI=%v\n", s2.AOI != nil)
	if s2.AOI != nil {
		t.Fatal("显式设 bvh 时应退回 BVH")
	}
}
