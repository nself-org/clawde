// types.go — shared types for GitHub discourse intelligence ingestion.
//
// Purpose:    Define the four discourse source kinds (issue, pr, commit,
//             review_comment), their doc_type/source_type mapping for
//             clawde_chunks, the DiscourseItem classification unit, the chunk
//             shape emitted into clawde_chunks, and the graph-edge model written
//             to clawde_graph_edges.
// Inputs:     N/A (declarations).
// Outputs:    N/A.
// Constraints: File ≤500 lines. doc_type/source_type must match migration CHECK
//             constraints. Stdlib + uuid only.
//
// SPORT: REGISTRY-PACKAGES.md → pkg/discourse.
package discourse

import "time"

// SourceKind enumerates the four GitHub discourse source kinds this package
// ingests. Each maps to a clawde_chunks.source_type value.
type SourceKind string

const (
	SourceIssue         SourceKind = "issue"
	SourcePR            SourceKind = "pr"
	SourceCommit        SourceKind = "commit"
	SourceReviewComment SourceKind = "review_comment"
)

// DocType mirrors clawde_chunks.doc_type. Discourse content is prose, so issues,
// PRs, and review comments are doc_type='comment'; commit messages are too.
const (
	DocTypeComment = "comment"
)

// SourceType returns the clawde_chunks.source_type for a SourceKind.
func (k SourceKind) SourceType() string { return string(k) }

// DocType returns the clawde_chunks.doc_type for a SourceKind. All discourse
// kinds are prose → 'comment'.
func (k SourceKind) DocType() string { return DocTypeComment }

// Issue is a GitHub issue (REST/GraphQL normalized).
type Issue struct {
	Number    int
	Title     string
	Body      string
	Author    string
	State     string
	URL       string
	Repo      string // "owner/name"
	CreatedAt time.Time
	UpdatedAt time.Time
	Comments  []Comment
}

// PR is a GitHub pull request (REST/GraphQL normalized).
type PR struct {
	Number       int
	Title        string
	Body         string
	Author       string
	State        string
	URL          string
	Repo         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ChangedFiles []string // file paths modified by the PR
	LinkedIssues []int    // issue numbers referenced/closed by this PR
	Comments     []Comment
}

// Commit is a GitHub commit (REST normalized).
type Commit struct {
	SHA          string
	Message      string
	Author       string
	URL          string
	Repo         string
	CommittedAt  time.Time
	ChangedFiles []string // file paths touched by the commit
}

// ReviewComment is a single PR review comment (inline or top-level).
type ReviewComment struct {
	ID       int64
	PRNumber int
	Body     string
	Author   string
	URL      string
	Repo     string
	Path     string // file the comment is anchored to ("" for top-level)
	CreatedAt time.Time
}

// Comment is a generic issue/PR conversation comment.
type Comment struct {
	ID     int64
	Body   string
	Author string
}

// DiscourseItem is a unit handed to the classifier. The classifier returns
// taxonomy tags appended back to the item via Tags.
type DiscourseItem struct {
	Kind  SourceKind
	Ref   string // source_ref: issue/PR number, commit SHA, or comment ID
	Repo  string
	Title string // title or first line (commit subject)
	Body  string // body text used for classification
	Tags  []string
}

// Chunk is a unit ready to insert into clawde_chunks + enqueue for embedding.
type Chunk struct {
	WorkspaceID string
	Content     string
	DocType     string // "comment"
	SourceType  string // issue | pr | commit | review_comment
	SourceRef   string // number / SHA / comment ID
	Repo        string
	Part        string // "title" | "body" | "comment"
	Index       int    // 0-based ordinal within this source item
	Tags        []string
}

// Edge models a clawde_graph_edges row produced by discourse ingestion.
type Edge struct {
	WorkspaceID string
	Kind        EdgeKind
	SrcType     string // source kind of the src node (issue/pr/commit/review_comment)
	SrcRef      string // src node ref
	DstType     string // "symbol" | "file" | "issue" | "pr"
	DstRef      string // symbol name, file path, or number
	Repo        string
}

// EdgeKind enumerates discourse graph-edge relationship kinds.
type EdgeKind string

const (
	// EdgeReferences: a discourse item textually mentions a code symbol.
	EdgeReferences EdgeKind = "REFERENCES"
	// EdgeModifies: a commit/PR modifies a file.
	EdgeModifies EdgeKind = "MODIFIES"
	// EdgeLinks: an issue↔PR link (PR closes/references issue).
	EdgeLinks EdgeKind = "LINKS"
)
