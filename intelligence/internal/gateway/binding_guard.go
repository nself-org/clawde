// Package gateway — M6 loopback binding guard for vLLM.
//
// Purpose: Enforce that the vLLM host resolves to a loopback address only.
//          Prevents accidental exposure of the GPU inference lane to non-loopback
//          interfaces, satisfying the M6 security constraint from ADR-001.
// Inputs:  host string — the base URL or bare host[:port] as configured in
//          model_registry.yaml or the VLLM_HOST environment variable.
// Outputs: nil when the resolved IP is loopback (127.x.x.x or ::1);
//          a descriptive error otherwise.
// Constraints: Only the IP portion is validated; port is stripped if present.
//              "localhost" resolves via net.ResolveTCPAddr, which honors /etc/hosts.
// SPORT: REGISTRY-FUNCTIONS.md → ValidateVLLMHost.
package gateway

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateVLLMHost returns nil when host resolves to a loopback address,
// or a descriptive M6-violation error otherwise.
//
// Accepted host forms:
//   - bare IP:                "127.0.0.1", "::1"
//   - IP + port:              "127.0.0.1:8093"
//   - full URL with scheme:   "http://127.0.0.1:8093/v1"
//   - hostname (resolved):    "localhost" → 127.0.0.1 (pass)
func ValidateVLLMHost(host string) error {
	bare := extractHost(host)
	if bare == "" {
		return fmt.Errorf("M6 violation: vLLM host %q is empty or unparseable", host)
	}

	// net.ResolveTCPAddr handles both IPv4 and IPv6 (including bracket notation).
	addr, err := net.ResolveTCPAddr("tcp", ensurePort(bare))
	if err != nil {
		return fmt.Errorf("M6 violation: vLLM host %q cannot be resolved: %w", host, err)
	}

	ip := addr.IP
	if ip == nil {
		// ResolveTCPAddr returned no IP — treat as non-loopback.
		return fmt.Errorf("M6 violation: vLLM host %s is not loopback", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("M6 violation: vLLM host %s is not loopback", host)
	}
	return nil
}

// extractHost strips scheme, path and query from a potentially full URL,
// returning only the host[:port] portion suitable for net.ResolveTCPAddr.
func extractHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// If it looks like a URL with a scheme, parse it properly.
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return u.Host // host[:port], no scheme or path
	}
	// Bare "host" or "host:port" — return as-is.
	return raw
}

// ensurePort appends a dummy port when none is present so net.ResolveTCPAddr
// does not reject the address with "missing port in address".
func ensurePort(hostPort string) string {
	// Already has both bracket notation and a port: "[::1]:8093" → leave as-is.
	if strings.HasPrefix(hostPort, "[") {
		// Check if there's a port after the closing bracket.
		closeBracket := strings.LastIndex(hostPort, "]")
		if closeBracket >= 0 && closeBracket < len(hostPort)-1 {
			// e.g. "[::1]:8093" — already has port.
			return hostPort
		}
		// "[::1]" with no port — append ":0".
		return hostPort + ":0"
	}
	// Count colons: more than one means bare IPv6 without brackets — wrap and add port.
	if strings.Count(hostPort, ":") > 1 {
		return "[" + hostPort + "]:0"
	}
	// Has exactly one colon → host:port already set (IPv4 with port, or "localhost:N").
	if strings.Contains(hostPort, ":") {
		return hostPort
	}
	// No colon at all — bare host or bare IPv4.
	return hostPort + ":0"
}
