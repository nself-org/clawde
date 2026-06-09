// lexical.go — LexicalRetriever: tsvector + ts_rank_cd full-text search.
//
// Purpose: Execute Postgres full-text search against clawde_chunks.content_tsv
//          (GIN-indexed, migration 0085). Supports plain queries (plainto_tsquery)
//          and queries with boolean operators (websearch_to_tsquery).
// Inputs:  DBQuerier (pgx in prod / stub in tests), workspace UUID, query string,
//          topK int.
// Outputs: []ScoredChunk ordered ts_rank_cd descending.
// Constraints: File ≤500 lines. Uses content_tsv — never ts_body or chunk_tsv.
//              No live PG in unit tests (skip-with-reason).
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.LexicalRetriever.
package retrieval

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

// LexicalRetriever queries clawde_chunks via Postgres tsvector full-text search.
//
// Purpose: Provide the lexical lane of the hybrid retrieval pipeline.
//          ts_rank_cd with normalization flag 32 (divides by document length)
//          produces comparable scores across short and long chunks.
// Inputs:  DBQuerier, workspace UUID (for RLS), query string, topK.
// Outputs: []ScoredChunk desc by ts_rank_cd; error on query failure.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.LexicalRetriever.
type LexicalRetriever struct {
	db lanes.DBQuerier
}

// NewLexicalRetriever constructs a LexicalRetriever backed by the provided DB.
func NewLexicalRetriever(db lanes.DBQuerier) *LexicalRetriever {
	return &LexicalRetriever{db: db}
}

// Query executes a tsvector full-text search against content_tsv.
//
// Query parser selection:
//   - If the query contains boolean operators (AND, OR, NOT, quotes) use
//     websearch_to_tsquery so the intent is preserved.
//   - Otherwise use plainto_tsquery which is forgiving of natural-language input.
//
// RLS: SET LOCAL app.workspace_id before the SELECT.
// Score: ts_rank_cd(content_tsv, tsq, 32) — normalization=32 divides by document
// length, making scores comparable across chunk sizes.
func (r *LexicalRetriever) Query(
	ctx context.Context,
	workspaceID uuid.UUID,
	query string,
	topK int,
) ([]ScoredChunk, error) {
	// Set RLS GUC.
	if err := r.db.Exec(ctx, "SET LOCAL app.workspace_id = $1", workspaceID.String()); err != nil {
		return nil, fmt.Errorf("lexical: set workspace_id GUC: %w", err)
	}

	// Choose the tsquery function based on query content.
	tsqFn := lexicalQueryParser(query)

	// ts_rank_cd normalization=32: divide rank by (1 + log(document length)).
	// This compensates for longer chunks having more term matches by chance.
	sql := fmt.Sprintf(`
SELECT id,
       content,
       file_path,
       ts_rank_cd(content_tsv, %s('english', $1), 32) AS score
FROM   clawde_chunks
WHERE  content_tsv @@ %s('english', $1)
  AND  workspace_id = $2
ORDER  BY score DESC
LIMIT  $3`, tsqFn, tsqFn)

	rows, err := r.db.Query(ctx, sql, query, workspaceID, topK)
	if err != nil {
		return nil, fmt.Errorf("lexical: query: %w", err)
	}
	defer rows.Close()

	return scanScoredChunks(rows, "lexical")
}

// lexicalQueryParser picks the Postgres tsquery parser for the given query string.
// websearch_to_tsquery handles double-quoted phrases, OR, and - negation.
// plainto_tsquery is used for plain natural-language queries.
func lexicalQueryParser(query string) string {
	q := strings.ToUpper(query)
	if strings.Contains(q, " AND ") ||
		strings.Contains(q, " OR ") ||
		strings.Contains(q, " NOT ") ||
		strings.Contains(query, `"`) ||
		strings.Contains(query, " -") {
		return "websearch_to_tsquery"
	}
	return "plainto_tsquery"
}
