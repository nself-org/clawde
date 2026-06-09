//go:build !treesitter

// extractor_stub.go — pure-Go stub extractor used when build tag 'treesitter' is absent.
//
// Purpose: Allows `go build ./...` and `go test ./...` to pass without cgo or the
//          tree-sitter grammars. Returns empty results; does not panic.
//
// To enable the real tree-sitter extractor:
//   CGO_ENABLED=1 \
//   CC="$(xcrun --find cc)" \
//   CGO_CFLAGS="-isysroot $(xcrun --show-sdk-path)" \
//   CGO_LDFLAGS="-isysroot $(xcrun --show-sdk-path)" \
//   go build -tags treesitter ./...
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel.ExtractSymbols (stub).
package repointel

import "github.com/google/uuid"

// StubExtractor is the no-op implementation used without cgo tree-sitter.
type StubExtractor struct{}

// NewExtractor returns the stub extractor when built without 'treesitter' tag.
func NewExtractor() Extractor { return &StubExtractor{} }

// ExtractSymbols returns empty slices. No cgo required.
func (s *StubExtractor) ExtractSymbols(_ uuid.UUID, _ string, _ []byte) ([]SymbolRecord, []CallEdge, error) {
	return nil, nil, nil
}
