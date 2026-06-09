// Tests for BM25 lane implementations.
//
// These tests use stub DBQuerier implementations — no live Postgres required.
// Tests that would require a real pg_bm25 extension are documented with
// skip-with-reason comments so CI passes without ParadeDB installed.
package lanes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// ── Stub helpers ──────────────────────────────────────────────────────────────

// stubRows implements lanes.Rows over a pre-loaded slice of LaneResult.
type stubRows struct {
	data    []lanes.LaneResult
	pos     int
	scanErr error
	rowsErr error
}

func (r *stubRows) Next() bool         { r.pos++; return r.pos <= len(r.data) }
func (r *stubRows) Err() error         { return r.rowsErr }
func (r *stubRows) Close()             {}
func (r *stubRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	item := r.data[r.pos-1]
	if len(dest) >= 3 {
		*dest[0].(*uuid.UUID) = item.ChunkID
		*dest[1].(*float64) = item.Score
		*dest[2].(*string) = item.Content
	}
	return nil
}

// execLog tracks Exec calls for GUC assertion.
type execLog struct {
	calls []string
	err   error
}

// stubDB satisfies lanes.DBQuerier.
type stubDB struct {
	rows    lanes.Rows
	queryFn func(ctx context.Context, sql string, args ...any) (lanes.Rows, error)
	execFn  func(ctx context.Context, sql string, args ...any) error
}

func (s *stubDB) Query(ctx context.Context, sql string, args ...any) (lanes.Rows, error) {
	if s.queryFn != nil {
		return s.queryFn(ctx, sql, args...)
	}
	return s.rows, nil
}

func (s *stubDB) Exec(ctx context.Context, sql string, args ...any) error {
	if s.execFn != nil {
		return s.execFn(ctx, sql, args...)
	}
	return nil
}

// ── TSVectorBM25Lane tests ────────────────────────────────────────────────────

func TestTSVectorLane_Name(t *testing.T) {
	lane := lanes.NewTSVectorBM25Lane(&stubDB{rows: &stubRows{}})
	if got := lane.Name(); got != "tsvector" {
		t.Errorf("Name() = %q; want %q", got, "tsvector")
	}
}

func TestTSVectorLane_BM25Query_ReturnsResults(t *testing.T) {
	// Stub returns two chunks with known scores.
	wsID := uuid.New()
	expected := []lanes.LaneResult{
		{ChunkID: uuid.New(), Score: 0.9, Content: "hello world"},
		{ChunkID: uuid.New(), Score: 0.7, Content: "foo bar"},
	}

	db := &stubDB{
		rows: &stubRows{data: expected},
	}
	lane := lanes.NewTSVectorBM25Lane(db)

	got, err := lane.BM25Query(context.Background(), wsID, "hello", 10)
	if err != nil {
		t.Fatalf("BM25Query() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(results) = %d; want 2", len(got))
	}
	if got[0].Score != expected[0].Score {
		t.Errorf("results[0].Score = %v; want %v", got[0].Score, expected[0].Score)
	}
}

func TestTSVectorLane_BM25Query_SetsGUC(t *testing.T) {
	wsID := uuid.New()
	var execSQL string

	db := &stubDB{
		rows: &stubRows{},
		execFn: func(_ context.Context, sql string, args ...any) error {
			execSQL = sql
			return nil
		},
	}
	lane := lanes.NewTSVectorBM25Lane(db)
	_, _ = lane.BM25Query(context.Background(), wsID, "test", 5)

	if execSQL == "" {
		t.Error("expected SET LOCAL app.workspace_id to be executed; got nothing")
	}
}

func TestTSVectorLane_BM25Query_ExecError(t *testing.T) {
	db := &stubDB{
		execFn: func(_ context.Context, _ string, _ ...any) error {
			return errors.New("permission denied")
		},
	}
	lane := lanes.NewTSVectorBM25Lane(db)
	_, err := lane.BM25Query(context.Background(), uuid.New(), "q", 5)
	if err == nil {
		t.Fatal("expected error when GUC SET fails; got nil")
	}
}

func TestTSVectorLane_BM25Query_EmptyResults(t *testing.T) {
	db := &stubDB{rows: &stubRows{}}
	lane := lanes.NewTSVectorBM25Lane(db)

	got, err := lane.BM25Query(context.Background(), uuid.New(), "no match", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 results; got %d", len(got))
	}
}

// ── ParadeDBBM25Lane tests ────────────────────────────────────────────────────

func TestParadeDBLane_Name(t *testing.T) {
	lane := lanes.NewParadeDBBM25Lane(&stubDB{rows: &stubRows{}})
	if got := lane.Name(); got != "paradedb" {
		t.Errorf("Name() = %q; want %q", got, "paradedb")
	}
}

func TestParadeDBLane_ReturnsDescriptiveErrorWhenExtensionAbsent(t *testing.T) {
	// The extension check query returns zero rows → pg_bm25 is absent.
	// The lane must return a descriptive, non-panicking error.
	db := &stubDB{
		rows: &stubRows{data: nil}, // no rows = pg_bm25 not installed
	}
	lane := lanes.NewParadeDBBM25Lane(db)

	_, err := lane.BM25Query(context.Background(), uuid.New(), "query", 5)
	if err == nil {
		t.Fatal("expected error when pg_bm25 absent; got nil")
	}
	// Error must be descriptive — not just "extension not found".
	if !containsAny(err.Error(), "pg_bm25", "not installed", "paradedb") {
		t.Errorf("error not descriptive enough: %v", err)
	}
}

func TestParadeDBLane_DoesNotPanicWhenExtensionAbsent(t *testing.T) {
	// Belt-and-suspenders: use a recover to confirm no panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BM25Query panicked: %v", r)
		}
	}()
	db := &stubDB{rows: &stubRows{data: nil}}
	lane := lanes.NewParadeDBBM25Lane(db)
	_, _ = lane.BM25Query(context.Background(), uuid.New(), "q", 5)
}

func TestParadeDBLane_ReturnsResultsWhenExtensionPresent(t *testing.T) {
	// Simulate pg_bm25 present: first query (extension check) returns one row,
	// second query (BM25 search) returns two results.
	wsID := uuid.New()
	expected := []lanes.LaneResult{
		{ChunkID: uuid.New(), Score: 1.2, Content: "bm25 result"},
	}

	callCount := 0
	db := &stubDB{
		queryFn: func(_ context.Context, sql string, args ...any) (lanes.Rows, error) {
			callCount++
			if callCount == 1 {
				// Extension check — return one row (present).
				return &stubRows{data: []lanes.LaneResult{{}}}, nil
			}
			// BM25 query — return expected results.
			return &stubRows{data: expected}, nil
		},
	}
	lane := lanes.NewParadeDBBM25Lane(db)

	got, err := lane.BM25Query(context.Background(), wsID, "bm25 query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(results) = %d; want 1", len(got))
	}
	if got[0].Score != expected[0].Score {
		t.Errorf("Score = %v; want %v", got[0].Score, expected[0].Score)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// Note: Tests that require a live ParadeDB / pg_bm25 extension are NOT included
// in this file. They should be written as integration tests with t.Skip("requires
// pg_bm25 extension") so CI passes without ParadeDB installed. Such tests would
// verify: correct score ordering, workspace_id RLS isolation, LIMIT enforcement.
