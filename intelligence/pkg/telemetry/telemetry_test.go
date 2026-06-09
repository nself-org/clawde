// Package telemetry — unit tests for the observability pipeline.
//
// Covers: InitTelemetry shutdown flush, no-op on unset endpoint, PII truncation,
// span naming, cost computation/aggregation, 8 metric registration, Langfuse
// disabled no-op. Jaeger/collector-dependent assertions use an in-memory span
// recorder (tracetest) — no external services required.
package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installRecorder swaps in an in-memory span recorder so span assertions need no
// collector. Returns the recorder; restores nothing (tests reset per call).
func installRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return sr
}

func TestInitTelemetry_NoEndpoint_NoError(t *testing.T) {
	cfg := config.TelemetryConfig{} // no OTLP endpoint
	shutdown, err := InitTelemetry(context.Background(), cfg)
	require.NoError(t, err, "unset endpoint must not error (graceful degradation)")
	require.NotNil(t, shutdown)
	assert.NoError(t, shutdown(context.Background()), "no-op shutdown must succeed")
}

func TestInitTelemetry_UnreachableEndpoint_NoError(t *testing.T) {
	cfg := config.TelemetryConfig{
		OTLPEndpoint: "127.0.0.1:1", // nothing listening
		OTLPProtocol: "grpc",
		OTLPInsecure: true,
		ServiceName:  "clawde",
	}
	shutdown, err := InitTelemetry(context.Background(), cfg)
	require.NoError(t, err, "unreachable endpoint degrades to no-op, never errors caller")
	require.NotNil(t, shutdown)
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.NoError(t, shutdown(sctx), "shutdown swallows flush errors to unreachable collector")
}

func TestStartSpan_NamingAndPIITruncation(t *testing.T) {
	sr := installRecorder(t)
	longInput := strings.Repeat("x", MaxAttrLen+500)

	_, span := StartSpan(context.Background(), "gateway", "complete", map[string]string{
		"prompt": longInput,
	})
	span.End()

	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "clawde.gateway.complete", spans[0].Name(), "canonical span name")

	var found bool
	for _, kv := range spans[0].Attributes() {
		if string(kv.Key) == "prompt" {
			found = true
			assert.Len(t, []rune(kv.Value.AsString()), MaxAttrLen, "PII attr truncated to MaxAttrLen")
		}
	}
	assert.True(t, found, "prompt attribute present")
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", Truncate("abc"))
	long := strings.Repeat("é", MaxAttrLen+10) // multi-byte runes
	out := Truncate(long)
	assert.Len(t, []rune(out), MaxAttrLen, "rune-safe truncation, no mid-char split")
}

func TestSpanName(t *testing.T) {
	assert.Equal(t, "clawde.worker.process_job", SpanName("worker", "process_job"))
}

func TestRegisterMetrics_All8(t *testing.T) {
	registerMetrics()
	m := GetMetrics()
	require.NotNil(t, m)
	assert.NotNil(t, m.GatewayRequests)
	assert.NotNil(t, m.GatewayLatency)
	assert.NotNil(t, m.GatewayCost)
	assert.NotNil(t, m.GatewayTokens)
	assert.NotNil(t, m.WorkerJobs)
	assert.NotNil(t, m.RetrievalLatency)
	assert.NotNil(t, m.EvalScore)
	assert.NotNil(t, m.Errors)
}

func TestComputeCost(t *testing.T) {
	rates := map[string]config.CostRate{
		"anthropic/claude": {InputPer1k: 3.0, OutputPer1k: 15.0},
	}
	// 2000 in @ $3/1k = $6 ; 1000 out @ $15/1k = $15 ; total $21.
	got := ComputeCost(rates, "anthropic", "claude", 2000, 1000)
	assert.InDelta(t, 21.0, got, 1e-9)

	// Unknown model → zero rate → zero cost (graceful degradation).
	assert.Equal(t, 0.0, ComputeCost(rates, "openai", "gpt", 5000, 5000))
}

func TestRecordCost_NilConn_MetricsOnly(t *testing.T) {
	registerMetrics()
	rates := map[string]config.CostRate{"p/m": {InputPer1k: 1, OutputPer1k: 2}}
	cost, err := RecordCost(context.Background(), nil, rates, CostEvent{
		Provider: "p", Model: "m", Lane: "chat", TokensIn: 1000, TokensOut: 1000,
	})
	require.NoError(t, err, "nil conn must not error")
	assert.InDelta(t, 3.0, cost, 1e-9)
}

func TestCostSummary_NilConn(t *testing.T) {
	rows, err := CostSummary(context.Background(), nil, "")
	require.NoError(t, err)
	assert.Nil(t, rows)
}

func TestLangfuse_DisabledNoOp(t *testing.T) {
	c := NewLangfuseClient(config.TelemetryConfig{}) // no keys
	assert.False(t, c.Enabled())
	require.NoError(t, c.TraceLLMCall(context.Background(), LLMTrace{Lane: "chat"}))
	require.NoError(t, c.ScoreTrace(context.Background(), RagasScore{Name: "faithfulness", Value: 0.9}))
}

func TestLoadTelemetryConfig_Defaults(t *testing.T) {
	t.Setenv("CLAWDE_OTEL_ENDPOINT", "")
	t.Setenv("CLAWDE_COST_RATES_JSON", `{"a/b":{"input_per_1k":1.5,"output_per_1k":4.0}}`)
	cfg := config.LoadTelemetryConfig()
	assert.Equal(t, "clawde", cfg.ServiceName)
	assert.Equal(t, "grpc", cfg.OTLPProtocol)
	assert.False(t, cfg.OTLPEnabled())
	assert.Equal(t, 1.5, cfg.CostRates["a/b"].InputPer1k)
	assert.False(t, cfg.LangfuseEnabled)
}
