// markdown_chunker.go — split markdown documents into doc-type-tagged chunks.
//
// Purpose:    Parse markdown by ATX heading hierarchy (H1=doc, H2=section,
//             H3+=subsection) and emit one DocChunk per heading region, tagged
//             doc_type='markdown'. Used for ingesting README/wiki/docs content and
//             external doc URLs into clawde_chunks → clawde_embed_queue.
// Inputs:     raw markdown content + source file path.
// Outputs:    []DocChunk (doc_type='markdown'), ordered by appearance.
// Constraints: File ≤500 lines. Stdlib only — a fenced-code-aware heading splitter
//             (no external markdown lib required, keeps the package cgo-free).
//
// SPORT: REGISTRY-FUNCTIONS.md → docs.MarkdownChunker.
package docs

import (
	"strings"
)

// DocType enumerates clawde_chunks.doc_type values (mirrors migration 0090 CHECK).
type DocType string

const (
	DocTypeCode      DocType = "code"
	DocTypeMarkdown  DocType = "markdown"
	DocTypeDocstring DocType = "docstring"
	DocTypeComment   DocType = "comment"
)

// DocChunk is a unit of documentation ready for embed enqueue.
type DocChunk struct {
	FilePath  string  // source file or URL
	Heading   string  // nearest heading text ("" for preamble)
	Content   string  // chunk body (heading line + content under it)
	DocType   DocType // code | markdown | docstring | comment
	LineStart int     // 1-based line where the chunk begins
	Level     int     // heading level: 1=doc(H1) 2=section(H2) 3+=subsection; 0=preamble
}

// MarkdownChunker splits markdown into heading-scoped chunks.
type MarkdownChunker struct {
	// MaxChunkBytes caps a single chunk's body; 0 = no cap (default 8192).
	MaxChunkBytes int
}

// NewMarkdownChunker returns a chunker with a sane default size cap.
func NewMarkdownChunker() *MarkdownChunker {
	return &MarkdownChunker{MaxChunkBytes: 8192}
}

// Chunk splits content by heading hierarchy. Each chunk runs from one heading
// (inclusive) up to the next heading of equal-or-shallower depth, OR to EOF.
// Content before the first heading becomes a Level-0 "preamble" chunk.
// Fenced code blocks (``` … ```) are not scanned for headings.
func (c *MarkdownChunker) Chunk(content, filePath string) []DocChunk {
	maxBytes := c.MaxChunkBytes
	if maxBytes <= 0 {
		maxBytes = 8192
	}

	lines := strings.Split(content, "\n")
	var chunks []DocChunk

	var (
		curHeading string
		curLevel   int
		curStart   = 1
		buf        strings.Builder
		inFence    bool
		started    bool // whether buf holds any content yet
	)

	flush := func(endLine int) {
		body := strings.TrimRight(buf.String(), "\n")
		buf.Reset()
		if strings.TrimSpace(body) == "" {
			return
		}
		if len(body) > maxBytes {
			body = body[:maxBytes]
		}
		chunks = append(chunks, DocChunk{
			FilePath:  filePath,
			Heading:   curHeading,
			Content:   body,
			DocType:   DocTypeMarkdown,
			LineStart: curStart,
			Level:     curLevel,
		})
	}

	for i, ln := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(ln)

		// Track fenced code regions so '#' inside code is not a heading.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			buf.WriteString(ln)
			buf.WriteString("\n")
			started = true
			continue
		}

		if !inFence {
			if level, text, ok := atxHeading(trimmed); ok {
				// New heading boundary: flush the prior region.
				if started {
					flush(lineNo - 1)
				}
				curHeading = text
				curLevel = level
				curStart = lineNo
				buf.WriteString(ln)
				buf.WriteString("\n")
				started = true
				continue
			}
		}

		buf.WriteString(ln)
		buf.WriteString("\n")
		started = true
	}
	flush(len(lines))

	return chunks
}

// atxHeading detects an ATX markdown heading ("# ", "## ", … up to 6 '#').
// Returns level (1-6), heading text, and true when the line is a heading.
func atxHeading(trimmed string) (int, string, bool) {
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level < 1 || level > 6 {
		return 0, "", false
	}
	// Must be followed by a space (ATX requires "# text", not "#text").
	if level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(strings.Trim(trimmed[level:], "# "))
	return level, text, true
}
