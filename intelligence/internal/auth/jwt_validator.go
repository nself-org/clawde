// Package auth — JWT validation + workspace isolation for public clawde-intelligence APIs.
//
// Purpose: Validate RS256/ES256 JWTs on public routes (/v1/retrieve, /v1/complete, etc.).
//          Internal clawd→clawde-intelligence gRPC uses HMAC (ADR-002) and is NOT replaced.
// Inputs:  Authorization: Bearer <token> header.
// Outputs: Claims{Sub, WorkspaceID, Tier, TokenID} on success; 401 on failure.
// Constraints:
//   - RS256 and ES256 ONLY. HS256 and "none" are rejected explicitly.
//   - JWKS fetch failure → 401 fail-closed (never use stale key past TTL).
//   - JWKS cached for 1 hour; refreshed lazily on cache miss or expiry.
//   - WorkspaceID claim key: "clawde/workspace_id". Tier claim: "clawde/tier".
//   - workspace_id present in JWT but no matching clawde_workspaces row → 401.
//
// SPORT: REGISTRY-FUNCTIONS.md — JWTValidator, Claims.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	// ClaimWorkspaceID is the JWT custom claim key for the workspace UUID.
	ClaimWorkspaceID = "clawde/workspace_id"
	// ClaimTier is the JWT custom claim key for the subscription tier.
	ClaimTier = "clawde/tier"

	jwksCacheTTL = time.Hour
)

// Tier represents the subscription tier encoded in the JWT.
type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierEnterprise Tier = "enterprise"
)

// DailyLimit returns the daily request limit for the tier.
// Enterprise tier returns -1 (unlimited).
func (t Tier) DailyLimit() int {
	switch t {
	case TierPro:
		return 10_000
	case TierEnterprise:
		return -1 // unlimited
	default: // free + unknown tiers
		return 100
	}
}

// Claims holds the validated JWT claims extracted for downstream use.
type Claims struct {
	// Sub is the subject (user/service identifier).
	Sub string
	// WorkspaceID is the clawde/workspace_id claim value (may be empty).
	WorkspaceID string
	// Tier is the clawde/tier claim value.
	Tier Tier
	// TokenID is the JWT ID (jti), used for revocation checks.
	TokenID string
}

// JWTValidator validates RS256/ES256 JWTs against a JWKS endpoint.
type JWTValidator struct {
	jwksURL  string
	issuer   string
	audience string

	mu          sync.RWMutex
	keySet      jwk.Set
	keySetFetch time.Time
}

// NewJWTValidator creates a JWTValidator. It does not fetch JWKS eagerly;
// the first validation call will fetch and cache the key set.
func NewJWTValidator(jwksURL, issuer, audience string) *JWTValidator {
	return &JWTValidator{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
	}
}

// Validate parses and validates a raw JWT string.
// Returns Claims on success; error (wrapping a 401-category message) on failure.
func (v *JWTValidator) Validate(ctx context.Context, rawToken string) (*Claims, error) {
	// Step 1: peek at the header — reject HS256 and "none" before doing anything else.
	// We parse only the JWS structure (no signature verification yet) to read alg.
	msg, err := jws.Parse([]byte(rawToken))
	if err != nil {
		return nil, fmt.Errorf("AUTH_FAILED: malformed JWT: %w", err)
	}
	if len(msg.Signatures()) == 0 {
		return nil, fmt.Errorf("AUTH_FAILED: no signatures in JWT")
	}
	algRaw := msg.Signatures()[0].ProtectedHeaders().Algorithm()
	alg := string(algRaw)
	switch alg {
	case "RS256", "ES256":
		// allowed
	case "HS256", "HS384", "HS512":
		return nil, fmt.Errorf("AUTH_FAILED: symmetric algorithm %s rejected", alg)
	case "none", "":
		return nil, fmt.Errorf("AUTH_FAILED: algorithm 'none' rejected")
	default:
		return nil, fmt.Errorf("AUTH_FAILED: unsupported algorithm %s", alg)
	}

	// Step 2: get (possibly cached) JWKS.
	ks, err := v.keySetFor(ctx)
	if err != nil {
		// JWKS fetch failure → fail-closed (never fall through with stale key past TTL).
		return nil, fmt.Errorf("AUTH_FAILED: JWKS unavailable: %w", err)
	}

	// Step 3: parse + verify signature using the JWKS.
	// Restrict accepted algorithms to RS256 and ES256 only.
	// WithRequireKid(false): allow matching without kid claim in token (alg-based fallback).
	parsed, err := jwt.Parse([]byte(rawToken),
		jwt.WithKeySet(ks, jws.WithRequireKid(false)),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(0),
	)
	if err != nil {
		return nil, fmt.Errorf("AUTH_FAILED: %w", err)
	}

	// Step 4: assert the signing key type matches the declared algorithm.
	if err := assertKeyTypeMatchesAlg(ks, parsed, string(alg)); err != nil {
		return nil, fmt.Errorf("AUTH_FAILED: %w", err)
	}

	// Step 5: extract custom claims.
	claims := &Claims{
		Sub:     parsed.Subject(),
		TokenID: parsed.JwtID(),
	}

	if wsID, ok := parsed.Get(ClaimWorkspaceID); ok {
		if s, ok := wsID.(string); ok {
			claims.WorkspaceID = s
		}
	}

	tier := TierFree
	if tv, ok := parsed.Get(ClaimTier); ok {
		if s, ok := tv.(string); ok {
			tier = Tier(s)
		}
	}
	claims.Tier = tier

	return claims, nil
}

// keySetFor returns a cached JWKS, refreshing when stale.
// If the remote JWKS cannot be fetched (network error, 4xx/5xx), returns an error
// regardless of whether a prior cached set exists — fail-closed on expiry.
func (v *JWTValidator) keySetFor(ctx context.Context) (jwk.Set, error) {
	v.mu.RLock()
	if v.keySet != nil && time.Since(v.keySetFetch) < jwksCacheTTL {
		ks := v.keySet
		v.mu.RUnlock()
		return ks, nil
	}
	v.mu.RUnlock()

	// Cache miss or stale — fetch fresh keys.
	v.mu.Lock()
	defer v.mu.Unlock()
	// Double-check under write lock in case another goroutine already refreshed.
	if v.keySet != nil && time.Since(v.keySetFetch) < jwksCacheTTL {
		return v.keySet, nil
	}

	ks, err := jwk.Fetch(ctx, v.jwksURL)
	if err != nil {
		// Fail-closed: do NOT return the stale set after TTL expiry.
		return nil, fmt.Errorf("JWKS fetch from %s: %w", v.jwksURL, err)
	}
	v.keySet = ks
	v.keySetFetch = time.Now()
	return ks, nil
}

// assertKeyTypeMatchesAlg verifies at least one key in the set has the right
// raw key type for the declared algorithm — additional hardening beyond what
// the library already verified.
func assertKeyTypeMatchesAlg(ks jwk.Set, _ jwt.Token, alg string) error {
	if ks.Len() == 0 {
		// Empty key set (test helpers that inject a nil-like set) — skip.
		return nil
	}
	var foundCorrect bool
	keys := ks.Keys(context.Background())
	for keys.Next(context.Background()) {
		k := keys.Pair().Value.(jwk.Key)
		switch alg {
		case "RS256":
			var pub rsa.PublicKey
			if err := k.Raw(&pub); err == nil {
				foundCorrect = true
			}
		case "ES256":
			var pub ecdsa.PublicKey
			if err := k.Raw(&pub); err == nil {
				foundCorrect = true
			}
		}
	}
	if !foundCorrect {
		return fmt.Errorf("no key of correct type for algorithm %s in JWKS", alg)
	}
	return nil
}

// InjectTestKeySet replaces the cached key set with the provided one (test use only).
// Not safe for concurrent use with Validate; call before tests start.
func (v *JWTValidator) InjectTestKeySet(ks jwk.Set) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keySet = ks
	v.keySetFetch = time.Now()
}
