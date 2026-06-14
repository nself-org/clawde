//go:build integration

// Package integration — end-to-end tests for the clawde-intelligence organism.
//
// Gated on CLAWDE_TEST_PG_DSN; all tests SKIP without it.
// Each test allocates its own workspace_id and OS-assigned ports.
// SPORT: REGISTRY-SERVICES.md → integration-test-lane, status: active.
package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nself-org/clawde/intelligence/internal/compiler"
	"github.com/nself-org/clawde/intelligence/internal/gateway"
	"github.com/nself-org/clawde/intelligence/internal/server"
	"github.com/nself-org/clawde/intelligence/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// ── DSN guard ─────────────────────────────────────────────────────────────────

// requirePG skips the test if CLAWDE_TEST_PG_DSN is unset, and returns the DSN.
func requirePG(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CLAWDE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("CLAWDE_TEST_PG_DSN unset — skipping integration test")
	}
	return dsn
}

// ── Test workspace ────────────────────────────────────────────────────────────

// seedTestWorkspace inserts a workspace into np_clawde_trusted_workspaces and
// returns a cleanup function that deletes it.  Each caller uses a fresh UUID
// to prevent cross-test pollution.
func seedTestWorkspace(t *testing.T, pool *pgxpool.Pool) (workspaceID string, cleanup func()) {
	t.Helper()
	id := uuid.New().String()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO np_clawde_trusted_workspaces (workspace_id, created_at)
		 VALUES ($1, NOW())
		 ON CONFLICT DO NOTHING`,
		id,
	)
	if err != nil {
		t.Fatalf("seedTestWorkspace: insert: %v", err)
	}
	return id, func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM np_clawde_trusted_workspaces WHERE workspace_id=$1`, id)
	}
}

// ── Stub compiler retriever ────────────────────────────────────────────────────

// stubRetriever satisfies compiler.ContextRetriever.
// Returns a single pre-seeded chunk for any query so CompileContext returns Enriched=true.
type stubRetriever struct{}

func (stubRetriever) RetrieveContext(_ context.Context, _, _ string) (*compiler.RetrievalResult, error) {
	return &compiler.RetrievalResult{
		Chunks: []compiler.ScoredChunk{
			{FilePath: "test/seed.go", Content: "// seed content for integration test", Score: 0.95, Method: "lexical"},
		},
	}, nil
}

// ── Stub providers (satisfies gateway.Provider) ────────────────────────────────

// stubProvider is a deterministic gateway.Provider for testing.
type stubProvider struct {
	name string
}

func (s *stubProvider) Complete(_ context.Context, _ gateway.LaneRequest) (*gateway.LaneResponse, error) {
	return &gateway.LaneResponse{
		Content:  "stub response from " + s.name,
		Provider: s.name,
		Enriched: true,
	}, nil
}

func (s *stubProvider) Stream(_ context.Context, _ gateway.LaneRequest) (<-chan gateway.StreamChunk, error) {
	ch := make(chan gateway.StreamChunk, 1)
	ch <- gateway.StreamChunk{Delta: "stub stream " + s.name, Done: true}
	close(ch)
	return ch, nil
}

func (s *stubProvider) Embed(_ context.Context, _ string, dim int) ([]float32, error) {
	if dim <= 0 {
		dim = 16
	}
	return make([]float32, dim), nil
}

func (s *stubProvider) Rerank(_ context.Context, _ string, docs []string, topN int) ([]int, error) {
	n := topN
	if n > len(docs) {
		n = len(docs)
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx, nil
}

func (s *stubProvider) HealthCheck(_ context.Context) error { return nil }
func (s *stubProvider) Name() string                        { return s.name }

// ── Test server harness ────────────────────────────────────────────────────────

// testHandle holds the running server and gRPC connection for one test.
type testHandle struct {
	srv      *server.Server
	conn     *grpc.ClientConn
	grpcAddr string
	restAddr string
}

// startTestServer starts the intelligence server in-process on OS-assigned
// ports and returns a testHandle, a pgx pool, and a cleanup function.
//
// Requires CLAWDE_TEST_PG_DSN; calls t.Skip when unset.
//
// Design:
//   - One healthy stub provider wired via Config.Providers.
//   - In-process compiler backed by stubRetriever → Enriched=true.
//   - Dynamic ports via 127.0.0.1:0.
func startTestServer(t *testing.T) (*testHandle, *pgxpool.Pool, func()) {
	t.Helper()
	dsn := requirePG(t)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("startTestServer: pgxpool.New: %v", err)
	}

	grpcPort := allocPort(t)
	restPort := allocPort(t)
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	restAddr := fmt.Sprintf("127.0.0.1:%d", restPort)

	providers := []gateway.Provider{
		&stubProvider{name: "stub-primary"},
	}

	comp := compiler.NewCompiler(stubRetriever{}, nil, nil)

	cfg := server.Config{
		GRPCAddr:   grpcAddr,
		RESTAddr:   restAddr,
		HMACSecret: []byte("test-secret-32-bytes-padded-here"),
		Providers:  providers,
		Env:        "test",
		Compiler:   comp,
	}

	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		pool.Close()
		t.Fatalf("startTestServer: Start: %v", err)
	}

	conn, err := grpc.NewClient(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(hmacClientInterceptor(cfg.HMACSecret)),
	)
	if err != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
		pool.Close()
		t.Fatalf("startTestServer: grpc.NewClient: %v", err)
	}

	h := &testHandle{srv: srv, conn: conn, grpcAddr: grpcAddr, restAddr: restAddr}
	cleanup := func() {
		_ = conn.Close()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
		pool.Close()
	}
	return h, pool, cleanup
}

// hmacClientInterceptor signs every outgoing RPC with the HMAC-SHA256 secret,
// matching the server's UnaryHMACInterceptor.  Uses the empty-body SHA256
// (server default when x-clawde-body-sha256 is absent).
func hmacClientInterceptor(secret []byte) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		tsStr := strconv.FormatInt(time.Now().Unix(), 10)
		bodySHA256Hex := server.BodySHA256Hex([]byte{}) // empty-body: server default
		sig := server.ComputeSignature(secret, tsStr, bodySHA256Hex)
		md := metadata.Pairs(
			"x-clawde-timestamp", tsStr,
			"x-clawde-signature", sig,
			"x-clawde-body-sha256", bodySHA256Hex,
		)
		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// allocPort binds a listener on 127.0.0.1:0, records the OS-assigned port, and
// immediately closes the listener so the server can bind to the same address.
func allocPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocPort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// ── In-memory worker.QueueStore ────────────────────────────────────────────────

// memStore is a thread-safe in-memory QueueStore for integration tests.
// Each test that needs a worker.Pool should call newMemStore().
type memStore struct {
	mu      sync.Mutex
	queues  map[string][]*worker.Message
	depths  map[string]int64
	deleted []int64
}

func newMemStore() *memStore {
	return &memStore{
		queues: map[string][]*worker.Message{
			worker.QueueIngest:  {},
			worker.QueueEmbed:   {},
			worker.QueueAnalyze: {},
			worker.QueueLearn:   {},
			worker.QueueDead:    {},
		},
		depths: make(map[string]int64),
	}
}

func (m *memStore) enqueue(q string, msgs ...*worker.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queues[q] = append(m.queues[q], msgs...)
}

func (m *memStore) ReadMessage(_ context.Context, q string) (*worker.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queues[q]) == 0 {
		return nil, nil
	}
	msg := m.queues[q][0]
	m.queues[q] = m.queues[q][1:]
	return msg, nil
}

func (m *memStore) DeleteMessage(_ context.Context, _ string, msgID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, msgID)
	return nil
}

func (m *memStore) ArchiveToDLQ(_ context.Context, _ *worker.Message, _ string) error { return nil }

func (m *memStore) QueueDepth(_ context.Context, q string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.depths[q], nil
}

func (m *memStore) Notify(_ context.Context, _, _ string) error { return nil }

func (m *memStore) IncrRetry(_ context.Context, _ string, _ time.Duration) error { return nil }
