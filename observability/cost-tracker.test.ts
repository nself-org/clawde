/**
 * Purpose: Unit tests for cost-tracker.ts — verifies computeCost reads rates from cost-rates.json
 *   and that computed values equal (token_count / 1000) * rate[model][direction].
 * Tests assert values against what is IN cost-rates.json (read the file), not hardcoded constants.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
 */

import * as fs from "fs";
import * as path from "path";
import { computeCost, getDailyCostByTenant, getRatesForModel, loadCostRates } from "./cost-tracker";
import type { TraceRecord } from "./types";

// Read rates directly from cost-rates.json so tests assert against file contents
const RATES_PATH = path.join(__dirname, "cost-rates.json");
const RATES = JSON.parse(fs.readFileSync(RATES_PATH, "utf-8"));

function makeTrace(overrides: Partial<TraceRecord>): TraceRecord {
  return {
    trace_id: "trace-test-001",
    tenant_id: "tenant-abc",
    tool_id: "key-001",
    stage: "generate",
    latency_ms: 120,
    token_count: 1000,
    cost_usd: 0,
    model_id: "claude-haiku-4-5",
    cache_hit: false,
    error_code: null,
    redacted: false,
    ...overrides,
  };
}

describe("loadCostRates", () => {
  it("loads cost-rates.json successfully", () => {
    const rates = loadCostRates();
    expect(rates).toBeDefined();
    expect(typeof rates["claude-haiku-4-5"]).toBe("object");
  });

  it("contains the three required model keys", () => {
    const rates = loadCostRates();
    expect(rates["claude-sonnet-4-6"]).toBeDefined();
    expect(rates["claude-haiku-4-5"]).toBeDefined();
    expect(rates["text-embedding-3-small"]).toBeDefined();
  });

  it("each model entry has input and output keys", () => {
    const rates = loadCostRates();
    for (const modelId of ["claude-sonnet-4-6", "claude-haiku-4-5", "text-embedding-3-small"]) {
      const entry = rates[modelId] as Record<string, number>;
      expect(typeof entry.input).toBe("number");
      expect(typeof entry.output).toBe("number");
    }
  });
});

describe("computeCost — reads rates from cost-rates.json only", () => {
  it("claude-haiku-4-5 generate: high token count", () => {
    const tokenCount = 5000;
    const expectedRate = (RATES["claude-haiku-4-5"] as Record<string, number>).output;
    const expected = (tokenCount / 1000) * expectedRate;
    const trace = makeTrace({ model_id: "claude-haiku-4-5", stage: "generate", token_count: tokenCount, cache_hit: false });
    expect(computeCost(trace)).toBeCloseTo(expected, 8);
  });

  it("claude-haiku-4-5 generate: low token count", () => {
    const tokenCount = 100;
    const expectedRate = (RATES["claude-haiku-4-5"] as Record<string, number>).output;
    const expected = (tokenCount / 1000) * expectedRate;
    const trace = makeTrace({ model_id: "claude-haiku-4-5", stage: "generate", token_count: tokenCount, cache_hit: false });
    expect(computeCost(trace)).toBeCloseTo(expected, 8);
  });

  it("claude-sonnet-4-6 generate: uses output rate from file", () => {
    const tokenCount = 2000;
    const expectedRate = (RATES["claude-sonnet-4-6"] as Record<string, number>).output;
    const expected = (tokenCount / 1000) * expectedRate;
    const trace = makeTrace({ model_id: "claude-sonnet-4-6", stage: "generate", token_count: tokenCount, cache_hit: false });
    expect(computeCost(trace)).toBeCloseTo(expected, 8);
  });

  it("text-embedding-3-small embed: uses input rate from file", () => {
    const tokenCount = 500;
    const expectedRate = (RATES["text-embedding-3-small"] as Record<string, number>).input;
    const expected = (tokenCount / 1000) * expectedRate;
    const trace = makeTrace({ model_id: "text-embedding-3-small", stage: "embed", token_count: tokenCount, cache_hit: false });
    expect(computeCost(trace)).toBeCloseTo(expected, 8);
  });

  it("cache_hit=true uses cache_read rate from file", () => {
    const tokenCount = 1000;
    const expectedRate = (RATES["claude-haiku-4-5"] as Record<string, number>).cache_read;
    const expected = (tokenCount / 1000) * expectedRate;
    const trace = makeTrace({ model_id: "claude-haiku-4-5", stage: "generate", token_count: tokenCount, cache_hit: true });
    expect(computeCost(trace)).toBeCloseTo(expected, 8);
  });

  it("returns 0 for null model_id (non-model policy stage)", () => {
    const trace = makeTrace({ model_id: null, stage: "policy", token_count: 0 });
    expect(computeCost(trace)).toBe(0);
  });

  it("returns 0 for unknown model_id", () => {
    const trace = makeTrace({ model_id: "unknown-model-xyz", stage: "generate", token_count: 500 });
    expect(computeCost(trace)).toBe(0);
  });
});

describe("getDailyCostByTenant", () => {
  it("aggregates multiple traces by tenant, model, and date", () => {
    const traces: TraceRecord[] = [
      makeTrace({ tenant_id: "tenant-A", model_id: "claude-haiku-4-5", token_count: 1000, cost_usd: 0.004 }),
      makeTrace({ tenant_id: "tenant-A", model_id: "claude-haiku-4-5", token_count: 500, cost_usd: 0.002 }),
      makeTrace({ tenant_id: "tenant-B", model_id: "claude-sonnet-4-6", token_count: 2000, cost_usd: 0.03 }),
    ];
    const result = getDailyCostByTenant(traces);
    expect(result.length).toBeGreaterThanOrEqual(2);

    const tenantA = result.find((r) => r.tenant_id === "tenant-A" && r.model_id === "claude-haiku-4-5");
    expect(tenantA).toBeDefined();
    expect(tenantA!.trace_count).toBe(2);
    expect(tenantA!.total_token_count).toBe(1500);
    expect(tenantA!.total_cost_usd).toBeCloseTo(0.006, 6);
  });

  it("returns empty array for empty trace input", () => {
    expect(getDailyCostByTenant([])).toEqual([]);
  });
});
