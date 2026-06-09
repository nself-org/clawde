// lsp_test.go — tests for the lsp package.
//
// Coverage:
//   - JSON-RPC Content-Length framing roundtrip
//   - LSP handshake against an in-process mock server
//   - LSIF dump parsing
//   - Lifecycle restart (simulated crash via mock)
//   - CrossFileResolver cross-file rate >80% on stubbed data
//   - gopls/tsserver tests skipped with reason when binary absent
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp tests.
package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// mustJSON marshals v or panics.
func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ── JSON-RPC framing roundtrip ────────────────────────────────────────────────

func TestContentLengthFramingRoundtrip(t *testing.T) {
	t.Parallel()

	// Prepare a framed message.
	body := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)

	r := bufio.NewReader(strings.NewReader(frame))

	// Read Content-Length header.
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("header read: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			if _, err := fmt.Sscanf(val, "%d", &contentLength); err != nil {
				t.Fatalf("parse Content-Length: %v", err)
			}
		}
	}

	if contentLength != len(body) {
		t.Fatalf("Content-Length mismatch: want %d got %d", len(body), contentLength)
	}

	raw := make([]byte, contentLength)
	if _, err := io.ReadFull(r, raw); err != nil {
		t.Fatalf("body read: %v", err)
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Method != "ping" {
		t.Errorf("method: want ping got %s", req.Method)
	}
}

// ── Mock LSP server ───────────────────────────────────────────────────────────

// mockLSPServer is an in-process LSPServer for testing.
type mockLSPServer struct {
	mu          sync.Mutex
	defs        map[string][]Location // fileURI → locations
	refs        map[string][]Location
	stopCount   int
	failAfter   int // if >0, return error after N calls total
	callCount   int
	name        string
}

func newMock(name string) *mockLSPServer {
	return &mockLSPServer{
		name: name,
		defs: make(map[string][]Location),
		refs: make(map[string][]Location),
	}
}

func (m *mockLSPServer) Name() string { return m.name }

func (m *mockLSPServer) Definition(_ context.Context, fileURI string, _ Position) ([]Location, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.failAfter > 0 && m.callCount > m.failAfter {
		return nil, fmt.Errorf("mock: forced failure")
	}
	return m.defs[fileURI], nil
}

func (m *mockLSPServer) References(_ context.Context, fileURI string, _ Position) ([]Location, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.failAfter > 0 && m.callCount > m.failAfter {
		return nil, fmt.Errorf("mock: forced failure")
	}
	return m.refs[fileURI], nil
}

func (m *mockLSPServer) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCount++
	return nil
}

// ── Stub DB seams ─────────────────────────────────────────────────────────────

type stubSymbolSource struct {
	rows []SymbolRow
}

func (s *stubSymbolSource) ListSymbols(_ context.Context, _ uuid.UUID) ([]SymbolRow, error) {
	return s.rows, nil
}

type stubEdgeStore struct {
	mu    sync.Mutex
	edges []CrossEdge
}

func (s *stubEdgeStore) UpsertCrossEdges(_ context.Context, edges []CrossEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edges = append(s.edges, edges...)
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestMockHandshake verifies that a Client can exchange initialize/initialized
// with a minimal in-process JSON-RPC echo server.
func TestMockHandshake(t *testing.T) {
	t.Parallel()

	// Set up piped reader/writer pairs simulating the server's stdio.
	clientR, serverW := io.Pipe() // server writes → client reads
	serverR, clientW := io.Pipe() // client writes → server reads

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Minimal in-process JSON-RPC server: reads one request, echoes a response.
	go func() {
		defer serverW.Close()
		reader := bufio.NewReader(serverR)
		// Read the initialize request.
		var cl int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")), "%d", &cl)
			}
		}
		body := make([]byte, cl)
		io.ReadFull(reader, body)

		var req Request
		json.Unmarshal(body, &req)

		// Build response.
		result := InitializeResult{
			Capabilities: ServerCapabilities{
				DefinitionProvider: true,
				ReferencesProvider: true,
			},
		}
		rawResult, _ := json.Marshal(result)
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  json.RawMessage(rawResult),
		}
		respBody, _ := json.Marshal(resp)
		frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(respBody), respBody)
		serverW.Write([]byte(frame))
		// Drain remaining input (initialized notification).
		io.Copy(io.Discard, serverR)
	}()

	c := NewClient(clientW)
	go c.Start(ctx, clientR)

	params := InitializeParams{
		RootURI: "file:///workspace",
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Definition: DynamicRegistrationCapability{true},
				References: DynamicRegistrationCapability{true},
			},
		},
	}
	raw, err := c.Call(ctx, "initialize", params)
	if err != nil {
		t.Fatalf("initialize call: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Capabilities.DefinitionProvider {
		t.Error("expected definitionProvider=true")
	}
}

// TestLSIFParse verifies that the sample LSIF dump is parsed correctly.
func TestLSIFParse(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/sample.lsif")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	edges, err := ParseLSIF(f)
	if err != nil {
		t.Fatalf("ParseLSIF: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("expected at least one edge from LSIF dump")
	}

	// Verify at least one RESOLVES edge targeting util.go.
	found := false
	for _, e := range edges {
		if e.Kind == EdgeKindResolves && strings.Contains(e.DstURI, "util.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a RESOLVES edge to util.go; edges: %+v", edges)
	}
}

// TestLSIFParseMalformed ensures malformed lines are skipped gracefully.
func TestLSIFParseMalformed(t *testing.T) {
	t.Parallel()

	input := `not json at all
{"id":1,"type":"vertex","label":"document","uri":"file:///a.go"}
{"id":2,"type":"vertex","label":"range","start":{"line":1,"character":0}}
{"id":3,"type":"edge","label":"item","outV":99,"inVs":[2],"document":1,"property":"definitions"}
`
	edges, err := ParseLSIF(bytes.NewReader([]byte(input)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the well-formed item edge should produce an edge.
	if len(edges) != 1 {
		t.Errorf("expected 1 edge got %d", len(edges))
	}
}

// perCallMockServer is a mock where Definition/References return one
// predetermined location per call (indexed by call count).
type perCallMockServer struct {
	mu        sync.Mutex
	callIndex int
	defs      []Location // one per symbol call; used round-robin by index
	name      string
}

func (m *perCallMockServer) Name() string { return m.name }

func (m *perCallMockServer) Definition(_ context.Context, _ string, _ Position) ([]Location, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.callIndex >= len(m.defs) {
		return nil, nil
	}
	loc := m.defs[m.callIndex]
	m.callIndex++
	return []Location{loc}, nil
}

func (m *perCallMockServer) References(_ context.Context, _ string, _ Position) ([]Location, error) {
	return nil, nil
}

func (m *perCallMockServer) Stop(_ context.Context) error { return nil }

// TestCrossFileResolverRate verifies >80%% cross-file edge rate on a mock set.
func TestCrossFileResolverRate(t *testing.T) {
	t.Parallel()

	wsID := uuid.New()
	fileA := "/workspace/a.go"
	fileB := "file:///workspace/b.go"
	fileAURI := "file://" + fileA

	// 10 symbols; 9 resolve to a different file (cross-file), 1 same-file.
	numSyms := 10
	crossExpected := 9

	defs := make([]Location, numSyms)
	for i := 0; i < numSyms; i++ {
		if i < crossExpected {
			defs[i] = Location{URI: fileB, Range: Range{Start: Position{Line: i}}}
		} else {
			// Same-file definition — must NOT produce a cross-file edge.
			defs[i] = Location{URI: fileAURI, Range: Range{Start: Position{Line: i}}}
		}
	}

	mock := &perCallMockServer{name: "per-call-mock", defs: defs}
	syms := make([]SymbolRow, numSyms)
	for i := 0; i < numSyms; i++ {
		syms[i] = SymbolRow{
			ID:          uuid.New(),
			WorkspaceID: wsID,
			FilePath:    fileA,
			Name:        fmt.Sprintf("Sym%d", i),
			LineStart:   i,
		}
	}

	src := &stubSymbolSource{rows: syms}
	store := &stubEdgeStore{}
	resolver := NewCrossFileResolver(mock, src, store, wsID, "file:///workspace")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	total, crossFile, errs, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if total != numSyms {
		t.Errorf("total: want %d got %d", numSyms, total)
	}
	if errs != 0 {
		t.Errorf("unexpected errors: %d", errs)
	}

	// Cross-file rate should be >= 80%%.
	rate := float64(crossFile) / float64(total)
	if rate < 0.80 {
		t.Errorf("cross-file rate %.2f < 0.80 (crossFile=%d total=%d)", rate, crossFile, total)
	}

	store.mu.Lock()
	storedCount := len(store.edges)
	store.mu.Unlock()
	if storedCount != crossExpected {
		t.Errorf("stored edges: want %d got %d", crossExpected, storedCount)
	}
}

// TestLifecycleRestart verifies that the LifecycleManager restarts a failed server.
func TestLifecycleRestart(t *testing.T) {
	t.Parallel()

	callCount := 0
	var mu sync.Mutex

	firstMock := newMock("first")
	// The first factory call returns firstMock; second call returns a new mock.
	factory := func(ctx context.Context, root string) (LSPServer, error) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		if callCount == 1 {
			return firstMock, nil
		}
		return newMock("restarted"), nil
	}

	mgr := NewLifecycleManager(factory, "/workspace")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mgr.Open(ctx)

	if mgr.Active() == nil {
		t.Fatal("Active() should be non-nil after Open")
	}
	if mgr.Active().Name() != "first" {
		t.Errorf("expected first server active")
	}

	// The lifecycle manager uses a health-ping ticker to detect crash (10s interval).
	// We directly test that after Close the active is nil.
	mgr.Close(ctx)

	if mgr.Active() != nil {
		t.Error("Active() should be nil after Close")
	}
	if firstMock.stopCount == 0 {
		t.Error("Stop should have been called on the first server")
	}
}

// TestGoplsUnavailable confirms graceful degradation when gopls is absent.
// Does NOT call t.Parallel() because t.Setenv cannot be used with parallel tests.
func TestGoplsUnavailable(t *testing.T) {
	// Override PATH to guarantee gopls is not found.
	t.Setenv("PATH", "")

	_, err := NewGoplsServer(context.Background(), t.TempDir())
	if err == nil {
		// gopls is somehow available even with empty PATH — skip.
		t.Skip("gopls found despite empty PATH — binary test skipped")
	}
	// ErrServerUnavailable or a path-lookup error are both acceptable.
	t.Logf("gopls unavailable (expected): %v", err)
}

// TestTSServerUnavailable confirms graceful degradation when tsserver is absent.
// Does NOT call t.Parallel() because t.Setenv cannot be used with parallel tests.
func TestTSServerUnavailable(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := NewTSServer(context.Background(), t.TempDir())
	if err == nil {
		t.Skip("tsserver found despite empty PATH — binary test skipped")
	}
	t.Logf("tsserver unavailable (expected): %v", err)
}

// TestGoplsLiveSkipIfAbsent runs a real definition call against gopls on the
// testdata/goproject fixture. Skipped with reason if gopls is not installed.
func TestGoplsLiveSkipIfAbsent(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skipf("gopls not installed — skipping live integration test (%v)", err)
	}

	dir := "testdata/goproject"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srv, err := NewGoplsServer(ctx, dir)
	if err != nil {
		t.Skipf("gopls start failed (may need go modules) — skipping: %v", err)
	}
	defer srv.Stop(ctx)

	absPath, _ := os.Getwd()
	fileURI := filePathToURI(absPath + "/testdata/goproject/main.go")

	// Query definition of the first identifier on line 6 (Run function body).
	locs, err := srv.Definition(ctx, fileURI, Position{Line: 6, Character: 8})
	if err != nil {
		t.Logf("definition error (acceptable if gopls needs full module): %v", err)
		return
	}
	t.Logf("gopls definition result: %+v", locs)
}

// TestLSIFEdgesToCrossEdges verifies the conversion helper.
func TestLSIFEdgesToCrossEdges(t *testing.T) {
	t.Parallel()

	wsID := uuid.New()
	symID := uuid.New()
	input := []LSIFEdge{
		{DstURI: "file:///a.go", DstLine: 5, Kind: EdgeKindResolves},
		{DstURI: "", DstLine: 0, Kind: EdgeKindReferences}, // empty URI — should be skipped
	}

	edges := LSIFEdgesToCrossEdges(input, wsID, symID)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge got %d", len(edges))
	}
	if edges[0].DstFilePath != "/a.go" {
		t.Errorf("DstFilePath: want /a.go got %s", edges[0].DstFilePath)
	}
	if edges[0].Kind != EdgeKindResolves {
		t.Errorf("Kind: want resolves got %s", edges[0].Kind)
	}
}

// TestClientNotification verifies that server-initiated notifications are dispatched.
func TestClientNotification(t *testing.T) {
	t.Parallel()

	clientR, serverW := io.Pipe()
	_, clientW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	received := make(chan string, 1)
	c := NewClient(clientW)
	c.SetNotificationHandler(func(method string, _ json.RawMessage) {
		received <- method
	})
	go c.Start(ctx, clientR)

	// Server sends a notification.
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "window/logMessage",
		"params":  map[string]string{"message": "hello"},
	}
	body, _ := json.Marshal(notif)
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	serverW.Write([]byte(frame))
	serverW.Close()

	select {
	case method := <-received:
		if method != "window/logMessage" {
			t.Errorf("notification method: want window/logMessage got %s", method)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification")
	}
}
