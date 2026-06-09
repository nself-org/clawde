/**
 * Purpose: Per-stage cost computation and daily aggregate query for the nClaw AI gateway observability layer.
 * Inputs: TraceRecord (stage, model_id, token_count, cache_hit); ag_traces rows for aggregate queries
 * Outputs: cost_usd (number); DailyCostAggregate[] for dashboard queries
 * Constraints: computeCost reads rates ONLY from cost-rates.json — NO hardcoded values.
 *   Unit tests MUST assert values against what is written in cost-rates.json, not separate constants.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals; REGISTRY-SERVICES.md observability entry
 */

import * as fs from "fs";
import * as path from "path";
import type { TraceRecord, DailyCostAggregate } from "./types";

// ─── Rate table loading ────────────────────────────────────────────────────────

interface ModelRates {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
}

interface CostRates {
  _comment: string;
  _source_date: string;
  [modelId: string]: ModelRates | string;
}

let _ratesCache: CostRates | null = null;

/**
 * Loads cost-rates.json from the observability directory.
 * Cached after first load. Reads from disk on first call.
 *
 * @returns Parsed cost rates object
 */
export function loadCostRates(): CostRates {
  if (_ratesCache) return _ratesCache;
  const ratesPath = path.join(__dirname, "cost-rates.json");
  const raw = fs.readFileSync(ratesPath, "utf-8");
  _ratesCache = JSON.parse(raw) as CostRates;
  return _ratesCache;
}

/**
 * Returns per-1k-token rates for a given model ID.
 * Returns null if the model is not found in cost-rates.json.
 *
 * @param modelId - Model identifier (e.g. "claude-haiku-4-5")
 */
export function getRatesForModel(modelId: string): ModelRates | null {
  const rates = loadCostRates();
  const entry = rates[modelId];
  if (!entry || typeof entry === "string") return null;
  return entry as ModelRates;
}

// ─── Cost computation ─────────────────────────────────────────────────────────

/**
 * Computes cost in USD for a single trace stage.
 * Formula: cost_usd = (token_count / 1000) * rate[model][direction]
 *
 * Direction selection:
 *   - cache_hit=true  → "cache_read" rate (reads cached tokens)
 *   - stage=embed     → "input" rate (embeddings are input-only billing)
 *   - stage=generate  → "output" rate (generation output is the main cost)
 *   - otherwise       → "input" rate
 *
 * Returns 0 if model_id is null (non-model stages such as policy gate).
 * Returns 0 if model_id is not in cost-rates.json (unknown model; log a warning).
 *
 * @param stage - TraceRecord object (uses model_id, token_count, cache_hit, stage)
 * @returns cost_usd as a float
 */
export function computeCost(stage: TraceRecord): number {
  if (!stage.model_id) return 0;
  const rates = getRatesForModel(stage.model_id);
  if (!rates) {
    // Unknown model — return 0 but emit a warning so callers can track
    console.warn(
      `[cost-tracker] Unknown model_id '${stage.model_id}' — cost set to 0. Add to cost-rates.json.`
    );
    return 0;
  }

  let ratePerKTokens: number;
  if (stage.cache_hit) {
    ratePerKTokens = rates.cache_read;
  } else if (stage.stage === "generate") {
    ratePerKTokens = rates.output;
  } else {
    // embed, retrieve, rerank, policy — use input rate
    ratePerKTokens = rates.input;
  }

  return (stage.token_count / 1000) * ratePerKTokens;
}

// ─── Daily aggregate query ────────────────────────────────────────────────────

/**
 * Aggregates per-tenant, per-model daily cost from a list of trace records.
 * In production this query runs against ag_traces via Drizzle/Hasura.
 * This in-process implementation supports unit testing and offline evaluation.
 *
 * @param traces - Array of TraceRecord objects
 * @returns Array of DailyCostAggregate rows grouped by (tenant_id, model_id, date)
 */
export function getDailyCostByTenant(
  traces: TraceRecord[]
): DailyCostAggregate[] {
  // Group key: "tenant_id::model_id::YYYY-MM-DD"
  const groups = new Map<
    string,
    {
      tenant_id: string;
      model_id: string | null;
      date: string;
      total_cost_usd: number;
      total_token_count: number;
      trace_count: number;
    }
  >();

  for (const t of traces) {
    // Derive date from trace_id timestamp — in production, traces have a created_at field.
    // For offline use we default to today.
    const date = new Date().toISOString().slice(0, 10);
    const key = `${t.tenant_id}::${t.model_id ?? "_none"}::${date}`;

    if (!groups.has(key)) {
      groups.set(key, {
        tenant_id: t.tenant_id,
        model_id: t.model_id,
        date,
        total_cost_usd: 0,
        total_token_count: 0,
        trace_count: 0,
      });
    }

    const g = groups.get(key)!;
    g.total_cost_usd += t.cost_usd;
    g.total_token_count += t.token_count;
    g.trace_count += 1;
  }

  return Array.from(groups.values());
}
