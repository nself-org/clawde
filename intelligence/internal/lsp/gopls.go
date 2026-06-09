// gopls.go — gopls LSP server adapter.
//
// Purpose: Launch and communicate with the gopls language server via stdio.
//          Implements the LSPServer interface for Go workspaces.
// Inputs:  workspace root path; gopls binary looked up via exec.LookPath.
// Outputs: Definition and References results from gopls.
// Constraints: File ≤500 lines. Gracefully degrades (log.Warn + ErrUnavailable)
//              when gopls binary is absent — never panics.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.GoplsServer.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// ErrServerUnavailable is returned when the language server binary is not found.
var ErrServerUnavailable = errors.New("lsp: server binary not available")

// GoplsServer is an LSPServer implementation backed by gopls.
type GoplsServer struct {
	root   string
	cmd    *exec.Cmd
	client *Client
	cancel context.CancelFunc
}

// NewGoplsServer creates a GoplsServer for the given workspace root.
// Returns ErrServerUnavailable if gopls is not installed — callers must handle.
func NewGoplsServer(ctx context.Context, root string) (*GoplsServer, error) {
	binary, err := exec.LookPath("gopls")
	if err != nil {
		slog.Warn("lsp: gopls not found — cross-file resolution disabled for Go", "err", err)
		return nil, ErrServerUnavailable
	}

	ctx2, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx2, binary, "serve") //nolint:gosec
	cmd.Env = os.Environ()
	cmd.Dir = root

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: gopls stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: gopls stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: gopls start: %w", err)
	}

	c := NewClient(stdin)
	go c.Start(ctx2, stdout)

	srv := &GoplsServer{root: root, cmd: cmd, client: c, cancel: cancel}
	if err := srv.initialize(ctx2); err != nil {
		_ = srv.Stop(ctx2)
		return nil, err
	}
	return srv, nil
}

// initialize performs the LSP handshake (initialize + initialized).
func (s *GoplsServer) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: nil,
		RootURI:   filePathToURI(s.root),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Definition: DynamicRegistrationCapability{DynamicRegistration: true},
				References: DynamicRegistrationCapability{DynamicRegistration: true},
			},
		},
	}
	raw, err := s.client.Call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("lsp: gopls initialize: %w", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("lsp: gopls initialize unmarshal: %w", err)
	}
	slog.Info("lsp: gopls initialized",
		"definitionProvider", result.Capabilities.DefinitionProvider,
		"referencesProvider", result.Capabilities.ReferencesProvider)
	return s.client.Notify("initialized", struct{}{})
}

// Name implements LSPServer.
func (s *GoplsServer) Name() string { return "gopls" }

// Definition implements LSPServer.
func (s *GoplsServer) Definition(ctx context.Context, fileURI string, pos Position) ([]Location, error) {
	params := DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
	}
	raw, err := s.client.Call(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return unmarshalLocations(raw)
}

// References implements LSPServer.
func (s *GoplsServer) References(ctx context.Context, fileURI string, pos Position) ([]Location, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     pos,
		Context:      ReferenceContext{IncludeDeclaration: false},
	}
	raw, err := s.client.Call(ctx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}
	return unmarshalLocations(raw)
}

// Stop implements LSPServer.
func (s *GoplsServer) Stop(ctx context.Context) error {
	_, _ = s.client.Call(ctx, "shutdown", nil)
	_ = s.client.Notify("exit", nil)
	s.cancel()
	return s.cmd.Wait()
}

// unmarshalLocations handles both Location and []Location responses.
func unmarshalLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// Try array first.
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err == nil {
		return locs, nil
	}
	// Try single Location.
	var loc Location
	if err := json.Unmarshal(raw, &loc); err != nil {
		return nil, err
	}
	return []Location{loc}, nil
}
