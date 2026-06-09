# ClawDE AI Gateway Observability

This page covers the trace architecture, cost tracking, and privacy redaction for the nClaw AI gateway pipeline.

## Overview

Every stage of the nClaw semantic retrieval and generation pipeline emits a `TraceRecord` before the result is returned. These records are persisted to `ag_traces` and used for cost tracking, latency analysis, and quality monitoring.

## Pipeline stages

| Stage | Description | Model used |
|---|---|---|
| `embed` | BGE-M3 text embedding via TEI sidecar | `text-embedding-3-small` or `bge-m3` |
| `retrieve` | pgvector ANN search + tsvector BM25 lexical search | None |
| `rerank` | BGE Reranker v2-m3 cross-encoder | `bge-reranker-v2-m3` |
| `generate` | LLM completion (Claude or local model) | `claude-haiku-4-5`, `claude-sonnet-4-6`, etc. |
| `policy` | Rate limit + quota + content-guard check | None |

## TraceRecord interface

Defined in `observability/types.ts`:

```typescript
interface TraceRecord {
  trace_id: string;        // UUID v4
  tenant_id: string;       // Hasura X-Hasura-Tenant-Id
  tool_id: string;         // ag_key_audit.key_id
  stage: TraceStage;       // embed | retrieve | rerank | generate | policy
  latency_ms: number;
  token_count: number;
  cost_usd: number;
  model_id: string | null;
  cache_hit: boolean;
  error_code: string | null;
  redacted: boolean;
}
```

## Cost tracking

Per-model rates are defined in `observability/cost-rates.json` (source-pinned to Anthropic pricing 2026-05-29). Rates are in USD per 1,000 tokens.

The `computeCost(stage: TraceRecord)` function in `cost-tracker.ts` reads rates exclusively from this file — no hardcoded values.

Daily cost aggregates per tenant per model are available via `getDailyCostByTenant(traces)`.

## Privacy redaction

All string fields in a TraceRecord are scanned before persistence using `observability/redact.ts`. Four PII patterns are applied:

| Pattern | Token | Example |
|---|---|---|
| Email address | `[EMAIL]` | `user@example.com` |
| US phone number | `[PHONE]` | `(555) 123-4567` |
| US Social Security Number | `[SSN]` | `123-45-6789` |
| Name (title + bigram) | `[NAME]` | `Dr. John Smith` |

When any pattern matches, `redacted=true` is set on the TraceRecord. The original value is never persisted.

## Database schema

Traces are stored in `ag_traces` (migration 058). See `observability/migration.ts` for Drizzle-compatible up/down methods and `apps/daemon/src/storage/migrations/058_ai_gateway_traces.sql` for the raw SQL.

Key indexes:
- `(tenant_id, model_id, date(created_at))` — for daily cost aggregates
- `(tool_id)` — for joining with ag_key_audit
- `(stage, created_at)` — for latency analysis by stage

## Pass thresholds (online monitoring)

| Metric | Threshold |
|---|---|
| Search latency p95 | ≤ 500ms |
| Cost per query | ≤ $0.002 at default model tier |

See `observability/types.ts` `EvalPassResult` for the full threshold definition used by both offline eval and online monitors.
