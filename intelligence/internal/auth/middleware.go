// Package auth — gRPC unary interceptor + HTTP middleware for JWT auth on public routes.
//
// Purpose: Add JWT auth layer on top of the existing HMAC layer.
//          HMAC (internal/server/auth.go, ADR-002) remains for clawd→clawde-intelligence
//          gRPC calls. This JWT middleware is ONLY for public-surface routes:
//            gRPC: GatewayPublicService (port 8094)
//            HTTP: /v1/retrieve, /v1/complete, and other public routes
//          Internal gRPC on port 8090 is HMAC-only; this middleware does NOT touch it.
//
// Inputs:  Authorization: Bearer <token> from HTTP header / gRPC metadata.
// Outputs: Injects *Claims + *Workspace into context; 401 on auth failure.
//          QuotaExceeded → 429 / ResourceExhausted.
// Constraints: Must not replace or disable UnaryHMACInterceptor on internal port.
// SPORT: REGISTRY-FUNCTIONS.md — UnaryJWTInterceptor, HTTPJWTMiddleware.
package auth

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const (
	contextKeyClaims    contextKey = iota
	contextKeyWorkspace contextKey = iota
)

// ClaimsFromContext extracts the validated Claims from a context set by the
// JWT middleware. Returns nil, false if not present.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(contextKeyClaims).(*Claims)
	return c, ok
}

// WorkspaceFromContext extracts the resolved Workspace from a context set by
// the JWT middleware. Returns nil, false if not present.
func WorkspaceFromContext(ctx context.Context) (*Workspace, bool) {
	w, ok := ctx.Value(contextKeyWorkspace).(*Workspace)
	return w, ok
}

// UnaryJWTInterceptor returns a gRPC unary interceptor for JWT auth.
// It validates the token, resolves the workspace, and enforces quota.
// The /Health method bypasses all checks.
func UnaryJWTInterceptor(v *JWTValidator, r *WorkspaceResolver, q *QuotaEnforcer) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if isHealthMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		ctx, err := applyJWTAuth(ctx, v, r, q)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// HTTPJWTMiddleware wraps an http.Handler to enforce JWT auth on every request.
// /health and /v1/gateway/health paths bypass auth.
func HTTPJWTMiddleware(v *JWTValidator, r *WorkspaceResolver, q *QuotaEnforcer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if isHealthPath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}

		authHeader := req.Header.Get("Authorization")
		rawToken, ok := bearerToken(authHeader)
		if !ok {
			http.Error(w, `{"error":"AUTH_FAILED: missing Bearer token"}`, http.StatusUnauthorized)
			return
		}

		claims, err := v.Validate(req.Context(), rawToken)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnauthorized)
			return
		}

		ws, err := r.Resolve(req.Context(), claims)
		if err != nil {
			if isWorkspaceNotFound(err) {
				http.Error(w, `{"error":"AUTH_FAILED: workspace not found"}`, http.StatusUnauthorized)
				return
			}
			http.Error(w, `{"error":"AUTH_FAILED: internal error"}`, http.StatusUnauthorized)
			return
		}

		if q != nil {
			if err := q.CheckAndIncrement(req.Context(), ws.ID, claims.Tier); err != nil {
				if isQuotaExceeded(err) {
					http.Error(w, `{"error":"quota exceeded"}`, http.StatusTooManyRequests)
					return
				}
				http.Error(w, `{"error":"AUTH_FAILED: quota check failed"}`, http.StatusUnauthorized)
				return
			}
		}

		ctx := context.WithValue(req.Context(), contextKeyClaims, claims)
		ctx = context.WithValue(ctx, contextKeyWorkspace, ws)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// applyJWTAuth extracts, validates, resolves workspace, and enforces quota.
// Returns an enriched context on success; gRPC status error on failure.
func applyJWTAuth(ctx context.Context, v *JWTValidator, r *WorkspaceResolver, q *QuotaEnforcer) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	authVals := md.Get("authorization")
	if len(authVals) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "AUTH_FAILED: missing authorization header")
	}

	rawToken, ok := bearerToken(authVals[0])
	if !ok {
		return ctx, status.Error(codes.Unauthenticated, "AUTH_FAILED: malformed authorization header")
	}

	claims, err := v.Validate(ctx, rawToken)
	if err != nil {
		return ctx, status.Errorf(codes.Unauthenticated, "%s", err.Error())
	}

	ws, err := r.Resolve(ctx, claims)
	if err != nil {
		if isWorkspaceNotFound(err) {
			return ctx, status.Error(codes.Unauthenticated, "AUTH_FAILED: workspace not found")
		}
		return ctx, status.Error(codes.Unauthenticated, "AUTH_FAILED: internal error")
	}

	if q != nil {
		if err := q.CheckAndIncrement(ctx, ws.ID, claims.Tier); err != nil {
			if isQuotaExceeded(err) {
				return ctx, status.Error(codes.ResourceExhausted, "daily quota exceeded")
			}
			return ctx, status.Error(codes.Unauthenticated, "AUTH_FAILED: quota check failed")
		}
	}

	ctx = context.WithValue(ctx, contextKeyClaims, claims)
	ctx = context.WithValue(ctx, contextKeyWorkspace, ws)
	return ctx, nil
}

// ---- helpers ----

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func isHealthMethod(method string) bool {
	return strings.HasSuffix(method, "/Health")
}

func isHealthPath(p string) bool {
	return p == "/health" || p == "/v1/gateway/health"
}

func isWorkspaceNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrWorkspaceNotFound.Error())
}

func isQuotaExceeded(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrQuotaExceeded.Error())
}
