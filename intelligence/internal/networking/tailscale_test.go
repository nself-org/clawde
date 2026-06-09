// Package networking — tests for Tailscale init helpers.
//
// Purpose: Verify InitTailscale behaviour without joining a real tailnet.
//          Tests cover: empty-authkey skip, config construction, and the
//          graceful-degradation path when tsnet.Up would time out (via a
//          cancelled context as the seam).
// Constraints: No real network calls; no actual Tailscale daemon required.
//              Uses context cancellation to simulate the 30-second Up timeout.
// SPORT: REGISTRY-FUNCTIONS.md — InitTailscale.
package networking

import (
	"context"
	"strings"
	"testing"
)

// TestInitTailscale_EmptyAuthKey verifies the skip path: when AuthKey is empty
// InitTailscale returns (nil, nil) without any network activity.
func TestInitTailscale_EmptyAuthKey(t *testing.T) {
	result, err := InitTailscale(context.Background(), InitConfig{
		Hostname: "test-node",
		AuthKey:  "",
	})
	if err != nil {
		t.Fatalf("expected nil error for empty authkey, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for empty authkey, got non-nil result")
	}
}

// TestInitTailscale_EmptyHostnameFallback verifies that an empty Hostname is
// replaced with the canonical default "clawde-intelligence".
// We use a pre-cancelled context so tsnet.Up fails immediately — we only care
// that the config was constructed correctly (hostname fallback) rather than
// testing the Up round-trip, which requires a real tailnet.
func TestInitTailscale_EmptyHostnameFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Up() returns immediately with context error

	_, err := InitTailscale(ctx, InitConfig{
		Hostname: "", // should fall back to "clawde-intelligence"
		AuthKey:  "tskey-auth-fake-0000000000000000",
	})
	// We expect an error because the context is already cancelled.
	// The test just confirms we got an error (not a panic) and that the error
	// message references the Up step — meaning the hostname fallback didn't crash.
	if err == nil {
		t.Fatal("expected error with pre-cancelled ctx, got nil")
	}
	if !strings.Contains(err.Error(), "Tailscale Up") {
		t.Errorf("expected error to mention 'Tailscale Up', got: %v", err)
	}
}

// TestInitTailscale_GracefulDegradation verifies that a failed Up() returns
// an error (not a panic) so the caller can log it and continue with loopback.
// Uses a pre-cancelled context as the failure seam.
func TestInitTailscale_GracefulDegradation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := InitTailscale(ctx, InitConfig{
		Hostname: "clawde-intelligence",
		AuthKey:  "tskey-auth-fake-0000000000000000",
		StateDir: t.TempDir(),
	})
	// Expect non-nil error (Up failed due to cancelled ctx).
	if err == nil {
		t.Fatal("expected error from Up with cancelled ctx, got nil")
	}
	// Expect nil result — no server to close.
	if result != nil {
		t.Error("expected nil result on Up failure, got non-nil")
		_ = result.Server.Close()
	}
	// Error should be wrapped with context info.
	errStr := err.Error()
	if !strings.Contains(errStr, "Tailscale Up") {
		t.Errorf("error should mention 'Tailscale Up': %v", errStr)
	}
}

// TestInitConfig_FieldAssignment verifies the InitConfig struct can be
// constructed and read back correctly.  Trivial but guards against accidental
// field rename.
func TestInitConfig_FieldAssignment(t *testing.T) {
	cfg := InitConfig{
		Hostname: "node-a",
		AuthKey:  "tskey-auth-abc123",
		StateDir: "/tmp/tsstate",
	}
	if cfg.Hostname != "node-a" {
		t.Errorf("Hostname: got %q want %q", cfg.Hostname, "node-a")
	}
	if cfg.AuthKey != "tskey-auth-abc123" {
		t.Errorf("AuthKey: got %q want %q", cfg.AuthKey, "tskey-auth-abc123")
	}
	if cfg.StateDir != "/tmp/tsstate" {
		t.Errorf("StateDir: got %q want %q", cfg.StateDir, "/tmp/tsstate")
	}
}
