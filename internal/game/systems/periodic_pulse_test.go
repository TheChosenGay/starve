package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

func TestPeriodicPulseDueIsTickRateIndependent(t *testing.T) {
	tests := []struct {
		name  string
		dt    time.Duration
		phase int
		due   bool
	}{
		{name: "20Hz before one second", dt: 50 * time.Millisecond, phase: 19, due: false},
		{name: "20Hz at one second", dt: 50 * time.Millisecond, phase: 20, due: true},
		{name: "40Hz before one second", dt: 25 * time.Millisecond, phase: 39, due: false},
		{name: "40Hz at one second", dt: 25 * time.Millisecond, phase: 40, due: true},
		{name: "10Hz at one second", dt: 100 * time.Millisecond, phase: 10, due: true},
		{name: "zero dt uses 20Hz fallback", dt: 0, phase: 20, due: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := ecs.NewWorld()
			w.AddResource(&components.DayCycle{Phase: tt.phase})
			if got := periodicPulseDue(w, tt.dt, time.Second); got != tt.due {
				t.Fatalf("periodicPulseDue(dt=%s, phase=%d)=%v want %v", tt.dt, tt.phase, got, tt.due)
			}
		})
	}
}
