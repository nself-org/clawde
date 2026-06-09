// tsserver.go — TypeScript language server (tsserver) adapter.
//
// Purpose: Launch and communicate with tsserver via stdio JSON-RPC.
//          Implements LSPServer for TypeScript/JavaScript workspaces.
// Inputs:  workspace root; tsserver binary located via exec.LookPath("tsserver").
// Outputs: Definition and References results from tsserver.
// Constraints: File ≤500 lines. Degrades gracefully when tsserver absent.
//              tsserver speaks a slightly different JSON-RPC dialect; wrapped here.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.TSServer.
package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// TSServer is an LSPServer implementation backed by tsserver.
// tsserver is looked up via PATH; degrades with ErrServerUnavailable if absent.
type TSServer struct {
	root   string
	cmd    *exec.Cmd
	client *Client
	cancel context.CancelFunc
}

// NewTSServer creates a TSServer for the given workspace root.
// Returns ErrServerUnavailable if tsserver is not in PATH.
func NewTSServer(ctx context.Context, root string) (*TSServer, error) {
	binary, err := exec.LookPath("tsserver")
	if err != nil {
		slog.Warn("lsp: tsserver not found — cross-file resolution disabled for TS/JS", "err", err)
		return nil, ErrServerUnavailable
	}

	ctx2, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx2, binary, "--stdio") //nolint:gosec
	cmd.Env = os.Environ()
	cmd.Dir = root

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: tsserver stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: tsserver stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("lsp: tsserver start: %w", err)
	}

	c := NewClient(stdin)
	go c.Start(ctx2, stdout)

	srv := &TSServer{root: root, cmd: cmd, client: c, cancel: cancel}
	if err := srv.initialize(ctx2); err != nil {
		_ = srv.Stop(ctx2)
		return nil, err
	}
	return srv, nil
}

// initialize performs the LSP handshake for tsserver.
func (s *TSServer) initialize(ctx context.Context) error {
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
	if _, err := s.client.Call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("lsp: tsserver initialize: %w", err)
	}
	slog.Info("lsp: tsserver initialized", "root", s.root)
	return s.client.Notify("initialized", struct{}{})
}

// Name implements LSPServer.
func (s *TSServer) Name() string { return "tsserver" }

// Definition implements LSPServer.
func (s *TSServer) Definition(ctx context.Context, fileURI string, pos Position) ([]Location, error) {
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
func (s *TSServer) References(ctx context.Context, fileURI string, pos Position) ([]Location, error) {
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
func (s *TSServer) Stop(ctx context.Context) error {
	_, _ = s.client.Call(ctx, "shutdown", nil)
	_ = s.client.Notify("exit", nil)
	s.cancel()
	return s.cmd.Wait()
}
