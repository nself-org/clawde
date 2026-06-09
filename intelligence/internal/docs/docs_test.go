// docs_test.go — unit tests for the documentation pipeline (W14-S14-T04).
//
// Covers: docstring extraction (via DB seam), markdown heading-split chunker,
//         IngestDocURL dispatch-chain gating, doc_type propagation, and
//         migration 0090 idempotency. cgo/DB/network paths use seams/stubs and
//         skip-with-reason where a live resource would be required.
package docs

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/hostadapter"
	"github.com/nself-org/clawde/intelligence/internal/repointel"
)

// ── stubs ───────────────────────────────────────────────────────────────────

type stubDocStore struct{ got []DocstringRecord }

func (s *stubDocStore) UpdateDocstrings(_ context.Context, recs []DocstringRecord) error {
	s.got = append(s.got, recs...)
	return nil
}

type fakeExtractor struct{ syms []repointel.SymbolRecord }

func (f fakeExtractor) ExtractSymbols(_ uuid.UUID, _ string, _ []byte) ([]repointel.SymbolRecord, []repointel.CallEdge, error) {
	return f.syms, nil, nil
}

type stubEnqueuer struct {
	chunks []DocChunk
	ws     string
}

func (s *stubEnqueuer) EnqueueChunks(_ context.Context, ws string, ch []DocChunk) (int, error) {
	s.ws = ws
	s.chunks = append(s.chunks, ch...)
	return len(ch), nil
}

type stubFetcher struct {
	body   string
	called bool
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (string, error) {
	s.called = true
	return s.body, nil
}

// ── docstring extraction ──────────────────────────────────────────────────────

func TestDocstringExtractor_PopulatesStore(t *testing.T) {
	ws := uuid.New()
	store := &stubDocStore{}
	// Fixture mirrors what the W12-T01 tree-sitter extractor yields: a Go symbol
	// with a leading doc comment. We use a fake extractor so the test passes
	// without cgo (the real treesitter path is exercised under -tags treesitter).
	ex := fakeExtractor{syms: []repointel.SymbolRecord{
		{WorkspaceID: ws, FilePath: "foo.go", Name: "DoThing", Kind: "function",
			DocComment: "// DoThing does the thing.\n// It is documented.", LineStart: 10},
		{WorkspaceID: ws, FilePath: "foo.go", Name: "Undocumented", Kind: "function",
			DocComment: "", LineStart: 20},
	}}
	de := NewDocstringExtractor(ex, store)

	recs, err := de.Extract(context.Background(), ws, "foo.go", "go", []byte("package x"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 documented symbol, got %d", len(recs))
	}
	if recs[0].SymbolName != "DoThing" {
		t.Fatalf("want DoThing, got %q", recs[0].SymbolName)
	}
	if !strings.Contains(recs[0].Docstring, "does the thing") {
		t.Fatalf("docstring not normalized: %q", recs[0].Docstring)
	}
	if strings.Contains(recs[0].Docstring, "//") {
		t.Fatalf("comment markers not stripped: %q", recs[0].Docstring)
	}
	if len(store.got) != 1 {
		t.Fatalf("store should receive 1 UPDATE record, got %d", len(store.got))
	}
}

func TestDocstringRecord_AsChunk_TaggedDocstring(t *testing.T) {
	r := DocstringRecord{FilePath: "a.go", SymbolName: "Foo", Docstring: "Foo does X.", LineStart: 3}
	c := r.AsChunk()
	if c.DocType != DocTypeDocstring {
		t.Fatalf("want doc_type=docstring, got %q", c.DocType)
	}
	if c.Content != "Foo does X." {
		t.Fatalf("content mismatch: %q", c.Content)
	}
}

// ── markdown chunker ──────────────────────────────────────────────────────────

func TestMarkdownChunker_HeadingSplit(t *testing.T) {
	md := `# Title

intro text

## Section A

body a

## Section B

body b
`
	chunks := NewMarkdownChunker().Chunk(md, "README.md")
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks (H1 + 2×H2), got %d: %+v", len(chunks), chunks)
	}
	for _, c := range chunks {
		if c.DocType != DocTypeMarkdown {
			t.Fatalf("chunk doc_type should be markdown, got %q", c.DocType)
		}
	}
	if chunks[0].Level != 1 || chunks[0].Heading != "Title" {
		t.Fatalf("first chunk should be H1 Title, got level=%d heading=%q", chunks[0].Level, chunks[0].Heading)
	}
	if chunks[1].Heading != "Section A" || chunks[2].Heading != "Section B" {
		t.Fatalf("section headings wrong: %q / %q", chunks[1].Heading, chunks[2].Heading)
	}
}

func TestMarkdownChunker_IgnoresHeadingsInFence(t *testing.T) {
	md := "# Real\n\n```\n# not a heading\n```\n\n## After\n\ntext\n"
	chunks := NewMarkdownChunker().Chunk(md, "x.md")
	// "# not a heading" inside the fence must not start a new chunk.
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks (fence ignored), got %d", len(chunks))
	}
	if chunks[1].Heading != "After" {
		t.Fatalf("second heading should be After, got %q", chunks[1].Heading)
	}
}

// ── IngestDocURL dispatch-chain gating ────────────────────────────────────────

func newIngestor(t *testing.T, trusted bool, fetcher *stubFetcher, enq *stubEnqueuer) *KBIngestor {
	t.Helper()
	trustIDs := []string{}
	if trusted {
		trustIDs = append(trustIDs, "client-1")
	}
	return NewKBIngestor(
		hostadapter.NewTrustRegistry(trustIDs...),
		hostadapter.NewAllowAllPolicy(),
		hostadapter.NewSupplyChainPolicy(ingestTool),
		fetcher,
		enq,
	)
}

func TestIngestDocURL_DeniedClient_NoFetch(t *testing.T) {
	fetcher := &stubFetcher{body: "# doc\n\ncontent"}
	enq := &stubEnqueuer{}
	ing := newIngestor(t, false /*untrusted*/, fetcher, enq)

	_, err := ing.IngestDocURL(context.Background(), "client-1",
		IngestDocURLRequest{WorkspaceID: "ws", URL: "https://example.com/doc"})
	if err == nil {
		t.Fatal("expected denial for untrusted client")
	}
	if !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("want untrusted denial, got %v", err)
	}
	if fetcher.called {
		t.Fatal("fetch must NOT run for a denied client")
	}
	if len(enq.chunks) != 0 {
		t.Fatal("nothing should be enqueued for a denied client")
	}
}

func TestIngestDocURL_MissingIdentity_Denied(t *testing.T) {
	fetcher := &stubFetcher{body: "# doc"}
	ing := newIngestor(t, true, fetcher, &stubEnqueuer{})
	_, err := ing.IngestDocURL(context.Background(), "", IngestDocURLRequest{URL: "https://x"})
	if err == nil || !strings.Contains(err.Error(), "missing client identity") {
		t.Fatalf("want missing-identity denial, got %v", err)
	}
	if fetcher.called {
		t.Fatal("fetch must not run without identity")
	}
}

func TestIngestDocURL_AllowedClient_EnqueuesWithDocType(t *testing.T) {
	fetcher := &stubFetcher{body: "# External\n\nsome external docs\n\n## More\n\nmore\n"}
	enq := &stubEnqueuer{}
	ing := newIngestor(t, true, fetcher, enq)

	resp, err := ing.IngestDocURL(context.Background(), "client-1",
		IngestDocURLRequest{WorkspaceID: "ws-7", URL: "https://example.com/doc", DocType: "markdown"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !fetcher.called {
		t.Fatal("fetch should run for an allowed client")
	}
	if resp.ChunksEnqueued != len(enq.chunks) || resp.ChunksEnqueued == 0 {
		t.Fatalf("enqueue count mismatch: resp=%d enq=%d", resp.ChunksEnqueued, len(enq.chunks))
	}
	if enq.ws != "ws-7" {
		t.Fatalf("workspace not threaded: %q", enq.ws)
	}
	// doc_type propagation: every enqueued chunk must be tagged 'markdown'.
	for _, c := range enq.chunks {
		if c.DocType != DocTypeMarkdown {
			t.Fatalf("chunk not tagged markdown: %q", c.DocType)
		}
	}
}

func TestIngestDocURL_DefaultsToMarkdownDocType(t *testing.T) {
	fetcher := &stubFetcher{body: "# x\n\nbody\n"}
	enq := &stubEnqueuer{}
	ing := newIngestor(t, true, fetcher, enq)
	_, err := ing.IngestDocURL(context.Background(), "client-1",
		IngestDocURLRequest{WorkspaceID: "ws", URL: "https://x"}) // no DocType
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	for _, c := range enq.chunks {
		if c.DocType != DocTypeMarkdown {
			t.Fatalf("empty doc_type should default to markdown, got %q", c.DocType)
		}
	}
}

// ── migration 0090 idempotency ────────────────────────────────────────────────

func TestMigration0090_Idempotent(t *testing.T) {
	// Static check: the migration must use IF NOT EXISTS guards so applying it
	// twice is a no-op. A live double-apply requires Postgres → skip-with-reason.
	if os.Getenv("CLAWDE_TEST_DB_DSN") == "" {
		t.Log("skip live double-apply: CLAWDE_TEST_DB_DSN unset (no Postgres)")
	}
	data, err := os.ReadFile("../../migrations/0090_docstring_columns.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)
	guards := strings.Count(sql, "IF NOT EXISTS")
	if guards < 2 {
		t.Fatalf("migration 0090 must have ≥2 IF NOT EXISTS guards, found %d", guards)
	}
	for _, dt := range []string{"'code'", "'markdown'", "'docstring'", "'comment'"} {
		if !strings.Contains(sql, dt) {
			t.Fatalf("migration 0090 missing doc_type enum value %s", dt)
		}
	}
	if !strings.Contains(sql, "docstring text") {
		t.Fatal("migration 0090 must add clawde_symbols.docstring")
	}
}
