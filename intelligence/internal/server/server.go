// Package server — gRPC + REST gateway server for clawde-intelligence.
//
// Purpose: Start gRPC server on 127.0.0.1:8090 and a plain-JSON REST HTTP
//          mux on 127.0.0.1:8091 (grpc-gateway pattern, hand-rolled JSON
//          bridge until grpc-gateway codegen is available).
//          Implements GatewayServiceServer delegating to internal/gateway.
// Inputs:  Config{GRPCAddr, RESTAddr, HMACSecret, Providers, Env}.
// Outputs: Running servers; Shutdown() for graceful stop.
// Constraints: Binds only to 127.0.0.1 (never 0.0.0.0).
//              CLAWDE_ENV=production → reflection disabled.
//              HMAC secret never logged.
// SPORT: REGISTRY-SERVICES.md — grpc_addr=127.0.0.1:8090,
//                                rest_addr=127.0.0.1:8091,
//                                auth=HMAC-SHA256,
//                                reflection=disabled-in-production.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultGRPCAddr = "127.0.0.1:8090"
	defaultRESTAddr = "127.0.0.1:8091"
)

// Config holds all server startup parameters.
type Config struct {
	// GRPCAddr is the gRPC listen address (must be 127.0.0.1:port).
	GRPCAddr string
	// RESTAddr is the HTTP/REST listen address (must be 127.0.0.1:port).
	RESTAddr string
	// HMACSecret is the shared secret for HMAC-SHA256 auth.
	// Never log this value.
	HMACSecret []byte
	// Providers is the list of gateway providers to route to and health-check.
	Providers []gw.Provider
	// Env is "production" | "dev" | "test"; controls reflection.
	Env string
}

// DefaultConfig returns a Config populated from canonical defaults and env vars.
func DefaultConfig(providers []gw.Provider) (*Config, error) {
	secret, err := HMACSecret()
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return &Config{
		GRPCAddr:   defaultGRPCAddr,
		RESTAddr:   defaultRESTAddr,
		HMACSecret: secret,
		Providers:  providers,
		Env:        os.Getenv("CLAWDE_ENV"),
	}, nil
}

// Server holds the running gRPC and HTTP servers.
type Server struct {
	cfg      Config
	grpcSrv  *grpc.Server
	httpSrv  *http.Server
	mu       sync.Mutex
}

// New creates a Server but does not start listeners.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Start binds and starts both gRPC (8090) and REST (8091) listeners.
// Returns once both are listening. Errors if either bind fails.
func (s *Server) Start() error {
	secret := s.cfg.HMACSecret

	// ---- gRPC server ----
	grpcLis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("server: grpc listen %s: %w", s.cfg.GRPCAddr, err)
	}

	s.grpcSrv = grpc.NewServer(
		grpc.UnaryInterceptor(UnaryHMACInterceptor(secret)),
		grpc.StreamInterceptor(StreamHMACInterceptor(secret)),
	)

	handler := &gatewayHandler{
		providers: s.cfg.Providers,
		health:    newHealthHandler(s.cfg.Providers),
	}
	RegisterGatewayServiceServer(s.grpcSrv, handler)

	// Reflection: enabled in dev/test, disabled in production.
	if s.cfg.Env != "production" {
		reflection.Register(s.grpcSrv)
	}

	go func() {
		if err := s.grpcSrv.Serve(grpcLis); err != nil {
			// Serve returns when GracefulStop/Stop is called; log only unexpected.
			if s.grpcSrv != nil {
				_ = err // server stopped; acceptable
			}
		}
	}()

	// ---- REST / HTTP mux ----
	mux := s.buildRESTMux(handler)
	s.httpSrv = &http.Server{
		Addr:         s.cfg.RESTAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	restLis, err := net.Listen("tcp", s.cfg.RESTAddr)
	if err != nil {
		s.grpcSrv.Stop()
		return fmt.Errorf("server: rest listen %s: %w", s.cfg.RESTAddr, err)
	}

	go func() { _ = s.httpSrv.Serve(restLis) }()

	return nil
}

// GRPCServer returns the underlying *grpc.Server so callers can attach
// additional net.Listeners (e.g. a Tailscale mesh listener) to the same
// gRPC instance.  Returns nil before Start() is called.
func (s *Server) GRPCServer() *grpc.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grpcSrv
}

// Shutdown gracefully stops both servers.
func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grpcSrv != nil {
		s.grpcSrv.GracefulStop()
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Shutdown(ctx)
	}
}

// ---- REST mux (hand-rolled JSON bridge, replaces grpc-gateway codegen) ----

func (s *Server) buildRESTMux(h *gatewayHandler) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/gateway/complete", s.restHMAC(func(w http.ResponseWriter, r *http.Request) {
		var req CompleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := h.Complete(r.Context(), &req)
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}))

	mux.HandleFunc("/v1/gateway/embed", s.restHMAC(func(w http.ResponseWriter, r *http.Request) {
		var req EmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := h.Embed(r.Context(), &req)
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}))

	mux.HandleFunc("/v1/gateway/rerank", s.restHMAC(func(w http.ResponseWriter, r *http.Request) {
		var req RerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := h.Rerank(r.Context(), &req)
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}))

	// /stream: returns newline-delimited JSON chunks.
	mux.HandleFunc("/v1/gateway/stream", s.restHMAC(func(w http.ResponseWriter, r *http.Request) {
		var req StreamCompleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Transfer-Encoding", "chunked")
		flusher, ok := w.(http.Flusher)
		ss := &httpStreamServer{w: w, flusher: flusher, hasFlusher: ok}
		if err := h.StreamComplete(&req, ss); err != nil {
			_ = err // already sent some chunks; best-effort
		}
	}))

	// /health: no HMAC required.
	mux.HandleFunc("/v1/gateway/health", func(w http.ResponseWriter, r *http.Request) {
		resp := h.health.check(r.Context())
		writeJSON(w, http.StatusOK, resp)
	})

	return mux
}

// restHMAC wraps an HTTP handler with HMAC validation matching the gRPC scheme.
func (s *Server) restHMAC(next http.HandlerFunc) http.HandlerFunc {
	secret := s.cfg.HMACSecret
	return func(w http.ResponseWriter, r *http.Request) {
		tsStr := r.Header.Get("X-ClawDE-Timestamp")
		sig := r.Header.Get("X-ClawDE-Signature")
		bodySHA := r.Header.Get("X-ClawDE-Body-SHA256")

		body := []byte{}
		if bodySHA == "" {
			bodySHA = BodySHA256Hex(body)
		}
		expected := ComputeSignature(secret, tsStr, bodySHA)
		if tsStr == "" || sig == "" {
			writeJSONError(w, http.StatusUnauthorized, "AUTH_FAILED: missing auth headers")
			return
		}
		if err := ValidateSignatureString(secret, tsStr, []byte(bodySHA), sig); err != nil {
			// Also accept comparison against raw expected for non-body use.
			if sig != expected {
				writeJSONError(w, http.StatusUnauthorized, "AUTH_FAILED: invalid signature")
				return
			}
		}
		next(w, r)
	}
}

// httpStreamServer adapts the REST response writer to GatewayService_StreamCompleteServer.
type httpStreamServer struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	hasFlusher bool
	grpc.ServerStream // embedded for interface satisfaction; unused methods panic.
}

func (s *httpStreamServer) Send(chunk *StreamChunkMsg) error {
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = s.w.Write(b)
	if s.hasFlusher {
		s.flusher.Flush()
	}
	return err
}

func (s *httpStreamServer) Context() context.Context { return context.Background() }

// ---- JSON helpers ----

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeGRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpCode := grpcCodeToHTTP(st.Code())
	writeJSON(w, httpCode, map[string]string{"error": st.Message(), "code": st.Code().String()})
}

func grpcCodeToHTTP(c codes.Code) int {
	switch c {
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
