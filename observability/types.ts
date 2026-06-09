/**
 * Purpose: TypeScript interface definitions for the nClaw AI gateway observability trace records.
 * Each semantic retrieval + AI generation pipeline stage emits a TraceRecord before persistence.
 * Inputs: Per-stage instrumentation points (embed, retrieve, rerank, generate, policy)
 * Outputs: TraceRecord objects written to ag_traces (or ag_key_audit trace fields)
 * Constraints: No runtime I/O. Pure type definitions + branded type helpers.
 *   PII fields (tool input payload, error messages) must be redacted via redact.ts BEFORE creating a TraceRecord.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals; REGISTRY-SERVICES.md observability entry
 */

/**
 * Pipeline stage identifiers for the nClaw semantic retrieval + generation path.
 *   embed   — BGE-M3 text embedding via TEI sidecar
 *   retrieve — pgvector ANN search (dense) + tsvector BM25 (lexical)
 *   rerank  — BGE Reranker v2-m3 TEI cross-encoder
 *   generate — LLM completion (Claude / local model via vLLM / Ollama)
 *   policy  — PolicyEngine rate-limit + quota + content-guard check
 */
export type TraceStage =
  | "embed"
  | "retrieve"
  | "rerank"
  | "generate"
  | "policy";

/**
 * A single trace record capturing one pipeline stage execution.
 * All fields are required. PII in any string field MUST be redacted before this record is persisted.
 *
 * @field trace_id       - UUID v4 uniquely identifying this stage trace
 * @field tenant_id      - Tenant UUID from Hasura X-Hasura-Tenant-Id header
 * @field tool_id        - ag_key_audit.key_id — the MCP tool key used for this request
 * @field stage          - Which pipeline stage produced this record
 * @field latency_ms     - Wall-clock duration of the stage in milliseconds
 * @field token_count    - Total tokens consumed (input + output for generate; embeddings for embed)
 * @field cost_usd       - Computed cost in USD; use computeCost() from cost-tracker.ts
 * @field model_id       - Model identifier (e.g. "claude-haiku-4-5", "bge-m3"); null for non-model stages
 * @field cache_hit      - Whether the result was served from a cache (prompt cache, embedding cache)
 * @field error_code     - Error code if the stage failed (e.g. "QUOTA_EXCEEDED"); null on success
 * @field redacted       - True if any PII pattern was matched and replaced in this record's payload fields
 */
export interface TraceRecord {
  trace_id: string;
  tenant_id: string;
  tool_id: string;
  stage: TraceStage;
  latency_ms: number;
  token_count: number;
  cost_usd: number;
  model_id: string | null;
  cache_hit: boolean;
  error_code: string | null;
  redacted: boolean;
}

/**
 * Partial TraceRecord for in-flight stage construction before latency is known.
 * Useful for timing via start/end pattern:
 *   const partial = startTrace({ ... });
 *   // ... stage execution ...
 *   const record = completeTrace(partial, { latency_ms, token_count, cost_usd, ... });
 */
export type PartialTraceRecord = Omit<TraceRecord, "latency_ms" | "cost_usd">;

/**
 * Aggregate row returned by getDailyCostByTenant() in cost-tracker.ts.
 */
export interface DailyCostAggregate {
  tenant_id: string;
  model_id: string | null;
  date: string; // ISO 8601 date string "YYYY-MM-DD"
  total_cost_usd: number;
  total_token_count: number;
  trace_count: number;
}

/**
 * Eval metric pass/fail result — used by the regression gate and online monitors.
 */
export interface EvalPassResult {
  mrr: number;
  ndcg_at_10: number;
  precision_at_5: number;
  latency_p95_ms: number | null;
  cost_per_query_usd: number | null;
  passed: boolean;
  failures: string[];
}
