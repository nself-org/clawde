// Package networking — Tailscale mesh networking for clawde-intelligence.
//
// Purpose: Optionally join a Tailscale network so other clawde-client nodes
//          (tagged tag:clawde-client) can reach clawde-intelligence services
//          (tagged tag:clawde-intelligence) over an encrypted mesh, in addition
//          to the existing loopback-only 127.0.0.1 listeners.
// Inputs:  InitConfig{Hostname, AuthKey, StateDir}.
// Outputs: *tsnet.Server (caller must call Close on shutdown), Tailscale IP string.
//          error — non-nil on Up timeout or network failure.
// Constraints:
//   - TAILSCALE_AUTHKEY unset → skip entirely; caller checks AuthKey before calling.
//   - Up timeout: 30 seconds; on timeout return wrapped error (caller logs + continues).
//   - Never binds 0.0.0.0; tsnet.Server manages its own virtual interface.
//   - ACL policy (managed outside this binary, documented below):
//       tag:clawde-client → tag:clawde-intelligence : tcp 8090
//
// Tailscale ACL policy excerpt (add to your tailnet ACL in the admin console):
//
//	{
//	  "acls": [
//	    {
//	      "action": "accept",
//	      "src": ["tag:clawde-client"],
//	      "dst": ["tag:clawde-intelligence:8090"]
//	    }
//	  ],
//	  "tagOwners": {
//	    "tag:clawde-client":       ["autogroup:member"],
//	    "tag:clawde-intelligence": ["autogroup:member"]
//	  }
//	}
//
// SPORT: REGISTRY-SERVICES.md — Tailscale mesh, ACL tags.
//        REGISTRY-FUNCTIONS.md — InitTailscale.
package networking

import (
	"context"
	"fmt"
	"net"
	"time"

	"tailscale.com/tsnet"
)

// InitConfig holds all parameters for InitTailscale.
type InitConfig struct {
	// Hostname is the Tailscale node hostname (CLAWDE_TAILSCALE_HOSTNAME).
	// Falls back to "clawde-intelligence" if empty.
	Hostname string
	// AuthKey is the Tailscale auth key (TAILSCALE_AUTHKEY).
	// When empty, InitTailscale returns (nil, "", nil) — caller skips Tailscale.
	AuthKey string
	// StateDir is an optional directory for tsnet state persistence.
	// Empty string lets tsnet choose a default under the OS cache dir.
	StateDir string
}

// InitResult is the successful result of joining the Tailscale network.
type InitResult struct {
	// Server is the live tsnet node. Caller MUST call Server.Close() on shutdown.
	Server *tsnet.Server
	// TailscaleIP is the primary IPv4 Tailscale address (100.x.y.z) as a string,
	// suitable for logging and for constructing the Tailscale gRPC listen address.
	TailscaleIP string
}

// InitTailscale constructs and brings up a tsnet node.
//
// Returns (nil, nil) when AuthKey is empty — no Tailscale, no error.
// Returns a non-nil error when Up() times out or fails; the caller should log
// the error and continue with loopback-only listeners (graceful degradation).
//
// On success the returned InitResult.Server is live; caller must call Close()
// on shutdown.
func InitTailscale(ctx context.Context, cfg InitConfig) (*InitResult, error) {
	if cfg.AuthKey == "" {
		return nil, nil //nolint:nilnil // intentional: empty authkey = skip
	}

	hostname := cfg.Hostname
	if hostname == "" {
		hostname = "clawde-intelligence"
	}

	s := &tsnet.Server{
		Hostname: hostname,
		AuthKey:  cfg.AuthKey,
	}
	if cfg.StateDir != "" {
		s.Dir = cfg.StateDir
	}

	upCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := s.Up(upCtx); err != nil {
		_ = s.Close() // best-effort cleanup
		return nil, fmt.Errorf("networking: Tailscale Up: %w", err)
	}

	ip4, _ := s.TailscaleIPs()
	ipStr := ip4.String()
	if !ip4.IsValid() {
		ipStr = "<no-ipv4>"
	}

	return &InitResult{
		Server:      s,
		TailscaleIP: ipStr,
	}, nil
}

// ListenTCP is a helper that calls s.Listen("tcp", addr) and returns a
// net.Listener.  Provided so callers do not need to import tsnet directly.
func ListenTCP(s *tsnet.Server, addr string) (net.Listener, error) {
	return s.Listen("tcp", addr)
}
