package observability

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"starve/internal/actor"
	"starve/internal/game/world"
	"starve/internal/gateway"
)

// MetricsConfig 只接收稳定业务抽象和低基数配置。
type MetricsConfig struct {
	TickBudget time.Duration
	Mailboxes  func() []actor.MailboxSnapshot
}

// Metrics 把业务观测接口适配到 OTel Metrics，并由 Prometheus 拉取。
type Metrics struct {
	provider *sdkmetric.MeterProvider
	handler  http.Handler

	tickBudget time.Duration
	mailboxes  func() []actor.MailboxSnapshot

	tickDuration      otelmetric.Float64Histogram
	tickOverBudget    otelmetric.Int64Counter
	deltaSnapshotSize otelmetric.Int64Histogram
	fullSnapshotSize  otelmetric.Int64Histogram
	saveDuration      otelmetric.Float64Histogram
	saveSize          otelmetric.Int64Histogram
	saveErrors        otelmetric.Int64Counter
	rejects           otelmetric.Int64Counter

	rawConnections atomic.Int64
	sessions       atomic.Int64
}

func NewMetrics(cfg MetricsConfig) (*Metrics, error) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("observability: create prometheus exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(
			histogramView("starve.tick.duration", []float64{
				0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005,
				0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1,
			}),
			histogramView("starve.save.duration", []float64{
				0.001, 0.0025, 0.005, 0.01, 0.025, 0.05,
				0.1, 0.25, 0.5, 1, 2.5, 5,
			}),
			histogramView("starve.snapshot.*.bytes", []float64{
				128, 256, 512, 1024, 2048, 4096, 8192,
				16384, 32768, 65536, 131072, 262144, 524288, 1048576,
			}),
			histogramView("starve.save.bytes", []float64{
				128, 256, 512, 1024, 2048, 4096, 8192,
				16384, 32768, 65536, 131072, 262144, 524288, 1048576,
			}),
		),
	)
	meter := provider.Meter("starve/server")

	m := &Metrics{
		provider:   provider,
		handler:    promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		tickBudget: cfg.TickBudget,
		mailboxes:  cfg.Mailboxes,
	}
	if err := m.createInstruments(meter); err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, err
	}
	return m, nil
}

func histogramView(name string, boundaries []float64) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
			Boundaries: boundaries,
			NoMinMax:   true,
		}},
	)
}

func (m *Metrics) createInstruments(meter otelmetric.Meter) error {
	var err error
	if m.tickDuration, err = meter.Float64Histogram(
		"starve.tick.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("World tick execution duration."),
	); err != nil {
		return err
	}
	if m.tickOverBudget, err = meter.Int64Counter(
		"starve.tick.overbudget",
		otelmetric.WithDescription("Number of world ticks exceeding their configured budget."),
	); err != nil {
		return err
	}
	if m.deltaSnapshotSize, err = meter.Int64Histogram(
		"starve.snapshot.delta.bytes",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Encoded delta snapshot size."),
	); err != nil {
		return err
	}
	if m.fullSnapshotSize, err = meter.Int64Histogram(
		"starve.snapshot.full.bytes",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Encoded full snapshot size."),
	); err != nil {
		return err
	}
	if m.saveDuration, err = meter.Float64Histogram(
		"starve.save.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("World save serialization or event persistence duration."),
	); err != nil {
		return err
	}
	if m.saveSize, err = meter.Int64Histogram(
		"starve.save.bytes",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Encoded world save size."),
	); err != nil {
		return err
	}
	if m.saveErrors, err = meter.Int64Counter(
		"starve.save.errors",
		otelmetric.WithDescription("Number of world save failures."),
	); err != nil {
		return err
	}
	if m.rejects, err = meter.Int64Counter(
		"starve.gateway.rejects",
		otelmetric.WithDescription("Number of requests rejected at the gateway boundary."),
	); err != nil {
		return err
	}
	rawGauge, err := meter.Int64ObservableGauge(
		"starve.gateway.raw_connections",
		otelmetric.WithDescription("Current raw WebSocket connections."),
	)
	if err != nil {
		return err
	}
	sessionGauge, err := meter.Int64ObservableGauge(
		"starve.gateway.sessions",
		otelmetric.WithDescription("Current authenticated gateway sessions."),
	)
	if err != nil {
		return err
	}
	depthGauge, err := meter.Int64ObservableGauge(
		"starve.actor.mailbox.depth",
		otelmetric.WithDescription("Approximate queued actor messages aggregated by actor kind."),
	)
	if err != nil {
		return err
	}
	capacityGauge, err := meter.Int64ObservableGauge(
		"starve.actor.mailbox.capacity",
		otelmetric.WithDescription("Actor mailbox capacity aggregated by actor kind."),
	)
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(rawGauge, m.rawConnections.Load())
		observer.ObserveInt64(sessionGauge, m.sessions.Load())
		if m.mailboxes == nil {
			return nil
		}
		type totals struct{ depth, capacity int64 }
		byKind := make(map[string]totals)
		for _, snapshot := range m.mailboxes() {
			total := byKind[snapshot.Kind]
			total.depth += int64(snapshot.Depth)
			total.capacity += int64(snapshot.Capacity)
			byKind[snapshot.Kind] = total
		}
		for kind, total := range byKind {
			options := otelmetric.WithAttributes(attribute.String("actor_kind", kind))
			observer.ObserveInt64(depthGauge, total.depth, options)
			observer.ObserveInt64(capacityGauge, total.capacity, options)
		}
		return nil
	}, rawGauge, sessionGauge, depthGauge, capacityGauge)
	return err
}

func (m *Metrics) Handler() http.Handler { return m.handler }

func (m *Metrics) Shutdown(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}

func (m *Metrics) ObserveTick(stats world.TickStats) {
	ctx := context.Background()
	m.tickDuration.Record(ctx, stats.Duration.Seconds())
	m.deltaSnapshotSize.Record(ctx, int64(stats.DeltaSnapshotBytes))
	if m.tickBudget > 0 && stats.Duration > m.tickBudget {
		m.tickOverBudget.Add(ctx, 1)
	}
}

func (m *Metrics) ObserveSave(stats world.SaveStats) {
	ctx := context.Background()
	triggerValue := string(stats.Trigger)
	switch stats.Trigger {
	case world.SaveTriggerManual, world.SaveTriggerEvent, world.SaveTriggerShutdown:
	default:
		triggerValue = "unknown"
	}
	trigger := attribute.String("trigger", triggerValue)
	options := otelmetric.WithAttributes(trigger)
	m.saveDuration.Record(ctx, stats.Duration.Seconds(), options)
	m.saveSize.Record(ctx, int64(stats.Bytes), options)
	if stats.Err != nil {
		m.saveErrors.Add(ctx, 1, options)
	}
}

func (m *Metrics) ObserveGateway(stats gateway.GatewayStats) {
	m.rawConnections.Store(int64(stats.RawConnections))
	m.sessions.Store(int64(stats.Sessions))
	ctx := context.Background()
	if stats.RejectReason != "" {
		reason := string(stats.RejectReason)
		switch stats.RejectReason {
		case gateway.RejectBadMessage,
			gateway.RejectUnknownRoute,
			gateway.RejectUnauthenticated,
			gateway.RejectBadToken,
			gateway.RejectWorldUnavailable,
			gateway.RejectStaleInput:
		default:
			reason = "unknown"
		}
		m.rejects.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("reason", reason),
		))
	}
	if stats.FullSnapshotBytes > 0 {
		m.fullSnapshotSize.Record(ctx, int64(stats.FullSnapshotBytes))
	}
}
