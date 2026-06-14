# clawde-intelligence

Go gRPC + REST gateway for the ClawDE intelligence backend.

Provides: LLM routing, embedding, reranking, static analysis dispatch, document ingestion, and the pgmq worker pool.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `CLAWDE_PG_DSN` | Optional | Postgres connection string (`postgres://user:pass@host/db`). When set, starts the pgmq worker pool to drain embed, analyze, and ingest queues. When unset, pool is skipped with a logged warning — binary still starts. |
| `TAILSCALE_AUTHKEY` | Optional | Tailscale auth key. When set, a second gRPC listener is started on the Tailscale mesh interface (`:8090`). |
| `CLAWDE_TAILSCALE_HOSTNAME` | Optional | Tailscale node hostname. Default: `clawde-intelligence`. |
| `CLAWDE_ENV` | Optional | Set to `production` to disable gRPC reflection. |
| `CLAWDE_HMAC_SECRET` | Optional | HMAC-SHA256 secret for request authentication. |
| `CLAWDE_WORKER_N` | Optional | Number of worker goroutines per queue. Default: 10. |
| `CLAWDE_JOB_MAX_RETRIES` | Optional | Max retries before a job is archived to DLQ. Default: 3. |

## Worker Pool

When `CLAWDE_PG_DSN` is set, `cmd/server` starts a `worker.Pool` alongside the gRPC server. The pool drains three pgmq queues:

| Queue | Handler | Notes |
|---|---|---|
| `clawde_embed_queue` | Stub (logs + acks) | TODO: wire to embedding pipeline (P2-E1-W2-S3+) |
| `clawde_analyze_queue` | `staticanalysis.Runner.Handle` | Runs Semgrep + CodeQL on workspace repos |
| `clawde_ingest_queue` | `docs.KBIngestor.IngestDocURL` | Fetches + chunks + enqueues external documentation URLs |

The pool stores/reads jobs from the `clawde_job` table (migration 0086). No external pgmq Go client is needed — the `pgxQueueStore` adapter (`internal/worker/pgx_store.go`) implements the `QueueStore` interface directly via pgx and raw SQL.

On `SIGINT`/`SIGTERM`, `Pool.Stop()` is called before `gRPC.GracefulStop()` to drain in-flight jobs before closing connections.

## Proto Generation

The gRPC stubs in `internal/server/` are generated from `proto/gateway.proto` using protoc. To regenerate after editing the proto:

```bash
# Install plugins (once)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

# Regenerate stubs
make proto
```

The CI `intelligence` job runs `make proto && git diff --exit-code` to fail on proto drift (generated files not committed after proto changes).

## Integration Tests

The `test/integration/` package contains end-to-end tests that exercise the full wired organism: gRPC server → compiler → retrieval → rerank → worker pool → Temporal agent workflow.

Tests require a live Postgres instance with the pgmq extension. They are gated behind the `integration` build tag and skip gracefully when `CLAWDE_TEST_PG_DSN` is unset.

| Test | What it proves |
|---|---|
| `TestOrganismCompileContext` | Full gRPC → compiler → retriever → Enriched=true |
| `TestOrganismEmbedJobDrains` | Embed job queued → worker.Pool drains it within 5s |
| `TestOrganismRouterFailover` | First provider 429 → response from second provider |
| `TestOrganismDocIngest` | IngestDocURL RPC path is wired and reachable |
| `TestOrganismAgentWorkflow` | AgentRunWorkflow terminates ≤3 turns (Temporal test env) |

```bash
# Run with live Postgres (all 5 PASS)
CLAWDE_TEST_PG_DSN=postgres://user:pass@localhost/clawde \
  go test -tags integration -timeout 300s -v ./test/integration/...

# Run without Postgres (all 5 SKIP, 0 FAIL)
CLAWDE_TEST_PG_DSN="" go test -tags integration -v ./test/integration/...

# Compile check only (no build tag — no tests run)
go test ./test/integration/...
```

## Building

```bash
go build ./...
go test ./...
```

## Running

```bash
# Without worker pool (loopback gRPC only)
./server

# With worker pool
CLAWDE_PG_DSN=postgres://user:pass@localhost/clawde ./server

# With Tailscale mesh listener
TAILSCALE_AUTHKEY=tskey-auth-... CLAWDE_PG_DSN=postgres://... ./server
```

## Structure

```text
cmd/
  server/         # gRPC + REST server + worker pool entry point
  mcp-server/     # MCP stdio adapter (Claude Code integration)
  eval-gate/      # Eval gate binary
internal/
  worker/         # pgmq worker pool (worker.go, pgx_store.go, handlers.go)
  staticanalysis/ # Semgrep + CodeQL runner
  docs/           # KB ingestion (IngestDocURL)
  gateway/        # LLM provider routing
  server/         # gRPC + REST server setup
migrations/       # Postgres migrations (0001–0091+)
```
