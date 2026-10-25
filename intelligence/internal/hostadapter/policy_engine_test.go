// policy_engine_test.go — deny-by-default tests for the ADR-003 PolicyEngine.
// These fail against the former AllowAll stub (which permitted everything).
package hostadapter

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPolicyEngine_DenyByDefault(t *testing.T) {
	p := NewPolicyEngine(
		PolicyEntry{ClientID: "elevated", Level: TrustElevated},
		PolicyEntry{ClientID: "limited", Level: TrustLimited},
		PolicyEntry{ClientID: "none", Level: TrustNone},
		PolicyEntry{ClientID: "override", Level: TrustLimited, ToolPerms: map[string]bool{
			toolListSymbols: false, "ingest_doc_url": true,
		}},
	)
	cases := []struct {
		name, client, tool string
		allow              bool
	}{
		{"unknown caller denied", "stranger", toolRetrieveContext, false},
		{"empty caller denied", "", toolRetrieveContext, false},
		{"unknown tool denied even at max trust", "elevated", "rm_rf", false},
		{"elevated allowed tier-1", "elevated", toolRetrieveContext, true},
		{"elevated allowed tier-2", "elevated", "ingest_doc_url", true},
		{"limited allowed tier-1", "limited", toolListSymbols, true},
		{"limited denied tier-2", "limited", "ingest_doc_url", false},
		{"level 0 denied everything", "none", toolRetrieveContext, false},
		{"explicit deny beats default allow", "override", toolListSymbols, false},
		{"explicit allow beats default deny", "override", "ingest_doc_url", true},
		{"explicit allow cannot invent unknown tool", "override", "rm_rf", false},
	}
	for _, c := range cases {
		err := p.Evaluate(context.Background(), c.client, c.tool, "ws")
		if c.allow && err != nil {
			t.Errorf("%s: want allow, got %v", c.name, err)
		}
		if !c.allow && !errors.Is(err, ErrPolicyDenied) {
			t.Errorf("%s: want ErrPolicyDenied, got %v", c.name, err)
		}
	}
}

// CLAWDE_POLICY_ENABLED used to be read by the stub and always allowed. No value
// of it may now turn an unknown caller into an allowed one.
func TestPolicyEngine_EnvCannotDisable(t *testing.T) {
	p := NewPolicyEngine(PolicyEntry{ClientID: "known", Level: TrustElevated})
	for _, v := range []string{"", "true", "1", "false", "0", "off"} {
		t.Setenv("CLAWDE_POLICY_ENABLED", v)
		if err := p.Evaluate(context.Background(), "stranger", toolRetrieveContext, "ws"); err == nil {
			t.Errorf("CLAWDE_POLICY_ENABLED=%q allowed an unknown caller", v)
		}
	}
}

// A low-trust server identity must be refused at step 3 with a policy error.
func TestMCPServer_PolicyDeniesLowTrustCaller(t *testing.T) {
	s := newTestServer(t, "mobile", &mcpMockSource{rc: &RetrievedContext{}})
	s.policy = NewPolicyEngine(PolicyEntry{ClientID: "mobile", Level: TrustNone})
	resps := drive(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"retrieve_context","arguments":{"query":"x"}}}`)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("want one error response, got %+v", resps)
	}
	if resps[0].Error.Code != rpcAccessDenied || !strings.Contains(resps[0].Error.Message, "denied by policy") {
		t.Errorf("want policy denial, got %+v", resps[0].Error)
	}
}
