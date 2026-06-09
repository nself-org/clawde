// graph.go — build clawde_graph_edges from discourse items.
//
// Purpose:    Derive GraphRAG edges from discourse content:
//             REFERENCES — a discourse item mentions a known code symbol;
//             MODIFIES   — a commit or PR modifies a file;
//             LINKS      — an issue↔PR link (PR closes/references an issue).
// Inputs:     Issue / PR / Commit + known-symbol set (for REFERENCES).
// Outputs:    []Edge ready to UpsertEdges into clawde_graph_edges.
// Constraints: File ≤500 lines. Symbol mentions matched against a provided
//             symbol set (case-sensitive word boundaries) — no AST here.
//
// SPORT: REGISTRY-FUNCTIONS.md → discourse.BuildEdges.
package discourse

import "regexp"

// identRe matches identifier-like tokens for symbol-mention detection.
var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// referenceEdges scans text for mentions of known symbols and emits REFERENCES
// edges from the (srcType, srcRef) discourse node to each matched symbol.
func referenceEdges(ws, repo, srcType, srcRef, text string, knownSymbols map[string]bool) []Edge {
	if len(knownSymbols) == 0 || text == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []Edge
	for _, tok := range identRe.FindAllString(text, -1) {
		if !knownSymbols[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, Edge{
			WorkspaceID: ws, Kind: EdgeReferences, SrcType: srcType, SrcRef: srcRef,
			DstType: "symbol", DstRef: tok, Repo: repo,
		})
	}
	return out
}

// modifiesEdges emits one MODIFIES edge per changed file.
func modifiesEdges(ws, repo, srcType, srcRef string, files []string) []Edge {
	seen := map[string]bool{}
	var out []Edge
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, Edge{
			WorkspaceID: ws, Kind: EdgeModifies, SrcType: srcType, SrcRef: srcRef,
			DstType: "file", DstRef: f, Repo: repo,
		})
	}
	return out
}

// BuildIssueEdges returns REFERENCES edges for an issue (title + body).
func BuildIssueEdges(ws string, is Issue, knownSymbols map[string]bool) []Edge {
	ref := itoa(is.Number)
	return referenceEdges(ws, is.Repo, string(SourceIssue), ref, is.Title+"\n"+is.Body, knownSymbols)
}

// BuildPREdges returns REFERENCES (from title+body), MODIFIES (from changed
// files), and LINKS (issue↔PR) edges for a PR.
func BuildPREdges(ws string, pr PR, knownSymbols map[string]bool) []Edge {
	ref := itoa(pr.Number)
	var out []Edge
	out = append(out, referenceEdges(ws, pr.Repo, string(SourcePR), ref, pr.Title+"\n"+pr.Body, knownSymbols)...)
	out = append(out, modifiesEdges(ws, pr.Repo, string(SourcePR), ref, pr.ChangedFiles)...)
	for _, issueNum := range pr.LinkedIssues {
		out = append(out, Edge{
			WorkspaceID: ws, Kind: EdgeLinks, SrcType: string(SourcePR), SrcRef: ref,
			DstType: string(SourceIssue), DstRef: itoa(issueNum), Repo: pr.Repo,
		})
	}
	return out
}

// BuildCommitEdges returns REFERENCES (from message) + MODIFIES (from changed
// files) edges for a commit.
func BuildCommitEdges(ws string, c Commit, knownSymbols map[string]bool) []Edge {
	var out []Edge
	out = append(out, referenceEdges(ws, c.Repo, string(SourceCommit), c.SHA, c.Message, knownSymbols)...)
	out = append(out, modifiesEdges(ws, c.Repo, string(SourceCommit), c.SHA, c.ChangedFiles)...)
	return out
}
