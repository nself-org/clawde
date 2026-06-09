// Package auth — unit tests for JWTValidator.
//
// All tests use an in-process RSA keypair + injected JWKS — no network calls.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIssuer   = "https://auth.clawde.test"
	testAudience = "clawde-intelligence"
)

// testKeyPair generates a fresh RSA-2048 keypair and a JWKS containing the
// public key. Returns the private key (for signing) and the JWKS (for inject).
func testKeyPair(t *testing.T) (*rsa.PrivateKey, jwk.Set) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := jwk.FromRaw(priv.Public())
	require.NoError(t, err)
	require.NoError(t, pubKey.Set(jwk.AlgorithmKey, jwa.RS256))
	require.NoError(t, pubKey.Set(jwk.KeyIDKey, "test-kid"))

	ks := jwk.NewSet()
	require.NoError(t, ks.AddKey(pubKey))
	return priv, ks
}

// signRS256 builds and signs a JWT with the given private key and optional
// custom claims. Defaults: iss=testIssuer, aud=testAudience, exp=+1h.
func signRS256(t *testing.T, priv *rsa.PrivateKey, opts ...func(jwt.Token)) string {
	t.Helper()
	tok, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{testAudience}).
		Subject("user-abc").
		JwtID("tok-123").
		Expiration(time.Now().Add(time.Hour)).
		IssuedAt(time.Now()).
		Build()
	require.NoError(t, err)

	for _, opt := range opts {
		opt(tok)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))
	require.NoError(t, err)
	return string(signed)
}

func newValidator(t *testing.T, ks jwk.Set) *JWTValidator {
	t.Helper()
	v := NewJWTValidator("https://auth.clawde.test/.well-known/jwks.json", testIssuer, testAudience)
	v.InjectTestKeySet(ks)
	return v
}

// ---- happy-path ----

func TestJWTValidator_ValidRS256(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newValidator(t, ks)

	raw := signRS256(t, priv, func(tok jwt.Token) {
		require.NoError(t, tok.Set(ClaimWorkspaceID, "ws-uuid-001"))
		require.NoError(t, tok.Set(ClaimTier, string(TierPro)))
	})

	claims, err := v.Validate(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, "user-abc", claims.Sub)
	assert.Equal(t, "ws-uuid-001", claims.WorkspaceID)
	assert.Equal(t, TierPro, claims.Tier)
	assert.Equal(t, "tok-123", claims.TokenID)
}

func TestJWTValidator_DefaultTierFree(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newValidator(t, ks)

	raw := signRS256(t, priv) // no tier claim
	claims, err := v.Validate(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, TierFree, claims.Tier)
}

// ---- rejection cases ----

func TestJWTValidator_ExpiredToken(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newValidator(t, ks)

	tok, _ := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{testAudience}).
		Subject("user-abc").
		Expiration(time.Now().Add(-time.Hour)).
		Build()
	signed, _ := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))

	_, err := v.Validate(context.Background(), string(signed))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
}

func TestJWTValidator_WrongAudience(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newValidator(t, ks)

	tok, _ := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{"wrong-service"}).
		Subject("u").
		Expiration(time.Now().Add(time.Hour)).
		Build()
	signed, _ := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))

	_, err := v.Validate(context.Background(), string(signed))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
}

func TestJWTValidator_WrongIssuer(t *testing.T) {
	priv, ks := testKeyPair(t)
	// Validator configured with testIssuer; token has different issuer.
	v := NewJWTValidator("", testIssuer, testAudience)
	v.InjectTestKeySet(ks)

	tok, _ := jwt.NewBuilder().
		Issuer("https://evil.example.com").
		Audience([]string{testAudience}).
		Subject("u").
		Expiration(time.Now().Add(time.Hour)).
		Build()
	signed, _ := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))

	_, err := v.Validate(context.Background(), string(signed))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
}

func TestJWTValidator_InvalidSignature(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newValidator(t, ks)

	// Sign with a DIFFERENT key not in the JWKS.
	otherPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
	raw := signRS256(t, otherPriv)

	_, err := v.Validate(context.Background(), raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
}

func TestJWTValidator_HS256Rejected(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newValidator(t, ks)

	// Build an HS256 token manually.
	hmacSecret := []byte("super-secret-key-that-is-long-enough-32b")
	tok, _ := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{testAudience}).
		Subject("u").
		Expiration(time.Now().Add(time.Hour)).
		Build()
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256, hmacSecret))
	require.NoError(t, err)

	_, err = v.Validate(context.Background(), string(signed))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
	assert.Contains(t, err.Error(), "HS256")
}

func TestJWTValidator_NoneAlgorithmRejected(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newValidator(t, ks)

	// Craft a JWT-like string with alg=none in header.
	// header = {"alg":"none","typ":"JWT"}, payload = {"sub":"u","exp":9999999999}
	// Compact: header.payload. (empty signature segment)
	noneHeader := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"
	nonePayload := "eyJzdWIiOiJ1IiwiaXNzIjoiaHR0cHM6Ly9hdXRoLmNsYXdkZS50ZXN0IiwiYXVkIjoiY2xhd2RlLWludGVsbGlnZW5jZSIsImV4cCI6OTk5OTk5OTk5OX0"
	noneToken := noneHeader + "." + nonePayload + "."

	_, err := v.Validate(context.Background(), noneToken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
}

func TestJWTValidator_JWKSFailClosedOnExpiry(t *testing.T) {
	// Validator with a bogus JWKS URL and no injected key set — simulates
	// JWKS fetch failure. Must return an error (fail-closed), not pass through.
	v := NewJWTValidator("https://nonexistent.clawde.test/jwks.json", testIssuer, testAudience)
	// Do NOT call InjectTestKeySet — key set is nil, fetch will fail.

	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	raw := signRS256(t, priv)

	_, err := v.Validate(context.Background(), raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH_FAILED")
	assert.Contains(t, err.Error(), "JWKS unavailable")
}

// ---- tier daily limits ----

func TestTierDailyLimits(t *testing.T) {
	assert.Equal(t, 100, TierFree.DailyLimit())
	assert.Equal(t, 10_000, TierPro.DailyLimit())
	assert.Equal(t, -1, TierEnterprise.DailyLimit())
}
