// client.go — generic LSP stdio JSON-RPC 2.0 client.
//
// Purpose: Send and receive LSP JSON-RPC messages over stdio pipes to a language
//          server process. Handles Content-Length framing, request/response
//          correlation, and notification dispatch.
// Inputs:  exec.Cmd with Stdin/Stdout connected; RequestID auto-incremented.
// Outputs: json.RawMessage responses; error on transport failure.
// Constraints: File ≤500 lines. No external deps beyond stdlib.
//              Thread-safe: concurrent Call() invocations are safe.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.Client, lsp.Call, lsp.Notify.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ── JSON-RPC types ────────────────────────────────────────────────────────────

// Request is an outgoing JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"` // nil = notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is an incoming JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *int64           `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *ResponseError   `json:"error,omitempty"`
	Method  string           `json:"method,omitempty"` // set for server→client notifications
	Params  json.RawMessage  `json:"params,omitempty"`
}

// ResponseError mirrors the LSP error object.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("LSP error %d: %s", e.Code, e.Message)
}

// pending holds an in-flight request awaiting a response.
type pending struct {
	ch chan Response
}

// ── Client ────────────────────────────────────────────────────────────────────

// Client is a generic LSP JSON-RPC stdio client.
// Create via NewClient; call Start to begin the read loop.
type Client struct {
	writer  io.WriteCloser
	nextID  atomic.Int64
	mu      sync.Mutex
	inflight map[int64]*pending
	done    chan struct{}
	onNotify func(method string, params json.RawMessage) // optional notification handler
}

// NewClient creates a Client that writes to w and reads from r.
// Call Start(r) in a goroutine to begin the read loop.
func NewClient(w io.WriteCloser) *Client {
	return &Client{
		writer:   w,
		inflight: make(map[int64]*pending),
		done:     make(chan struct{}),
	}
}

// SetNotificationHandler registers a callback for server-initiated notifications.
// Must be called before Start.
func (c *Client) SetNotificationHandler(fn func(method string, params json.RawMessage)) {
	c.onNotify = fn
}

// Start launches the response-reader loop. Blocks until r is closed or ctx is done.
// Call in a dedicated goroutine.
func (c *Client) Start(ctx context.Context, r io.Reader) {
	defer close(c.done)
	scanner := bufio.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Read headers.
		contentLength := -1
		for {
			line, err := scanner.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					slog.Warn("lsp: header read error", "err", err)
				}
				c.cancelAll(err)
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // end of headers
			}
			if strings.HasPrefix(line, "Content-Length:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				n, _ := strconv.Atoi(val)
				contentLength = n
			}
		}
		if contentLength < 0 {
			slog.Warn("lsp: missing Content-Length header")
			continue
		}

		// Read body.
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(scanner, body); err != nil {
			slog.Warn("lsp: body read error", "err", err)
			c.cancelAll(err)
			return
		}

		var resp Response
		if err := json.Unmarshal(body, &resp); err != nil {
			slog.Warn("lsp: unmarshal error", "err", err)
			continue
		}

		if resp.ID != nil {
			// It's a response to one of our requests.
			c.mu.Lock()
			p, ok := c.inflight[*resp.ID]
			if ok {
				delete(c.inflight, *resp.ID)
			}
			c.mu.Unlock()
			if ok {
				p.ch <- resp
			}
		} else if resp.Method != "" && c.onNotify != nil {
			// Server-initiated notification.
			c.onNotify(resp.Method, resp.Params)
		}
	}
}

// Call sends a request and waits for the response or ctx cancellation.
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("lsp: marshal params: %w", err)
	}
	req := Request{JSONRPC: "2.0", ID: &id, Method: method, Params: raw}

	ch := make(chan Response, 1)
	c.mu.Lock()
	c.inflight[id] = &pending{ch: ch}
	c.mu.Unlock()

	if err := c.writeFrame(req); err != nil {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("lsp: client closed")
	}
}

// Notify sends a JSON-RPC notification (no ID, no response expected).
func (c *Client) Notify(method string, params interface{}) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal notify params: %w", err)
	}
	return c.writeFrame(Request{JSONRPC: "2.0", Method: method, Params: raw})
}

// writeFrame writes a single Content-Length framed JSON-RPC message.
func (c *Client) writeFrame(req Request) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("lsp: marshal request: %w", err)
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	_, err = io.WriteString(c.writer, frame)
	return err
}

// cancelAll resolves all pending calls with an error when the transport closes.
func (c *Client) cancelAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, p := range c.inflight {
		p.ch <- Response{Error: &ResponseError{Code: -32000, Message: err.Error()}}
		delete(c.inflight, id)
	}
}
