package observability

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"starve/internal/actor"
	"starve/internal/game/world"
	"starve/internal/gateway"
)

func TestMetricsExposeBoundedServerSignals(t *testing.T) {
	metrics, err := NewMetrics(MetricsConfig{
		TickBudget: 10 * time.Millisecond,
		Mailboxes: func() []actor.MailboxSnapshot {
			return []actor.MailboxSnapshot{{Kind: "world", Depth: 2, Capacity: 16}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer metrics.Shutdown(t.Context())

	metrics.ObserveTick(world.TickStats{
		Duration:           20 * time.Millisecond,
		DeltaSnapshotBytes: 64,
	})
	metrics.ObserveSave(world.SaveStats{
		Duration: 5 * time.Millisecond,
		Bytes:    128,
		Trigger:  world.SaveTriggerEvent,
		Err:      errors.New("disk full"),
	})
	metrics.ObserveGateway(gateway.GatewayStats{
		RawConnections:    3,
		Sessions:          2,
		RejectReason:      gateway.RejectBadToken,
		FullSnapshotBytes: 256,
	})

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	body := response.Body.String()

	for _, want := range []string{
		"# HELP starve_tick_duration_seconds World tick execution duration.",
		"starve_tick_duration",
		`starve_tick_duration_seconds_bucket{otel_scope_name="starve/server",otel_scope_schema_url="",otel_scope_version="",le="0.05"}`,
		"starve_tick_overbudget",
		"starve_actor_mailbox_depth",
		`actor_kind="world"`,
		"starve_gateway_raw_connections",
		"starve_gateway_sessions",
		`reason="bad_token"`,
		"starve_snapshot_delta",
		`starve_snapshot_delta_bytes_bucket{otel_scope_name="starve/server",otel_scope_schema_url="",otel_scope_version="",le="128"}`,
		"starve_snapshot_full",
		`starve_snapshot_full_bytes_bucket{otel_scope_name="starve/server",otel_scope_schema_url="",otel_scope_version="",le="128"}`,
		`trigger="event"`,
		`starve_save_bytes_bucket{otel_scope_name="starve/server",otel_scope_schema_url="",otel_scope_version="",trigger="event",le="128"}`,
		"starve_save_errors",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"uid=", "entity=", "conn_id="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics output contains forbidden label %q", forbidden)
		}
	}
}
