// Package telemetry — OpenTelemetry tracing + metrics for clawde-intelligence.
//
// Purpose:    Initialize OTel TracerProvider + MeterProvider with OTLP export,
//             register the 8 canonical clawde metrics, and provide PII-safe span
//             helpers. Graceful degradation: an unset/unreachable endpoint yields
//             no-op providers and never returns an error to the caller.
// Inputs:     context.Context, config.TelemetryConfig.
// Outputs:    InitTelemetry returns a shutdown func and error. Shutdown flushes
//             the batch span processor and metric reader.
// Constraints: service.name=clawde; span naming clawde.{subsystem}.{operation};
//             PII truncation to MaxAttrLen before any export.
// SPORT:      REGISTRY-FUNCTIONS.md → InitTelemetry. REGISTRY-PACKAGES.md → pkg/telemetry.
package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	apimetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// MaxAttrLen is the PII-guard truncation length for prompt/output span attrs.
const MaxAttrLen = 2000

// Batch span processor tuning (canonical per ticket).
const (
	batchMaxSize    = 512
	batchQueueSize  = 2048
	exportTimeoutMs = 30000
)

// tracerName is the instrumentation scope for all clawde spans.
const tracerName = "github.com/nself-org/clawde/intelligence"

// ShutdownFunc flushes and stops the telemetry providers.
type ShutdownFunc func(context.Context) error

var (
	mu      sync.Mutex
	metrics *Metrics
)

// Metrics holds the 8 canonical clawde OTel instruments.
type Metrics struct {
	GatewayRequests apimetric.Int64Counter
	GatewayLatency  apimetric.Float64Histogram
	GatewayCost     apimetric.Float64Counter
	GatewayTokens   apimetric.Int64Counter
	WorkerJobs      apimetric.Int64Counter
	RetrievalLatency apimetric.Float64Histogram
	EvalScore       apimetric.Float64Histogram
	Errors          apimetric.Int64Counter
}

// InitTelemetry installs the OTel TracerProvider, MeterProvider, and W3C
// propagators. When the OTLP endpoint is unset (or export setup fails) it falls
// back to no-op providers and returns a no-op shutdown with NO error — callers
// must never block on telemetry availability.
func InitTelemetry(ctx context.Context, cfg config.TelemetryConfig) (ShutdownFunc, error) {
	// Always install W3C TraceContext + Baggage propagators.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if !cfg.OTLPEnabled() {
		// Graceful degradation: no-op providers, metrics registered against the
		// default global no-op MeterProvider so call sites never nil-panic.
		registerMetrics()
		return func(context.Context) error { return nil }, nil
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		registerMetrics()
		return func(context.Context) error { return nil }, nil
	}

	spanExp, err := newTraceExporter(ctx, cfg)
	if err != nil {
		// Unreachable/misconfigured endpoint → no-op, no error to caller.
		registerMetrics()
		return func(context.Context) error { return nil }, nil
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(spanExp,
			sdktrace.WithMaxExportBatchSize(batchMaxSize),
			sdktrace.WithMaxQueueSize(batchQueueSize),
			sdktrace.WithExportTimeout(timeoutDuration()),
		),
	)
	otel.SetTracerProvider(tp)

	var mp *sdkmetric.MeterProvider
	if metricExp, mErr := newMetricExporter(ctx, cfg); mErr == nil {
		mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		)
		otel.SetMeterProvider(mp)
	}

	registerMetrics()

	return func(c context.Context) error {
		// Shutdown flushes the batch processor + metric reader. A failed flush
		// to an unreachable collector is NOT propagated — telemetry must never
		// break the host process on the way down (graceful degradation).
		_ = tp.Shutdown(c)
		if mp != nil {
			_ = mp.Shutdown(c)
		}
		return nil
	}, nil
}

// Tracer returns the clawde tracer from the global provider.
func Tracer() trace.Tracer { return otel.Tracer(tracerName) }

// SpanName builds a canonical span name: clawde.{subsystem}.{operation}.
func SpanName(subsystem, operation string) string {
	return fmt.Sprintf("clawde.%s.%s", subsystem, operation)
}

// StartSpan opens a span named clawde.{subsystem}.{operation}. Any attribute
// value is truncated to MaxAttrLen before being attached (PII guard).
func StartSpan(ctx context.Context, subsystem, operation string, attrs map[string]string) (context.Context, trace.Span) {
	ctx, span := Tracer().Start(ctx, SpanName(subsystem, operation))
	for k, v := range attrs {
		span.SetAttributes(attribute.String(k, Truncate(v)))
	}
	return ctx, span
}

// Truncate caps s at MaxAttrLen runes for PII-safe export. It operates on runes
// so multi-byte content is never split mid-character.
func Truncate(s string) string {
	r := []rune(s)
	if len(r) <= MaxAttrLen {
		return s
	}
	return string(r[:MaxAttrLen])
}

// GetMetrics returns the registered metrics instruments (never nil after Init).
func GetMetrics() *Metrics {
	mu.Lock()
	defer mu.Unlock()
	if metrics == nil {
		registerMetricsLocked()
	}
	return metrics
}

func registerMetrics() {
	mu.Lock()
	defer mu.Unlock()
	registerMetricsLocked()
}

// registerMetricsLocked builds the 8 canonical instruments against the global
// MeterProvider. Instrument creation never fails for valid names, but any error
// leaves that field nil; call sites must nil-check (helpers below do).
func registerMetricsLocked() {
	m := otel.Meter(tracerName)
	out := &Metrics{}
	out.GatewayRequests, _ = m.Int64Counter("clawde.gateway.requests_total")
	out.GatewayLatency, _ = m.Float64Histogram("clawde.gateway.latency_ms")
	out.GatewayCost, _ = m.Float64Counter("clawde.gateway.cost_usd")
	out.GatewayTokens, _ = m.Int64Counter("clawde.gateway.tokens_total")
	out.WorkerJobs, _ = m.Int64Counter("clawde.worker.jobs_total")
	out.RetrievalLatency, _ = m.Float64Histogram("clawde.retrieval.latency_ms")
	out.EvalScore, _ = m.Float64Histogram("clawde.eval.score")
	out.Errors, _ = m.Int64Counter("clawde.errors_total")
	metrics = out
}

func timeoutDuration() time.Duration { return exportTimeoutMs * time.Millisecond }

// CheckEndpoint verifies the OTLP collector is reachable by establishing the
// exporter connection. Returns nil when reachable, an error otherwise. Used by
// the `clawde telemetry health` CLI. A disabled endpoint returns an error.
func CheckEndpoint(ctx context.Context, cfg config.TelemetryConfig) error {
	if !cfg.OTLPEnabled() {
		return fmt.Errorf("OTLP endpoint not configured")
	}
	exp, err := newTraceExporter(ctx, cfg)
	if err != nil {
		return err
	}
	return exp.Shutdown(ctx)
}

func buildResource(ctx context.Context, cfg config.TelemetryConfig) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			semconv.DeploymentEnvironment(cfg.Environment),
			semconv.HostName(cfg.HostName),
		),
	)
}

func newTraceExporter(ctx context.Context, cfg config.TelemetryConfig) (sdktrace.SpanExporter, error) {
	if cfg.OTLPProtocol == "http" {
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.OTLPEndpoint)}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	}
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newMetricExporter(ctx context.Context, cfg config.TelemetryConfig) (sdkmetric.Exporter, error) {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	return otlpmetricgrpc.New(ctx, opts...)
}
