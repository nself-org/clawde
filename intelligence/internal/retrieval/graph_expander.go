// graph_expander.go — BFS graph expansion over clawde_graph_edges.
//
// Purpose: Given a seed set of chunk IDs (top-10 from RRF), walk
//          clawde_graph_edges up to maxHops=2 hops to surface related chunks
//          the seed set may have missed (call-graph neighbours, import chains).
// Inputs:  GraphQuerier (DB seam), workspace UUID, seed chunk IDs, maxHops int.
// Outputs: []uuid.UUID — neighbour chunk IDs discovered by BFS (deduped, no seeds).
// Constraints: File ≤500 lines. Visited-set cycle guard prevents infinite loops.
//              No live PG in unit tests (skip-with-reason).
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.GraphExpander.
package retrieval

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// GraphQuerier is the minimal DB interface for BFS edge traversal.
// The real implementation wraps pgx; tests inject a stub.
//
// Purpose: Seam so graph expansion tests run without a live Postgres instance.
// Inputs:  ctx, workspace UUID, source chunk IDs (batch lookup).
// Outputs: destination chunk IDs reachable in one hop.
type GraphQuerier interface {
	// NeighbourChunks returns the dst_chunk_ids reachable from the given src
	// chunk IDs in one hop through clawde_graph_edges within workspaceID.
	NeighbourChunks(ctx context.Context, workspaceID uuid.UUID, srcs []uuid.UUID) ([]uuid.UUID, error)
}

// GraphExpander performs BFS over clawde_graph_edges to find related chunks.
//
// Purpose: Expand the dense+lexical seed set with call-graph neighbours so the
//          final context window includes callers and callees of the top-10 chunks.
// Inputs:  GraphQuerier, workspace UUID, seed IDs, maxHops (2 recommended).
// Outputs: newly discovered chunk IDs (union of BFS frontier, seeds excluded).
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.GraphExpander.
type GraphExpander struct {
	db lanes.DBQuerier
}

// NewGraphExpander constructs a GraphExpander using the provided DBQuerier.
// The DBQuerier is also used as a GraphQuerier via pgxGraphQuerier adapter in
// production; tests inject a stub GraphQuerier directly via ExpandWithQuerier.
func NewGraphExpander(db lanes.DBQuerier) *GraphExpander {
	return &GraphExpander{db: db}
}

// Expand runs BFS from seedIDs up to maxHops hops.
// Returns newly discovered chunk IDs (excluding the seeds themselves).
// Uses pgxGraphQuerier to adapt the DBQuerier to GraphQuerier.
func (e *GraphExpander) Expand(
	ctx context.Context,
	workspaceID uuid.UUID,
	seedIDs []uuid.UUID,
	maxHops int,
) ([]uuid.UUID, error) {
	gq := &pgxGraphQuerier{db: e.db}
	return ExpandWithQuerier(ctx, gq, workspaceID, seedIDs, maxHops)
}

// ExpandWithQuerier runs BFS using the provided GraphQuerier.
// This is the testable entry point; production callers use Expand.
func ExpandWithQuerier(
	ctx context.Context,
	gq GraphQuerier,
	workspaceID uuid.UUID,
	seedIDs []uuid.UUID,
	maxHops int,
) ([]uuid.UUID, error) {
	if maxHops <= 0 || len(seedIDs) == 0 {
		return nil, nil
	}

	visited := make(map[uuid.UUID]bool, len(seedIDs)*4)
	for _, id := range seedIDs {
		visited[id] = true
	}

	frontier := make([]uuid.UUID, len(seedIDs))
	copy(frontier, seedIDs)

	var discovered []uuid.UUID

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		neighbours, err := gq.NeighbourChunks(ctx, workspaceID, frontier)
		if err != nil {
			return nil, fmt.Errorf("graph expand hop %d: %w", hop+1, err)
		}

		var nextFrontier []uuid.UUID
		for _, nb := range neighbours {
			if !visited[nb] {
				visited[nb] = true
				discovered = append(discovered, nb)
				nextFrontier = append(nextFrontier, nb)
			}
		}
		frontier = nextFrontier
	}

	return discovered, nil
}

// ── pgxGraphQuerier ───────────────────────────────────────────────────────────

// pgxGraphQuerier adapts lanes.DBQuerier to GraphQuerier using
// clawde_graph_edges (dst_chunk_id column).
type pgxGraphQuerier struct {
	db lanes.DBQuerier
}

// NeighbourChunks queries clawde_graph_edges for direct neighbours of srcs.
// The query uses ANY($1) to batch-fetch all neighbours in one round-trip.
func (q *pgxGraphQuerier) NeighbourChunks(
	ctx context.Context,
	workspaceID uuid.UUID,
	srcs []uuid.UUID,
) ([]uuid.UUID, error) {
	if len(srcs) == 0 {
		return nil, nil
	}

	// Set RLS GUC so row-level security applies.
	if err := q.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("graph: set workspace_id GUC: %w", err)
	}

	// Convert []uuid.UUID to []string for ANY($1) parameter.
	srcStrs := make([]string, len(srcs))
	for i, id := range srcs {
		srcStrs[i] = id.String()
	}

	const sql = `
SELECT DISTINCT dst_chunk_id
FROM   clawde_graph_edges
WHERE  workspace_id = $1
  AND  src_chunk_id = ANY($2::uuid[])`

	rows, err := q.db.Query(ctx, sql, workspaceID, srcStrs)
	if err != nil {
		return nil, fmt.Errorf("graph: neighbour query: %w", err)
	}
	defer rows.Close()

	var result []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("graph: scan neighbour id: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: rows error: %w", err)
	}
	return result, nil
}
