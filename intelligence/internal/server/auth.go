// Package server — HMAC-SHA256 authentication middleware for GatewayService.
//
// Purpose: Validate every incoming gRPC call (except Health) using a
//          shared HMAC-SHA256 signature. Prevents replay attacks via a
//          ±30-second timestamp window.
// Inputs:  gRPC metadata headers: X-ClawDE-Timestamp (Unix seconds),
//          X-ClawDE-Signature (see below).
// Outputs: gRPC Status error on failure; passes through on success.
// Signature scheme:
//   sig = HMAC-SHA256(key=secret, msg=timestamp + "." + hex(SHA256(body)))
//   header value: "HMAC-SHA256 " + hex(sig)
// Constraints: Secret read only from CLAWDE_GATEWAY_HMAC_SECRET env var.
//              Secret is NEVER logged, printed, or included in errors.
//              /Health bypasses HMAC entirely.
// SPORT: REGISTRY-SERVICES.md — auth=HMAC-SHA256.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	hmacTimestampHeader  = "x-clawde-timestamp"
	hmacSignatureHeader  = "x-clawde-signature"
	hmacWindowSeconds    = 30
	hmacSecretEnvVar     = "CLAWDE_GATEWAY_HMAC_SECRET"
	healthFullMethod     = "/gateway.v1.GatewayService/Health"
)

// HMACSecret returns the HMAC secret from the environment.
// Returns an error if the variable is unset or empty.
// The returned secret is a []byte so it never appears as a string in logs.
func HMACSecret() ([]byte, error) {
	s := os.Getenv(hmacSecretEnvVar)
	if s == "" {
		return nil, fmt.Errorf("CLAWDE_GATEWAY_HMAC_SECRET is not set")
	}
	return []byte(s), nil
}

// ComputeSignature computes the expected HMAC-SHA256 signature string for a
// given timestamp (Unix seconds string) and body SHA256 hex digest.
//
// Format: "HMAC-SHA256 " + hex(HMAC-SHA256(secret, timestamp + "." + bodySHA256Hex))
func ComputeSignature(secret []byte, timestampStr, bodySHA256Hex string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestampStr + "." + bodySHA256Hex))
	return "HMAC-SHA256 " + hex.EncodeToString(mac.Sum(nil))
}

// BodySHA256Hex returns the lowercase hex SHA-256 of body.
func BodySHA256Hex(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// authError returns a gRPC AUTH_FAILED error without leaking internal details.
func authError(detail string) error {
	return status.Errorf(codes.Unauthenticated, "AUTH_FAILED: %s", detail)
}

// UnaryHMACInterceptor is a gRPC UnaryServerInterceptor that enforces HMAC auth.
// The Health method bypasses authentication.
func UnaryHMACInterceptor(secret []byte) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if info.FullMethod == healthFullMethod {
			return handler(ctx, req)
		}
		if err := validateHMAC(ctx, secret); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamHMACInterceptor is a gRPC StreamServerInterceptor that enforces HMAC auth.
// StreamComplete sends body as the serialised request bytes; for the stream
// interceptor we validate the metadata headers present on the initial call.
func StreamHMACInterceptor(secret []byte) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Health is unary, so this branch is a safety guard only.
		if info.FullMethod == healthFullMethod {
			return handler(srv, ss)
		}
		if err := validateHMAC(ss.Context(), secret); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// validateHMAC extracts and validates the HMAC headers from the gRPC metadata.
func validateHMAC(ctx context.Context, secret []byte) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return authError("missing metadata")
	}

	// ---- timestamp ----
	timestamps := md.Get(hmacTimestampHeader)
	if len(timestamps) == 0 {
		return authError("missing " + hmacTimestampHeader)
	}
	tsStr := timestamps[0]
	tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return authError("invalid timestamp")
	}
	now := time.Now().Unix()
	diff := now - tsUnix
	if diff < 0 {
		diff = -diff
	}
	if diff > hmacWindowSeconds {
		return authError("timestamp expired")
	}

	// ---- signature ----
	sigs := md.Get(hmacSignatureHeader)
	if len(sigs) == 0 {
		return authError("missing " + hmacSignatureHeader)
	}
	provided := sigs[0]

	// The body SHA256 is carried in x-clawde-body-sha256 by the client.
	// If absent we use the empty-body digest (for empty-body RPCs).
	bodySHA256Vals := md.Get("x-clawde-body-sha256")
	bodySHA256Hex := BodySHA256Hex([]byte{}) // default: empty body
	if len(bodySHA256Vals) > 0 && bodySHA256Vals[0] != "" {
		bodySHA256Hex = bodySHA256Vals[0]
	}

	expected := ComputeSignature(secret, tsStr, bodySHA256Hex)

	// Constant-time compare to prevent timing attacks.
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return authError("signature mismatch")
	}

	return nil
}

// ValidateSignatureString is a helper for tests and the HTTP middleware.
// It validates a raw provided signature string against the expected value,
// given the secret, timestamp string, and body bytes.
func ValidateSignatureString(secret []byte, timestampStr string, body []byte, provided string) error {
	tsUnix, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("AUTH_FAILED: invalid timestamp")
	}
	now := time.Now().Unix()
	diff := now - tsUnix
	if diff < 0 {
		diff = -diff
	}
	if diff > hmacWindowSeconds {
		return fmt.Errorf("AUTH_FAILED: timestamp expired")
	}
	bodySHA256Hex := BodySHA256Hex(body)
	expected := ComputeSignature(secret, timestampStr, bodySHA256Hex)
	if !strings.EqualFold(provided, expected) && !hmac.Equal([]byte(provided), []byte(expected)) {
		return fmt.Errorf("AUTH_FAILED: signature mismatch")
	}
	return nil
}
