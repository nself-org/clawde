/**
 * Purpose: Drizzle ORM schema companion for clawde knowledge tables.
 *          Mirrors the DDL in migrations 0082-0085 exactly (zero drift).
 * Inputs:  Postgres 16 + pgvector 0.7+
 * Outputs: Table definitions for clawde_workspaces, clawde_cost_ledger,
 *          clawde_chunks, clawde_eval_runs, clawde_symbols, clawde_graph_edges.
 * Constraints:
 *   - vector(1024) — ADR-005 BGE-M3 dimension.
 *   - tsvector column name: content_tsv (GENERATED ALWAYS AS STORED).
 *   - RLS isolation: app.workspace_id GUC (not hasura.user, not tenant_id).
 *   - workspace_id is UUID FK → clawde_workspaces on all clawde_* tables.
 *   - clawde_eval_runs is a STUB; full schema owned by migration 0089.
 * SPORT: REGISTRY-MIGRATIONS.md (0082-0085); REGISTRY-TABLES.md (all six tables).
 */

import {
  pgTable,
  uuid,
  text,
  integer,
  bigint,
  numeric,
  real,
  timestamp,
  json,
  customType,
  check,
} from 'drizzle-orm/pg-core';
import { sql } from 'drizzle-orm';

// ── Custom pgvector type ───────────────────────────────────────────────────────
// drizzle-orm does not ship a native vector() type yet; define it here.
const vector = customType<{ data: number[]; driverData: string }>({
  dataType(config?: { dimensions?: number }) {
    return config?.dimensions ? `vector(${config.dimensions})` : 'vector';
  },
});

// ── Custom tsvector type ───────────────────────────────────────────────────────
const tsvector = customType<{ data: string; driverData: string }>({
  dataType() {
    return 'tsvector';
  },
});

// ── clawde_workspaces ─────────────────────────────────────────────────────────
export const clawdeWorkspaces = pgTable('clawde_workspaces', {
  id:        uuid('id').primaryKey().defaultRandom(),
  name:      text('name').notNull().default(''),
  ownerId:   text('owner_id').notNull().default(''),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
});

// ── clawde_cost_ledger ────────────────────────────────────────────────────────
export const clawdeCostLedger = pgTable('clawde_cost_ledger', {
  id:               uuid('id').primaryKey().defaultRandom(),
  workspaceId:      uuid('workspace_id').notNull().references(() => clawdeWorkspaces.id, { onDelete: 'cascade' }),
  provider:         text('provider').notNull(),
  model:            text('model').notNull(),
  lane:             text('lane').notNull(),
  userId:           text('user_id').notNull().default(''),
  tokensIn:         integer('tokens_in').notNull().default(0),
  tokensOut:        integer('tokens_out').notNull().default(0),
  costUsdEstimate:  numeric('cost_usd_estimate', { precision: 18, scale: 8 }).notNull().default('0'),
  latencyMs:        bigint('latency_ms', { mode: 'number' }).notNull().default(0),
  createdAt:        timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

// ── clawde_chunks ─────────────────────────────────────────────────────────────
// content_tsv is GENERATED ALWAYS AS STORED in Postgres; Drizzle maps it as
// a computed column. The application never writes content_tsv directly.
export const clawdeChunks = pgTable('clawde_chunks', {
  id:          uuid('id').primaryKey().defaultRandom(),
  workspaceId: uuid('workspace_id').notNull().references(() => clawdeWorkspaces.id, { onDelete: 'cascade' }),
  content:     text('content').notNull(),
  // Dense embedding — 1024 dimensions (ADR-005 BGE-M3).
  embedding:   vector('embedding', { dimensions: 1024 }),
  // Full-text: CANONICAL NAME content_tsv. GENERATED ALWAYS AS STORED in DB.
  contentTsv:  tsvector('content_tsv').generatedAlwaysAs(
    sql`to_tsvector('english', content)`
  ),
  sparseVec:   json('sparse_vec'),   // SPLADE {term: weight} JSONB
  sourceType:  text('source_type').notNull().default(''),
  sourceRef:   text('source_ref').notNull().default(''),
  chunkIndex:  integer('chunk_index').notNull().default(0),
  contentHash: text('content_hash').notNull().default(''),
  metadata:    json('metadata'),
  createdAt:   timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  ttlDays:     integer('ttl_days').notNull().default(90),
});

// ── clawde_eval_runs (STUB) ───────────────────────────────────────────────────
// Minimal placeholder. Full schema (datasets, scores, comparisons) is owned
// by migration 0089 (W12-T04). Do NOT add columns here.
export const clawdeEvalRuns = pgTable('clawde_eval_runs', {
  id:          uuid('id').primaryKey().defaultRandom(),
  workspaceId: uuid('workspace_id').notNull().references(() => clawdeWorkspaces.id, { onDelete: 'cascade' }),
  name:        text('name').notNull().default(''),
  status:      text('status').notNull().default('pending'),
  createdAt:   timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
});

// ── clawde_symbols ────────────────────────────────────────────────────────────
export const clawdeSymbols = pgTable(
  'clawde_symbols',
  {
    id:          uuid('id').primaryKey().defaultRandom(),
    workspaceId: uuid('workspace_id').notNull().references(() => clawdeWorkspaces.id, { onDelete: 'cascade' }),
    name:        text('name').notNull(),
    kind:        text('kind').notNull(),
    filePath:    text('file_path').notNull().default(''),
    lineStart:   integer('line_start').notNull().default(0),
    lineEnd:     integer('line_end').notNull().default(0),
    signature:   text('signature'),
    doc:         text('doc'),
    createdAt:   timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => [
    check('kind_check', sql`${t.kind} IN ('function','class','method','type','const')`),
  ]
);

// ── clawde_graph_edges ────────────────────────────────────────────────────────
export const clawdeGraphEdges = pgTable(
  'clawde_graph_edges',
  {
    id:          uuid('id').primaryKey().defaultRandom(),
    workspaceId: uuid('workspace_id').notNull().references(() => clawdeWorkspaces.id, { onDelete: 'cascade' }),
    srcType:     text('src_type').notNull(),
    srcId:       uuid('src_id').notNull(),
    dstType:     text('dst_type').notNull(),
    dstId:       uuid('dst_id').notNull(),
    edgeKind:    text('edge_kind').notNull().default('calls'),
    weight:      real('weight').notNull().default(1.0),
    metadata:    json('metadata'),
    createdAt:   timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => [
    check('src_type_check', sql`${t.srcType} IN ('symbol','chunk')`),
    check('dst_type_check', sql`${t.dstType} IN ('symbol','chunk')`),
  ]
);
