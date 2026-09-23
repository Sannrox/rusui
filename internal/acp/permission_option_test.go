package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDenyNeverSelectsAnAllowOption(t *testing.T) {
	allowOnly := []PermOption{{OptionID: "allow-once", Kind: "allow_once"}, {OptionID: "allow-always", Kind: "allow_always"}}
	mislabelled := []PermOption{{OptionID: "reject-once", Kind: "allow_always"}}
	for name, opts := range map[string][]PermOption{"allow only": allowOnly, "mislabelled": mislabelled, "none": nil} {
		if got := pickOption(opts, false); got != "reject-once" {
			t.Fatalf("%s: deny answered %q, want synthesized reject-once", name, got)
		}
	}
	both := []PermOption{{OptionID: "allow-once", Kind: "allow_once"}, {OptionID: "reject-once", Kind: "reject_once"}}
	if got := pickOption(both, false); got != "reject-once" {
		t.Fatalf("deny with reject offered: %q", got)
	}
	if got := pickOption(both, true); got != "allow-once" {
		t.Fatalf("allow: %q", got)
	}
	if got := pickOption([]PermOption{{OptionID: "reject-once", Kind: "reject_once"}}, true); got != "allow-once" {
		t.Fatalf("allow with only reject offered: %q", got)
	}
}

// answerFor runs the client's permission answer path for a gate decision
// against guest-controlled options.
func answerFor(t *testing.T, gate PermissionGate, cmd string, opts []PermOption) string {
	t.Helper()
	var out strings.Builder
	c := &Client{Out: &out, Perm: gate}
	raw, _ := json.Marshal(map[string]string{"command": cmd})
	params, _ := json.Marshal(PermissionParams{SessionID: "s", ToolCall: raw, Options: opts})
	c.answerPermission(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: MethodRequestPermission, Params: params})
	var reply struct {
		Result PermissionOutcome `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &reply); err != nil {
		t.Fatalf("reply %q: %v", out.String(), err)
	}
	return reply.Result.Outcome.OptionID
}

func TestFenceAndRejectRulesHoldAgainstAllowOnlyOptions(t *testing.T) {
	allowOnly := []PermOption{{OptionID: "allow-once", Kind: "allow_once"}}
	fence := FenceGate{Next: RulesGate{Rules: []Rule{{Command: "gh"}}}}
	if got := answerFor(t, fence, "gh pr merge 1", allowOnly); got != "reject-once" {
		t.Fatalf("fence deny answered %q", got)
	}
	rules := RulesGate{Rules: []Rule{{Command: "npm"}, {Command: "npm publish", Action: RuleReject}}}
	if got := answerFor(t, rules, "npm publish", allowOnly); got != "reject-once" {
		t.Fatalf("reject rule answered %q", got)
	}
	if got := answerFor(t, fence, "gh pr create", allowOnly); got != "allow-once" {
		t.Fatalf("allowed command answered %q", got)
	}
}
