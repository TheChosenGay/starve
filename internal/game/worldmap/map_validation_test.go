package worldmap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMapSpecRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "spawn outside map",
			body: `{"width":8,"height":8,"spawn_x":8,"spawn_y":2}`,
			want: "spawn",
		},
		{
			name: "unknown workstation",
			body: `{"width":8,"height":8,"spawn_x":2,"spawn_y":2,
				"handplaced":{"stations":[{"type":"magic_table","x":3,"y":3}]}}`,
			want: "unknown type",
		},
		{
			name: "resource outside map",
			body: `{"width":8,"height":8,"spawn_x":2,"spawn_y":2,
				"handplaced":{"resources":[{"kind":"wood","action":"chop","work":2,"x":-1,"y":3}]}}`,
			want: "outside map",
		},
		{
			name: "invalid scatter",
			body: `{"width":8,"height":8,"spawn_x":2,"spawn_y":2,
				"scatter":[{"kind":"wood","action":"chop","work":0,"count":1,"min_dist":1}]}`,
			want: "work must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "map.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadMapSpec(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadMapSpec error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadMapSpecDefaultsRevivalStatue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map.json")
	body := `{
		"width": 8, "height": 8, "spawn_x": 2, "spawn_y": 2,
		"handplaced": {"revival_statues": [{"x": 3, "y": 2}]}
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := LoadMapSpec(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Handplaced.RevivalStatues) != 1 {
		t.Fatalf("revival statues=%d, want 1", len(spec.Handplaced.RevivalStatues))
	}
	statue := spec.Handplaced.RevivalStatues[0]
	if statue.Uses != 1 || statue.DurationTicks != 60 {
		t.Fatalf("revival statue defaults=%+v, want uses=1 duration=60", statue)
	}
}
