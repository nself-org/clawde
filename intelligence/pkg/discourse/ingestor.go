// ingestor.go — orchestrate discourse ingestion end to end.
//
// Purpose:    Ingestor takes fetched GitHub discourse (issues, PRs, commits,
//             review comments), classifies them (FAST lane, ≥1 ppi:* tag, batch
//             ≤20), chunks them into clawde_chunks shapes (title/body/comment),
//             enqueues chunks onto clawde_embed_queue, and writes GraphRAG edges
//             (REFERENCES/MODIFIES/LINKS) into clawde_graph_edges.
// Inputs:     fetched discourse slices, workspace ID, known-symbol set, taxonomy.
// Outputs:    IngestResult counts; chunks enqueued + edges upserted via seams.
// Constraints: File ≤500 lines. ChunkSink and EdgeSink are interface seams; tests
//             inject stubs (DB tests skip-with-reason).
//
// SPORT: REGISTRY-FUNCTIONS.md → discourse.Ingestor.
package discourse

import (
	"context"
	"fmt"
)

// ChunkSink persists + enqueues discourse chunks. The production impl inserts
// into clawde_chunks and pushes onto clawde_embed_queue (pgmq); tests stub it.
type ChunkSink interface {
	// SaveChunks inserts chunks into clawde_chunks and enqueues them for embedding.
	// Returns the count successfully persisted.
	SaveChunks(ctx context.Context, chunks []Chunk) (int, error)
}

// EdgeSink upserts discourse graph edges into clawde_graph_edges (idempotent).
type EdgeSink interface {
	UpsertEdges(ctx context.Context, edges []Edge) error
}

// Ingestor wires the classifier, chunk sink, and edge sink.
type Ingestor struct {
	classifier *Classifier
	chunks     ChunkSink
	edges      EdgeSink
}

// NewIngestor constructs an Ingestor.
func NewIngestor(classifier *Classifier, chunks ChunkSink, edges EdgeSink) *Ingestor {
	return &Ingestor{classifier: classifier, chunks: chunks, edges: edges}
}

// IngestInput is the full discourse payload for one ingest pass.
type IngestInput struct {
	WorkspaceID    string
	Issues         []Issue
	PRs            []PR
	Commits        []Commit
	ReviewComments []ReviewComment
	KnownSymbols   map[string]bool // for REFERENCES edges
	Taxonomy       []string        // allowed ppi:* tags
}

// IngestResult reports what was ingested.
type IngestResult struct {
	ItemsClassified int
	ChunksEnqueued  int
	EdgesUpserted   int
}

// Ingest runs the full pipeline. Order: classify → chunk (tags attached) →
// enqueue → build+upsert edges. A nil classifier skips tagging (chunks still
// get the fallback tag at chunk time is not applied — tagging is classifier-only).
func (in *Ingestor) Ingest(ctx context.Context, input IngestInput) (*IngestResult, error) {
	if input.WorkspaceID == "" {
		return nil, fmt.Errorf("ingest: empty workspace_id")
	}
	res := &IngestResult{}

	// 1) Build classification items across all four source kinds.
	items := buildItems(input)
	tagByRef := map[string][]string{}
	if in.classifier != nil && len(items) > 0 {
		classified, err := in.classifier.BatchClassify(ctx, items, input.Taxonomy)
		if err != nil {
			return nil, fmt.Errorf("ingest: classify: %w", err)
		}
		res.ItemsClassified = len(classified)
		for _, it := range classified {
			tagByRef[tagKey(it.Kind, it.Ref)] = it.Tags
		}
	}

	// 2) Chunk every source item, attach tags, collect for enqueue.
	var allChunks []Chunk
	for _, is := range input.Issues {
		cs := ChunkIssue(input.WorkspaceID, is)
		attachTags(cs, tagByRef[tagKey(SourceIssue, itoa(is.Number))])
		allChunks = append(allChunks, cs...)
	}
	for _, pr := range input.PRs {
		cs := ChunkPR(input.WorkspaceID, pr)
		attachTags(cs, tagByRef[tagKey(SourcePR, itoa(pr.Number))])
		allChunks = append(allChunks, cs...)
	}
	for _, c := range input.Commits {
		cs := ChunkCommit(input.WorkspaceID, c)
		attachTags(cs, tagByRef[tagKey(SourceCommit, c.SHA)])
		allChunks = append(allChunks, cs...)
	}
	for _, rc := range input.ReviewComments {
		cs := ChunkReviewComment(input.WorkspaceID, rc)
		attachTags(cs, tagByRef[tagKey(SourceReviewComment, itoa64(rc.ID))])
		allChunks = append(allChunks, cs...)
	}

	if in.chunks != nil && len(allChunks) > 0 {
		n, err := in.chunks.SaveChunks(ctx, allChunks)
		if err != nil {
			return nil, fmt.Errorf("ingest: save chunks: %w", err)
		}
		res.ChunksEnqueued = n
	}

	// 3) Build + upsert graph edges.
	var allEdges []Edge
	for _, is := range input.Issues {
		allEdges = append(allEdges, BuildIssueEdges(input.WorkspaceID, is, input.KnownSymbols)...)
	}
	for _, pr := range input.PRs {
		allEdges = append(allEdges, BuildPREdges(input.WorkspaceID, pr, input.KnownSymbols)...)
	}
	for _, c := range input.Commits {
		allEdges = append(allEdges, BuildCommitEdges(input.WorkspaceID, c, input.KnownSymbols)...)
	}
	if in.edges != nil && len(allEdges) > 0 {
		if err := in.edges.UpsertEdges(ctx, allEdges); err != nil {
			return nil, fmt.Errorf("ingest: upsert edges: %w", err)
		}
	}
	res.EdgesUpserted = len(allEdges)

	return res, nil
}

// buildItems flattens all four source kinds into DiscourseItems for classification.
func buildItems(in IngestInput) []DiscourseItem {
	var items []DiscourseItem
	for _, is := range in.Issues {
		items = append(items, DiscourseItem{Kind: SourceIssue, Ref: itoa(is.Number), Repo: is.Repo, Title: is.Title, Body: is.Body})
	}
	for _, pr := range in.PRs {
		items = append(items, DiscourseItem{Kind: SourcePR, Ref: itoa(pr.Number), Repo: pr.Repo, Title: pr.Title, Body: pr.Body})
	}
	for _, c := range in.Commits {
		subj, body := splitCommitMessage(c.Message)
		items = append(items, DiscourseItem{Kind: SourceCommit, Ref: c.SHA, Repo: c.Repo, Title: subj, Body: body})
	}
	for _, rc := range in.ReviewComments {
		items = append(items, DiscourseItem{Kind: SourceReviewComment, Ref: itoa64(rc.ID), Repo: rc.Repo, Title: "", Body: rc.Body})
	}
	return items
}

func tagKey(kind SourceKind, ref string) string { return string(kind) + ":" + ref }

func attachTags(chunks []Chunk, tags []string) {
	if len(tags) == 0 {
		return
	}
	for i := range chunks {
		chunks[i].Tags = tags
	}
}
