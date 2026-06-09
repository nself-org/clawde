// chunker.go — chunk discourse source items into clawde_chunks shapes.
//
// Purpose:    Split each discourse item into chunks: a single "title" chunk, one
//             or more "body" chunks (512-token windows, 64-token overlap), and one
//             "comment" chunk per conversation/review comment. Each chunk carries
//             the correct source_type, doc_type='comment', and source_ref.
// Inputs:     Issue / PR / Commit / ReviewComment + workspace ID.
// Outputs:    []Chunk ready for insert + embed enqueue.
// Constraints: File ≤500 lines. Token approximation: whitespace-split words ≈
//             tokens (deterministic, dependency-free). 512 window / 64 overlap.
//
// SPORT: REGISTRY-FUNCTIONS.md → discourse.ChunkItem.
package discourse

import "strings"

const (
	// chunkTokens is the body-chunk window in approximate tokens.
	chunkTokens = 512
	// chunkOverlap is the overlap between successive body chunks.
	chunkOverlap = 64
)

// titleChunk builds the single title chunk for an item.
func titleChunk(ws, repo, ref string, kind SourceKind, title string) Chunk {
	return Chunk{
		WorkspaceID: ws, Content: title, DocType: kind.DocType(),
		SourceType: kind.SourceType(), SourceRef: ref, Repo: repo,
		Part: "title", Index: 0,
	}
}

// bodyChunks splits body text into 512-token / 64-overlap windows. An empty body
// yields no chunks. Indices continue after the title (start at 1).
func bodyChunks(ws, repo, ref string, kind SourceKind, body string, startIndex int) []Chunk {
	words := strings.Fields(body)
	if len(words) == 0 {
		return nil
	}
	var out []Chunk
	idx := startIndex
	step := chunkTokens - chunkOverlap
	if step <= 0 {
		step = chunkTokens
	}
	for start := 0; start < len(words); start += step {
		end := start + chunkTokens
		if end > len(words) {
			end = len(words)
		}
		out = append(out, Chunk{
			WorkspaceID: ws, Content: strings.Join(words[start:end], " "),
			DocType: kind.DocType(), SourceType: kind.SourceType(), SourceRef: ref,
			Repo: repo, Part: "body", Index: idx,
		})
		idx++
		if end == len(words) {
			break
		}
	}
	return out
}

// commentChunks builds one "comment" chunk per non-empty comment body.
func commentChunks(ws, repo, ref string, kind SourceKind, comments []Comment, startIndex int) []Chunk {
	var out []Chunk
	idx := startIndex
	for _, cm := range comments {
		if strings.TrimSpace(cm.Body) == "" {
			continue
		}
		out = append(out, Chunk{
			WorkspaceID: ws, Content: cm.Body, DocType: kind.DocType(),
			SourceType: kind.SourceType(), SourceRef: ref, Repo: repo,
			Part: "comment", Index: idx,
		})
		idx++
	}
	return out
}

// ChunkIssue produces title + body + comment chunks for an issue.
func ChunkIssue(ws string, is Issue) []Chunk {
	ref := itoa(is.Number)
	chunks := []Chunk{titleChunk(ws, is.Repo, ref, SourceIssue, is.Title)}
	chunks = append(chunks, bodyChunks(ws, is.Repo, ref, SourceIssue, is.Body, len(chunks))...)
	chunks = append(chunks, commentChunks(ws, is.Repo, ref, SourceIssue, is.Comments, len(chunks))...)
	return chunks
}

// ChunkPR produces title + body + comment chunks for a PR.
func ChunkPR(ws string, pr PR) []Chunk {
	ref := itoa(pr.Number)
	chunks := []Chunk{titleChunk(ws, pr.Repo, ref, SourcePR, pr.Title)}
	chunks = append(chunks, bodyChunks(ws, pr.Repo, ref, SourcePR, pr.Body, len(chunks))...)
	chunks = append(chunks, commentChunks(ws, pr.Repo, ref, SourcePR, pr.Comments, len(chunks))...)
	return chunks
}

// ChunkCommit produces a title chunk (subject) + body chunks (message body) for
// a commit. The first line is the title; the remainder is the body.
func ChunkCommit(ws string, c Commit) []Chunk {
	subject, body := splitCommitMessage(c.Message)
	chunks := []Chunk{titleChunk(ws, c.Repo, c.SHA, SourceCommit, subject)}
	chunks = append(chunks, bodyChunks(ws, c.Repo, c.SHA, SourceCommit, body, len(chunks))...)
	return chunks
}

// ChunkReviewComment produces a single "comment" chunk for a review comment.
func ChunkReviewComment(ws string, rc ReviewComment) []Chunk {
	if strings.TrimSpace(rc.Body) == "" {
		return nil
	}
	return []Chunk{{
		WorkspaceID: ws, Content: rc.Body, DocType: SourceReviewComment.DocType(),
		SourceType: SourceReviewComment.SourceType(), SourceRef: itoa64(rc.ID),
		Repo: rc.Repo, Part: "comment", Index: 0,
	}}
}

// splitCommitMessage separates the subject (first line) from the rest.
func splitCommitMessage(msg string) (subject, body string) {
	msg = strings.TrimRight(msg, "\n")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[:i]), strings.TrimSpace(msg[i+1:])
	}
	return strings.TrimSpace(msg), ""
}

// itoa / itoa64 — small deps-free int formatters (avoid importing strconv twice).
func itoa(n int) string   { return formatInt(int64(n)) }
func itoa64(n int64) string { return formatInt(n) }

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
