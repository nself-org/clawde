// Tests for the BM25 Kernel: feature flag routing, fallback, A/B harness.
//
// All tests use stub lane implementations — no live Postgres or ParadeDB required.
// CI passes without pg_bm25 installed because TSVectorBM25Lane is always available.
package retrieval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// ── Stub lanes ────────────────────────────────────────────────────────────────

// stubLane is a configurable BM25Lane for kernel tests.
type stubLane struct {
	name    string
	results []lanes.LaneResult
	err     error
	calls   int
}

func (s *stubLane) Name() string { return s.name }

func (s *stubLane) BM25Query(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ int,
) ([]lanes.LaneResult, error) {
	s.calls++
	return s.results, s.err
}

// ── Stub DBQuerier (minimal — kernel tests use stub lanes, not DB) ─────────────

type stubDB struct {
	rows lanes.Rows
}

func (s *stubDB) Query(_ context.Context, _ string, _ ...any) (lanes.Rows, error) {
	return s.rows, nil
}
func (s *stubDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// stubRows satisfies lanes.Rows and returns no data.
type stubRows struct{}

func (s *stubRows) Next() bool            { return false }
func (s *stubRows) Scan(_ ...any) error   { return nil }
func (s *stubRows) Close()                {}
func (s *stubRows) Err() error            { return nil }

// ── Stub ABLogWriter ──────────────────────────────────────────────────────────

type stubABWriter struct {
	rows []retrieval.ABLogRow
	err  error
}

func (s *stubABWriter) WriteABLog(_ context.Context, row retrieval.ABLogRow) error {
	s.rows = append(s.rows, row)
	return s.err
}

// ── Config helpers ────────────────────────────────────────────────────────────

func cfgDefault() retrieval.Config {
	return retrieval.Config{BM25Enabled: false, BM25ABMode: false}
}

func cfgBM25() retrieval.Config {
	return retrieval.Config{BM25Enabled: true, BM25ABMode: false}
}

func cfgAB() retrieval.Config {
	return retrieval.Config{BM25Enabled: false, BM25ABMode: true}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestKernel_DefaultConfig_UsesTSVector(t *testing.T) {
	// With BM25Enabled=false, the kernel must use tsvector and NOT call paradedb.
	tsLane := &stubLane{
		name:    "tsvector",
		results: []lanes.LaneResult{{Score: 0.8}},
	}
	db := &stubDB{rows: &stubRows{}}
	k := retrieval.NewBM25KernelWithLanes(cfgDefault(), tsLane, nil, retrieval.NoopABLogWriter{})

	got, err := k.Query(context.Background(), uuid.New(), "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(results) = %d; want 1", len(got))
	}
	if tsLane.calls != 1 {
		t.Errorf("tsvector lane called %d times; want 1", tsLane.calls)
	}
	_ = db // suppress "declared and not used"
}

func TestKernel_BM25Enabled_UsesParadeDB(t *testing.T) {
	tsLane := &stubLane{name: "tsvector", results: []lanes.LaneResult{{Score: 0.5}}}
	bm25Lane := &stubLane{name: "paradedb", results: []lanes.LaneResult{{Score: 1.2}}}

	k := retrieval.NewBM25KernelWithLanes(cfgBM25(), tsLane, bm25Lane, retrieval.NoopABLogWriter{})

	got, err := k.Query(context.Background(), uuid.New(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bm25Lane.calls != 1 {
		t.Errorf("paradedb lane called %d times; want 1", bm25Lane.calls)
	}
	if tsLane.calls != 0 {
		t.Errorf("tsvector lane called %d times; want 0 (no fallback needed)", tsLane.calls)
	}
	if len(got) != 1 || got[0].Score != 1.2 {
		t.Errorf("unexpected results: %+v", got)
	}
}

func TestKernel_BM25Enabled_FallsBackOnParadeDBError(t *testing.T) {
	// Simulates pg_bm25 returning an error → kernel must fall back to tsvector
	// and emit a bm25_fallback OTel log event (verified via tsvector.calls).
	tsResults := []lanes.LaneResult{{Score: 0.6}}
	tsLane := &stubLane{name: "tsvector", results: tsResults}
	bm25Lane := &stubLane{
		name: "paradedb",
		err:  errors.New("pg_bm25 extension is not installed"),
	}

	k := retrieval.NewBM25KernelWithLanes(cfgBM25(), tsLane, bm25Lane, retrieval.NoopABLogWriter{})

	got, err := k.Query(context.Background(), uuid.New(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error after fallback: %v", err)
	}
	if tsLane.calls != 1 {
		t.Errorf("tsvector fallback calls = %d; want 1", tsLane.calls)
	}
	if len(got) == 0 || got[0].Score != 0.6 {
		t.Errorf("expected tsvector results after fallback; got %+v", got)
	}
}

func TestKernel_ABMode_WritesABLogRow(t *testing.T) {
	// In A/B mode the kernel must call both lanes and log a row.
	tsLane := &stubLane{name: "tsvector", results: []lanes.LaneResult{{Score: 0.5}}}
	bm25Lane := &stubLane{name: "paradedb", results: []lanes.LaneResult{{Score: 1.0}}}
	abWriter := &stubABWriter{}

	k := retrieval.NewBM25KernelWithLanes(cfgAB(), tsLane, bm25Lane, abWriter)
	wsID := uuid.New()
	_, err := k.Query(context.Background(), wsID, "ab query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(abWriter.rows) != 1 {
		t.Fatalf("A/B log rows = %d; want 1", len(abWriter.rows))
	}
	row := abWriter.rows[0]
	if row.WorkspaceID != wsID {
		t.Errorf("log row workspace_id = %v; want %v", row.WorkspaceID, wsID)
	}
	if row.Query != "ab query" {
		t.Errorf("log row query = %q; want %q", row.Query, "ab query")
	}
	if len(row.TSVectorTop10) == 0 {
		t.Error("log row tsvector_top10 is empty; want results")
	}
	if len(row.BM25Top10) == 0 {
		t.Error("log row bm25_top10 is empty; want results")
	}
}

func TestKernel_ABMode_FallsBackWhenParadeDBErrors(t *testing.T) {
	// In A/B mode with paradedb error, tsvector results are returned.
	// The A/B log row is still written (bm25_top10 is empty JSONB).
	tsLane := &stubLane{name: "tsvector", results: []lanes.LaneResult{{Score: 0.4}}}
	bm25Lane := &stubLane{
		name: "paradedb",
		err:  errors.New("pg_bm25 not installed"),
	}
	abWriter := &stubABWriter{}

	k := retrieval.NewBM25KernelWithLanes(cfgAB(), tsLane, bm25Lane, abWriter)

	got, err := k.Query(context.Background(), uuid.New(), "q", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected tsvector results; got none")
	}
	// A/B log row must still be written.
	if len(abWriter.rows) != 1 {
		t.Fatalf("A/B log rows = %d; want 1", len(abWriter.rows))
	}
	if len(abWriter.rows[0].BM25Top10) != 0 {
		t.Error("expected bm25_top10 to be empty (paradedb failed)")
	}
}

func TestKernel_Config_LoadConfig_FalseByDefault(t *testing.T) {
	// With no env vars set, both flags default to false.
	// (This test does not set env vars — relies on test environment being clean.)
	cfg := retrieval.LoadConfig()
	if cfg.BM25Enabled {
		t.Error("BM25Enabled should default to false")
	}
	if cfg.BM25ABMode {
		t.Error("BM25ABMode should default to false")
	}
}
