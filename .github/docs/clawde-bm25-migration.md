# ClawDE BM25 Migration Guide

This document describes the three-phase migration from the tsvector lexical baseline
to ParadeDB BM25 retrieval in clawde-intelligence.

## License Note

ParadeDB ([github.com/paradedb/paradedb](https://github.com/paradedb/paradedb)) is
licensed under **Apache 2.0**. This is compatible with ClawDE's MIT license and with
all commercial use by nSelf users.

---

## Phase V1 — Tsvector Baseline (default)

**Status:** Default. Active when `CLAWDE_BM25_ENABLED=false` (or not set).

The `TSVectorBM25Lane` uses Postgres's built-in `tsvector` + `ts_rank` against
`clawde_chunks.content_tsv` (a generated `tsvector` column, GIN-indexed since
migration 0085).

**Characteristics:**
- No external extension required — works on any Postgres 14+ instance.
- Good recall for exact-term queries; limited BM25 score calibration.
- Always available; CI passes without ParadeDB installed.

**Activation:** Nothing to do — this is the default path.

---

## Phase V2 — A/B Comparison (optional)

**Status:** Available. Activate with `CLAWDE_BM25_AB_MODE=true`.

In A/B mode the kernel queries **both** lanes per request and logs comparative
top-10 results to `clawde_lane_ab_log` (migration 0087). This enables offline
recall analysis before committing to ParadeDB.

**Steps to enable A/B mode:**
1. Install ParadeDB on your Postgres instance
   (`pg_bm25` extension: `CREATE EXTENSION pg_bm25;`).
2. Create the BM25 index on `clawde_chunks`:
   ```sql
   CREATE INDEX clawde_chunks_bm25
       ON clawde_chunks
       USING bm25 (content)
       WITH (key_field='id');
   ```
   This is a non-breaking addition — the existing schema is unchanged.
3. Set `CLAWDE_BM25_AB_MODE=true` in the environment.
4. Run queries normally. Top-10 from both lanes accumulates in
   `clawde_lane_ab_log` for analysis.

**Analyzing results:**
```sql
SELECT query,
       jsonb_array_length(tsvector_top10) AS ts_count,
       jsonb_array_length(bm25_top10)     AS bm25_count,
       created_at
FROM   clawde_lane_ab_log
ORDER  BY created_at DESC
LIMIT  50;
```

---

## Phase V3 — ParadeDB BM25 (promoted)

**Status:** Available. Activate with `CLAWDE_BM25_ENABLED=true`.

When promoted, the kernel routes requests to `ParadeDBBM25Lane`. On any error
(e.g., if the extension becomes temporarily unavailable), it automatically falls
back to `TSVectorBM25Lane` and emits a `bm25_fallback` OTel log event.

**The swap is non-breaking:** no schema migration is needed because both lanes
query the same `clawde_chunks` table. The BM25 index is an additive index, not
a table change.

**Steps to promote:**
1. Complete V2 A/B analysis and confirm recall improvement.
2. Ensure `CREATE INDEX clawde_chunks_bm25 ...` is applied (see V2, step 2).
3. Switch `CLAWDE_BM25_ENABLED=true` (and optionally set `CLAWDE_BM25_AB_MODE=false`
   to stop logging once satisfied with BM25 quality).
4. Monitor the `bm25_fallback` OTel event rate — a spike indicates ParadeDB
   availability issues; the tsvector fallback keeps the service healthy.

**Fallback behaviour:**
- ParadeDB query error → tsvector results returned → `bm25_fallback` event logged.
- No user-visible degradation; only a log entry is emitted.

---

## Configuration Reference

| Env var | Type | Default | Description |
|---|---|---|---|
| `CLAWDE_BM25_ENABLED` | bool | `false` | Route to ParadeDB BM25 lane (with tsvector fallback) |
| `CLAWDE_BM25_AB_MODE` | bool | `false` | Query both lanes; log comparative results to `clawde_lane_ab_log` |

Valid truthy values: `1`, `true`, `yes` (case-insensitive).

---

## Migration Table

| Migration | File | Purpose |
|---|---|---|
| 0085 | `0085_indexes_rls_cron.sql` | GIN index on `content_tsv` — required for V1 tsvector baseline |
| 0087 | `0087_ab_log.sql` | `clawde_lane_ab_log` table + RLS + optional pg_bm25 VACUUM cron |

The BM25 index (`CREATE INDEX ... USING bm25`) is NOT managed by a migration file.
It is an operational step performed when enabling V2/V3 because it requires ParadeDB
to be installed first.
