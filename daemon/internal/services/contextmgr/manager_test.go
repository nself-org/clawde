package contextmgr_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/nself-org/clawde/daemon/internal/services/contextmgr"
)

// openTestDB returns an in-memory SQLite DB with migrations applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := contextmgr.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return db
}

// makeMessages returns n simple ContextRow values for testing.
func makeMessages(n int) []contextmgr.ContextRow {
	rows := make([]contextmgr.ContextRow, n)
	for i := 0; i < n; i++ {
		rows[i] = contextmgr.ContextRow{
			ID:       fmt.Sprintf("msg-%03d", i),
			Role:     "user",
			Content:  fmt.Sprintf("This is message %d. It contains some text.", i),
			TokenEst: 10,
		}
	}
	return rows
}

// TestSnapshotAndRestore verifies the round-trip: Snapshot persists, Restore returns the record.
func TestSnapshotAndRestore(t *testing.T) {
	db := openTestDB(t)
	mgr := contextmgr.New(db, "")
	ctx := context.Background()

	messages := makeMessages(5)
	rec, err := mgr.Snapshot(ctx, "session-abc", messages)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec == nil {
		t.Fatal("Snapshot returned nil record")
	}
	if rec.SessionID != "session-abc" {
		t.Errorf("SessionID = %q, want session-abc", rec.SessionID)
	}
	if rec.CoveredFrom != "msg-000" {
		t.Errorf("CoveredFrom = %q, want msg-000", rec.CoveredFrom)
	}
	if rec.CoveredTo != "msg-004" {
		t.Errorf("CoveredTo = %q, want msg-004", rec.CoveredTo)
	}
	if rec.Summary == "" {
		t.Error("Summary is empty")
	}

	// Restore must return the same record.
	restored, err := mgr.Restore(ctx, "session-abc")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored == nil {
		t.Fatal("Restore returned nil")
	}
	if restored.ID != rec.ID {
		t.Errorf("Restored ID = %q, want %q", restored.ID, rec.ID)
	}
	if restored.Summary != rec.Summary {
		t.Errorf("Restored Summary mismatch")
	}
}

// TestRestoreNoSnapshot verifies Restore returns nil (not an error) when no snapshot exists.
func TestRestoreNoSnapshot(t *testing.T) {
	db := openTestDB(t)
	mgr := contextmgr.New(db, "")
	ctx := context.Background()

	rec, err := mgr.Restore(ctx, "nonexistent-session")
	if err != nil {
		t.Fatalf("Restore: unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("Restore expected nil for nonexistent session, got %+v", rec)
	}
}

// TestExtractiveNoAI verifies that with CLAWDE_SERVER_MODE=false the extractive summary is used.
func TestExtractiveNoAI(t *testing.T) {
	// Ensure server mode is off.
	t.Setenv("CLAWDE_SERVER_MODE", "false")

	db := openTestDB(t)
	mgr := contextmgr.New(db, "") // no AI endpoint
	ctx := context.Background()

	messages := []contextmgr.ContextRow{
		{ID: "m1", Role: "user", Content: "Hello world. Extra text here.", TokenEst: 5},
		{ID: "m2", Role: "assistant", Content: "Hi there. How can I help?", TokenEst: 5},
	}

	rec, err := mgr.Snapshot(ctx, "sess-ext", messages)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Summary == "" {
		t.Error("Summary empty for extractive fallback")
	}
	// Extractive summary should contain first sentences.
	if !strings.Contains(rec.Summary, "Hello world") {
		t.Errorf("Summary should contain 'Hello world', got: %q", rec.Summary)
	}
}

// TestServerModeAISummarise verifies that with CLAWDE_SERVER_MODE=true the mock AI is called.
func TestServerModeAISummarise(t *testing.T) {
	const wantSummary = "AI-generated summary of the session."

	// Start a test HTTP server that mimics plugin-ai.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wantSummary))
	}))
	defer srv.Close()

	t.Setenv("CLAWDE_SERVER_MODE", "true")

	db := openTestDB(t)
	mgr := contextmgr.New(db, srv.URL)
	ctx := context.Background()

	messages := makeMessages(3)
	rec, err := mgr.Snapshot(ctx, "sess-ai", messages)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", rec.Summary, wantSummary)
	}
}

// TestServerModeAIFallback verifies that when plugin-ai fails, extractive summary is used.
func TestServerModeAIFallback(t *testing.T) {
	// Point at a server that returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("CLAWDE_SERVER_MODE", "true")

	db := openTestDB(t)
	mgr := contextmgr.New(db, srv.URL)
	ctx := context.Background()

	messages := []contextmgr.ContextRow{
		{ID: "x1", Role: "user", Content: "Fallback test sentence. More content.", TokenEst: 8},
	}
	rec, err := mgr.Snapshot(ctx, "sess-fallback", messages)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Should still produce a non-empty extractive summary.
	if rec.Summary == "" {
		t.Error("Expected fallback summary, got empty string")
	}
	if !strings.Contains(rec.Summary, "Fallback test sentence") {
		t.Errorf("Fallback summary should contain first sentence, got: %q", rec.Summary)
	}
}

// TestCorruptSessionHandledGracefully verifies Restore on a session with no rows
// does not panic and returns nil record.
func TestCorruptSessionHandledGracefully(t *testing.T) {
	db := openTestDB(t)
	mgr := contextmgr.New(db, "")
	ctx := context.Background()

	rec, err := mgr.Restore(ctx, "")
	if err != nil {
		t.Fatalf("Restore empty sessionID: unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("Expected nil record for empty session, got %+v", rec)
	}
}

// TestSummaryMessageFlag verifies SummaryMessage produces IsSummary=true.
func TestSummaryMessageFlag(t *testing.T) {
	db := openTestDB(t)
	mgr := contextmgr.New(db, "")
	ctx := context.Background()

	messages := makeMessages(2)
	rec, err := mgr.Snapshot(ctx, "sess-flag", messages)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	msg := contextmgr.SummaryMessage(rec)
	if !msg.IsSummary {
		t.Error("SummaryMessage.IsSummary should be true")
	}
	if msg.Content != rec.Summary {
		t.Errorf("SummaryMessage.Content mismatch")
	}
}

// TestSnapshotEmptyMessages verifies Snapshot returns an error for empty slice.
func TestSnapshotEmptyMessages(t *testing.T) {
	db := openTestDB(t)
	mgr := contextmgr.New(db, "")
	ctx := context.Background()

	_, err := mgr.Snapshot(ctx, "sess-empty", []contextmgr.ContextRow{})
	if err == nil {
		t.Error("Expected error for empty messages slice")
	}
}

// TestNewFromFileMigration verifies NewFromFile creates the table and returns a working Manager.
func TestNewFromFileMigration(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "clawd-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()
	path := tmpFile.Name()
	t.Cleanup(func() { _ = os.Remove(path) })

	mgr, db, err := contextmgr.NewFromFile(path, "")
	if err != nil {
		t.Fatalf("NewFromFile: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	messages := makeMessages(3)
	rec, err := mgr.Snapshot(ctx, "sess-file", messages)
	if err != nil {
		t.Fatalf("Snapshot after NewFromFile: %v", err)
	}
	if rec == nil {
		t.Fatal("nil record from Snapshot after NewFromFile")
	}
}
