// discourse_test.go — tests for GitHub discourse intelligence ingestion.
//
// Covers: REST Link-header pagination, GraphQL cursor pagination, 70/min rate
// limiter, 429 exponential backoff honoring Retry-After, chunk shapes
// (title/body 512-64/comment), classification (≥1 ppi:* tag, batch ≤20), and
// graph edge creation (REFERENCES/MODIFIES/LINKS). GitHub-API tests use httptest;
// DB-dependent paths use in-memory seam stubs.
package discourse

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// ── REST Link-header pagination ─────────────────────────────────────────────

func TestListIssues_LinkHeaderPagination(t *testing.T) {
	var page int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := atomic.AddInt32(&page, 1)
		switch p {
		case 1:
			w.Header().Set("Link", fmt.Sprintf(`<%s/page2>; rel="next"`, "http://"+r.Host))
			fmt.Fprint(w, `[{"number":1,"title":"a","user":{"login":"x"}},{"number":2,"title":"pr","pull_request":{},"user":{"login":"x"}}]`)
		default:
			fmt.Fprint(w, `[{"number":3,"title":"b","user":{"login":"y"}}]`)
		}
	}))
	defer srv.Close()

	c := NewGitHubClient("tok", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	issues, err := c.ListIssues(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	// 3 returned in payloads, but the PR (#2) is skipped → 2 issues across 2 pages.
	if len(issues) != 2 {
		t.Fatalf("want 2 issues (PR skipped), got %d", len(issues))
	}
	if issues[0].Number != 1 || issues[1].Number != 3 {
		t.Fatalf("pagination order wrong: %+v", issues)
	}
}

// ── GraphQL cursor pagination ───────────────────────────────────────────────

func TestListIssuesGraphQL_CursorPagination(t *testing.T) {
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&call, 1)
		if c == 1 {
			fmt.Fprint(w, `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"CUR1"},"nodes":[{"number":10,"title":"t1","author":{"login":"a"}}]}}}}`)
		} else {
			fmt.Fprint(w, `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":11,"title":"t2","author":{"login":"b"}}]}}}}`)
		}
	}))
	defer srv.Close()

	c := NewGitHubClient("tok", WithGraphQLURL(srv.URL), WithHTTPClient(srv.Client()))
	issues, err := c.ListIssuesGraphQL(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Number != 10 || issues[1].Number != 11 {
		t.Fatalf("graphql cursor paging failed: %+v", issues)
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Fatalf("want 2 graphql calls, got %d", call)
	}
}

// ── Rate limiter 70/min ─────────────────────────────────────────────────────

func TestRateLimiter_70PerMinute(t *testing.T) {
	c := NewGitHubClient("tok")
	// Verify the limiter is configured at 70/min with burst 70.
	want := rate.Limit(70.0 / 60.0)
	if c.limiter.Limit() != want {
		t.Fatalf("limiter rate = %v, want %v", c.limiter.Limit(), want)
	}
	if c.limiter.Burst() != 70 {
		t.Fatalf("limiter burst = %d, want 70", c.limiter.Burst())
	}
	// A fresh limiter (burst 70) allows exactly 70 immediate tokens, then throttles.
	l := rate.NewLimiter(rate.Limit(70.0/60.0), 70)
	allowed := 0
	for i := 0; i < 80; i++ {
		if l.Allow() {
			allowed++
		}
	}
	if allowed != 70 {
		t.Fatalf("burst allowed %d, want 70", allowed)
	}
}

// ── 429 backoff + Retry-After honored exactly ───────────────────────────────

func TestDo_429Backoff_HonorsRetryAfter(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `rate limited`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	var slept time.Duration
	c := NewGitHubClient("tok",
		WithBaseURL(srv.URL), WithHTTPClient(srv.Client()),
		WithSleep(func(d time.Duration) { slept = d }),
	)
	_, err := c.ListIssues(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if slept != 5*time.Second {
		t.Fatalf("Retry-After not honored exactly: slept %v, want 5s", slept)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("want 2 attempts (429 then ok), got %d", hits)
	}
}

func TestDo_429Backoff_ExponentialDefault(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 403 (abuse) twice with no Retry-After → default exp backoff (60s, 120s).
		if atomic.AddInt32(&hits, 1) <= 2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	var slept []time.Duration
	c := NewGitHubClient("tok",
		WithBaseURL(srv.URL), WithHTTPClient(srv.Client()),
		WithSleep(func(d time.Duration) { slept = append(slept, d) }),
	)
	if _, err := c.ListIssues(context.Background(), "o", "r"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 2 || slept[0] != 60*time.Second || slept[1] != 120*time.Second {
		t.Fatalf("exp backoff wrong: %v (want [60s 120s])", slept)
	}
}

// ── Chunk shapes ────────────────────────────────────────────────────────────

func TestChunkIssue_Shapes(t *testing.T) {
	body := strings.Repeat("word ", 600) // 600 tokens → spills 512/64 window
	is := Issue{Number: 7, Repo: "o/r", Title: "Title here", Body: body,
		Comments: []Comment{{Body: "first comment"}, {Body: ""}, {Body: "second"}}}
	chunks := ChunkIssue("ws", is)

	if chunks[0].Part != "title" || chunks[0].Content != "Title here" {
		t.Fatalf("first chunk must be title, got %+v", chunks[0])
	}
	var body0 *Chunk
	commentCount := 0
	for i := range chunks {
		ch := chunks[i]
		if ch.DocType != "comment" {
			t.Fatalf("doc_type must be comment, got %q", ch.DocType)
		}
		if ch.SourceType != "issue" {
			t.Fatalf("source_type must be issue, got %q", ch.SourceType)
		}
		if ch.SourceRef != "7" {
			t.Fatalf("source_ref must be 7, got %q", ch.SourceRef)
		}
		if ch.Part == "body" && body0 == nil {
			body0 = &chunks[i]
		}
		if ch.Part == "comment" {
			commentCount++
		}
	}
	// 600 tokens, step=448 → windows at [0:512],[448:600] = 2 body chunks.
	bodyCount := 0
	for _, ch := range chunks {
		if ch.Part == "body" {
			bodyCount++
		}
	}
	if bodyCount != 2 {
		t.Fatalf("want 2 body chunks (512/64 over 600), got %d", bodyCount)
	}
	if got := len(strings.Fields(body0.Content)); got != 512 {
		t.Fatalf("first body chunk must be 512 tokens, got %d", got)
	}
	if commentCount != 2 { // empty comment skipped
		t.Fatalf("want 2 comment chunks (empty skipped), got %d", commentCount)
	}
}

func TestChunkBodyOverlap(t *testing.T) {
	body := strings.Repeat("a ", 512) + strings.Repeat("b ", 100) // 612 tokens
	chunks := bodyChunks("ws", "o/r", "1", SourcePR, body, 1)
	if len(chunks) != 2 {
		t.Fatalf("want 2 body chunks, got %d", len(chunks))
	}
	w0 := strings.Fields(chunks[0].Content)
	w1 := strings.Fields(chunks[1].Content)
	// step = 512-64 = 448; chunk1 starts at word 448 → last 64 of chunk0 overlap.
	if w1[0] != w0[448] {
		t.Fatalf("overlap mismatch: chunk1[0]=%q chunk0[448]=%q", w1[0], w0[448])
	}
}

func TestChunkCommit_TitleBody(t *testing.T) {
	c := Commit{SHA: "abc123", Repo: "o/r", Message: "fix: thing\n\nlonger body text here"}
	chunks := ChunkCommit("ws", c)
	if chunks[0].Part != "title" || chunks[0].Content != "fix: thing" {
		t.Fatalf("commit title chunk wrong: %+v", chunks[0])
	}
	if chunks[0].SourceType != "commit" || chunks[0].SourceRef != "abc123" {
		t.Fatalf("commit source meta wrong: %+v", chunks[0])
	}
	if len(chunks) < 2 || chunks[1].Part != "body" {
		t.Fatalf("commit body chunk missing: %+v", chunks)
	}
}

func TestChunkReviewComment_Shape(t *testing.T) {
	rc := ReviewComment{ID: 999, PRNumber: 3, Repo: "o/r", Body: "nit: rename this"}
	chunks := ChunkReviewComment("ws", rc)
	if len(chunks) != 1 || chunks[0].Part != "comment" {
		t.Fatalf("want 1 comment chunk, got %+v", chunks)
	}
	if chunks[0].SourceType != "review_comment" || chunks[0].SourceRef != "999" {
		t.Fatalf("review comment meta wrong: %+v", chunks[0])
	}
}

// ── Classification: ≥1 ppi:* tag + batch ≤20 ────────────────────────────────

type stubCompleter struct {
	calls    int32
	maxBatch int
	reply    func(items int) string
}

func (s *stubCompleter) Complete(_ context.Context, req ClassifyRequest) (*ClassifyResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	// Count items in the prompt to enforce batch size assertion.
	n := strings.Count(req.Prompt, "\n0.\n") // not reliable; use marker below
	_ = n
	items := strings.Count(req.Prompt, "[") // each item line has "[kind]"
	if items > s.maxBatch {
		s.maxBatch = items
	}
	// Derive count from "Return a JSON array of N tag-arrays."
	cnt := parseReturnCount(req.Prompt)
	return &ClassifyResponse{Content: s.reply(cnt)}, nil
}

func parseReturnCount(prompt string) int {
	marker := "Return a JSON array of "
	i := strings.Index(prompt, marker)
	if i < 0 {
		return 0
	}
	rest := prompt[i+len(marker):]
	end := strings.Index(rest, " ")
	if end < 0 {
		return 0
	}
	n := 0
	for _, ch := range rest[:end] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func TestBatchClassify_TagsAndBatchSize(t *testing.T) {
	stub := &stubCompleter{reply: func(items int) string {
		var parts []string
		for i := 0; i < items; i++ {
			parts = append(parts, `["ppi:bug"]`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}}
	cl := NewClassifier(stub)

	var items []DiscourseItem
	for i := 0; i < 45; i++ { // 45 items → 3 batches (20,20,5)
		items = append(items, DiscourseItem{Kind: SourceIssue, Ref: itoa(i), Title: "t", Body: "b"})
	}
	out, err := cl.BatchClassify(context.Background(), items, []string{"ppi:bug", "ppi:feature"})
	if err != nil {
		t.Fatal(err)
	}
	if stub.calls != 3 {
		t.Fatalf("want 3 batches for 45 items, got %d calls", stub.calls)
	}
	if stub.maxBatch > 20 {
		t.Fatalf("batch exceeded 20: %d", stub.maxBatch)
	}
	for _, it := range out {
		if len(it.Tags) == 0 {
			t.Fatalf("item missing tag: %+v", it)
		}
		if !strings.HasPrefix(it.Tags[0], "ppi:") {
			t.Fatalf("tag missing ppi: prefix: %v", it.Tags)
		}
	}
}

func TestBatchClassify_FallbackGuaranteesPPITag(t *testing.T) {
	// Model returns a non-allowed / non-ppi tag → fallback must apply.
	stub := &stubCompleter{reply: func(items int) string {
		var parts []string
		for i := 0; i < items; i++ {
			parts = append(parts, `["random","not-ppi"]`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}}
	cl := NewClassifier(stub)
	items := []DiscourseItem{{Kind: SourcePR, Ref: "1", Body: "x"}}
	out, _ := cl.BatchClassify(context.Background(), items, []string{"ppi:bug"})
	if len(out[0].Tags) != 1 || out[0].Tags[0] != fallbackTag {
		t.Fatalf("want fallback ppi tag, got %v", out[0].Tags)
	}
}

// ── Graph edges: REFERENCES / MODIFIES / LINKS ──────────────────────────────

func TestBuildPREdges_AllKinds(t *testing.T) {
	known := map[string]bool{"GitHubClient": true, "BatchClassify": true}
	pr := PR{
		Number: 5, Repo: "o/r", Title: "Use GitHubClient", Body: "closes #3 and calls BatchClassify",
		ChangedFiles: []string{"a.go", "b.go"}, LinkedIssues: ExtractLinkedIssues("closes #3"),
	}
	edges := BuildPREdges("ws", pr, known)

	var refs, mods, links int
	for _, e := range edges {
		switch e.Kind {
		case EdgeReferences:
			refs++
			if e.DstType != "symbol" {
				t.Fatalf("REFERENCES dst must be symbol: %+v", e)
			}
		case EdgeModifies:
			mods++
			if e.DstType != "file" {
				t.Fatalf("MODIFIES dst must be file: %+v", e)
			}
		case EdgeLinks:
			links++
			if e.DstType != "issue" || e.DstRef != "3" {
				t.Fatalf("LINKS edge wrong: %+v", e)
			}
		}
	}
	if refs != 2 {
		t.Fatalf("want 2 REFERENCES (GitHubClient, BatchClassify), got %d", refs)
	}
	if mods != 2 {
		t.Fatalf("want 2 MODIFIES, got %d", mods)
	}
	if links != 1 {
		t.Fatalf("want 1 LINKS, got %d", links)
	}
}

func TestBuildCommitEdges_ReferencesAndModifies(t *testing.T) {
	known := map[string]bool{"Ingestor": true}
	c := Commit{SHA: "sha1", Repo: "o/r", Message: "refactor Ingestor", ChangedFiles: []string{"ingestor.go"}}
	edges := BuildCommitEdges("ws", c, known)
	var refs, mods int
	for _, e := range edges {
		if e.Kind == EdgeReferences && e.DstRef == "Ingestor" {
			refs++
		}
		if e.Kind == EdgeModifies && e.DstRef == "ingestor.go" {
			mods++
		}
	}
	if refs != 1 || mods != 1 {
		t.Fatalf("want 1 ref + 1 mod, got %d ref %d mod", refs, mods)
	}
}

func TestExtractLinkedIssues(t *testing.T) {
	got := ExtractLinkedIssues("Fixes #12, also closes #34. see #99")
	if len(got) != 2 || got[0] != 12 || got[1] != 34 {
		t.Fatalf("closing-keyword extraction wrong: %v", got)
	}
	// No closing keyword → fall back to bare refs.
	got2 := ExtractLinkedIssues("relates to #7 and #7")
	if len(got2) != 1 || got2[0] != 7 {
		t.Fatalf("bare-ref fallback wrong: %v", got2)
	}
}

// ── Ingestor end-to-end with seam stubs ─────────────────────────────────────

type stubChunkSink struct{ saved []Chunk }

func (s *stubChunkSink) SaveChunks(_ context.Context, c []Chunk) (int, error) {
	s.saved = append(s.saved, c...)
	return len(c), nil
}

type stubEdgeSink struct{ edges []Edge }

func (s *stubEdgeSink) UpsertEdges(_ context.Context, e []Edge) error {
	s.edges = append(s.edges, e...)
	return nil
}

func TestIngestor_EndToEnd(t *testing.T) {
	stubCl := &stubCompleter{reply: func(items int) string {
		var parts []string
		for i := 0; i < items; i++ {
			parts = append(parts, `["ppi:feature"]`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}}
	cs := &stubChunkSink{}
	es := &stubEdgeSink{}
	ing := NewIngestor(NewClassifier(stubCl), cs, es)

	res, err := ing.Ingest(context.Background(), IngestInput{
		WorkspaceID:  "ws1",
		Issues:       []Issue{{Number: 1, Repo: "o/r", Title: "T", Body: "uses Foo"}},
		PRs:          []PR{{Number: 2, Repo: "o/r", Title: "P", Body: "closes #1", ChangedFiles: []string{"f.go"}, LinkedIssues: []int{1}}},
		Commits:      []Commit{{SHA: "c1", Repo: "o/r", Message: "msg", ChangedFiles: []string{"g.go"}}},
		KnownSymbols: map[string]bool{"Foo": true},
		Taxonomy:     []string{"ppi:feature"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemsClassified != 3 {
		t.Fatalf("want 3 classified, got %d", res.ItemsClassified)
	}
	if res.ChunksEnqueued == 0 || len(cs.saved) != res.ChunksEnqueued {
		t.Fatalf("chunk enqueue mismatch: res=%d saved=%d", res.ChunksEnqueued, len(cs.saved))
	}
	if res.EdgesUpserted == 0 || len(es.edges) != res.EdgesUpserted {
		t.Fatalf("edge upsert mismatch: res=%d edges=%d", res.EdgesUpserted, len(es.edges))
	}
	// Every saved chunk must carry the ppi:* tag.
	for _, ch := range cs.saved {
		if len(ch.Tags) == 0 || !strings.HasPrefix(ch.Tags[0], "ppi:") {
			t.Fatalf("chunk missing ppi tag: %+v", ch)
		}
	}
}

func TestIngestor_RequiresWorkspaceID(t *testing.T) {
	ing := NewIngestor(nil, nil, nil)
	if _, err := ing.Ingest(context.Background(), IngestInput{}); err == nil {
		t.Fatal("want error on empty workspace_id")
	}
}

// DB-dependent integration paths (real clawde_chunks / clawde_graph_edges) are
// skipped here — they require a live Postgres + pgmq and run in the worker/DB
// suite, not the unit suite.
func TestIngestor_DBIntegration_Skipped(t *testing.T) {
	t.Skip("requires live Postgres + pgmq (clawde_chunks / clawde_embed_queue / clawde_graph_edges); covered by DB integration suite")
}
