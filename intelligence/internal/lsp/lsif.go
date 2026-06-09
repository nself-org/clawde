// lsif.go — LSIF (Language Server Index Format) dump parser.
//
// Purpose: Parse a pre-generated LSIF JSON Lines dump and load cross-file
//          edges into clawde_graph_edges as a fallback when a live LSP server
//          is unavailable (e.g., in CI without gopls/tsserver).
// Inputs:  An io.Reader containing LSIF JSON Lines (one JSON object per line).
// Outputs: []CrossEdge extracted from item/textDocument edges in the dump.
// Constraints: File ≤500 lines. No external deps beyond stdlib.
//              Only "item" edges with cross-document outV→inV links are loaded.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.ParseLSIF, lsp.LSIFEdge.

package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// ── LSIF vertex/edge types ────────────────────────────────────────────────────

// lsifElement is a generic LSIF line — type + label + id.
type lsifElement struct {
	ID    int64           `json:"id"`
	Type  string          `json:"type"`  // "vertex" | "edge"
	Label string          `json:"label"` // e.g. "document", "range", "item"
	Extra json.RawMessage `json:"-"`
}

// lsifDocument is a "document" vertex.
type lsifDocument struct {
	URI string `json:"uri"`
}

// lsifRange is a "range" vertex.
type lsifRange struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// lsifItemEdge is an "item" edge linking a result set to specific ranges.
type lsifItemEdge struct {
	OutV     int64   `json:"outV"`
	InVs     []int64 `json:"inVs"`
	Document int64   `json:"document"` // document vertex id the inVs belong to
	Property string  `json:"property"` // "definitions" | "references" | ""
}

// LSIFEdge is a parsed cross-file relationship from an LSIF dump.
type LSIFEdge struct {
	SrcURI  string
	SrcLine int
	SrcChar int
	DstURI  string
	DstLine int
	DstChar int
	Kind    string // EdgeKindResolves | EdgeKindReferences
}

// ParseLSIF reads an LSIF JSON Lines stream and returns cross-file edges.
// Lines that are not valid JSON or are unrecognized vertex/edge types are skipped.
func ParseLSIF(r io.Reader) ([]LSIFEdge, error) {
	docs := make(map[int64]string)     // id → URI
	ranges := make(map[int64]lsifRange) // id → range
	var edges []LSIFEdge

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB line buffer

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Parse just id/type/label and the full raw line.
		var elem struct {
			ID    int64           `json:"id"`
			Type  string          `json:"type"`
			Label string          `json:"label"`
			URI   string          `json:"uri"`   // present on document vertices
			Start *Position       `json:"start"` // present on range vertices
			End   *Position       `json:"end"`
			OutV  int64           `json:"outV"`
			InVs  []int64         `json:"inVs"`
			Document int64        `json:"document"`
			Property string       `json:"property"`
			Extra json.RawMessage `json:",omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &elem); err != nil {
			continue // skip malformed
		}

		switch elem.Type {
		case "vertex":
			switch elem.Label {
			case "document":
				docs[elem.ID] = elem.URI
			case "range":
				if elem.Start != nil {
					ranges[elem.ID] = lsifRange{Start: *elem.Start}
				}
			}
		case "edge":
			if elem.Label != "item" || elem.Document == 0 || len(elem.InVs) == 0 {
				continue
			}
			dstURI, ok := docs[elem.Document]
			if !ok {
				continue
			}
			// Determine edge kind from property.
			kind := EdgeKindReferences
			if elem.Property == "definitions" {
				kind = EdgeKindResolves
			}
			// outV is the result-set vertex; trace back to a range if possible.
			// For cross-file we care about dstURI vs where outV's range lives.
			for _, inV := range elem.InVs {
				r, ok := ranges[inV]
				if !ok {
					continue
				}
				// srcURI: if outV is a range, use its doc; otherwise unknown.
				// LSIF dumps may have complex graph; we emit edges using dstURI.
				edges = append(edges, LSIFEdge{
					SrcURI:  "",            // caller maps from context
					SrcLine: 0,
					SrcChar: 0,
					DstURI:  dstURI,
					DstLine: r.Start.Line,
					DstChar: r.Start.Character,
					Kind:    kind,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return edges, fmt.Errorf("lsif: scan error: %w", err)
	}
	slog.Info("lsif: parsed dump", "edges", len(edges))
	return edges, nil
}

// LSIFEdgesToCrossEdges converts LSIFEdge slices to CrossEdge slices.
// srcSymbolID is the symbol that owns the outgoing edge in the DB.
func LSIFEdgesToCrossEdges(lsifEdges []LSIFEdge, workspaceID, srcSymbolID uuid.UUID) []CrossEdge {
	out := make([]CrossEdge, 0, len(lsifEdges))
	for _, e := range lsifEdges {
		if e.DstURI == "" {
			continue
		}
		out = append(out, CrossEdge{
			WorkspaceID: workspaceID,
			SrcSymbolID: srcSymbolID,
			DstFilePath: uriToFilePath(e.DstURI),
			DstLine:     e.DstLine,
			DstChar:     e.DstChar,
			Kind:        e.Kind,
			Metadata:    fmt.Sprintf(`{"source":"lsif","uri":%q}`, e.DstURI),
		})
	}
	return out
}
