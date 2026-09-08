//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestPolicyListAgainstCincZero asserts the seeded policy index is
// returned in both formats. The cinc-zero seed contains a single
// "appserver" policy.
func TestPolicyListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "policy", "list", "--config", env.cfgPath)
	if human != "appserver\n" {
		t.Errorf("policy list (human) = %q, want \"appserver\\n\"", human)
	}

	jsonOut := runCinc(t, env.binary, "policy", "list", "--config", env.cfgPath, "--format", "json")
	if !strings.Contains(jsonOut, "appserver") {
		t.Errorf("policy list (json) missing appserver\ngot: %s", jsonOut)
	}
}

// TestPolicyShowAgainstCincZero fetches the seeded policy and asserts
// its single revision is present in both output formats.
func TestPolicyShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "policy", "show", "appserver", "--config", env.cfgPath)
	if !strings.Contains(human, "1.0.0") {
		t.Errorf("policy show (human) missing revision 1.0.0:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "policy", "show", "appserver", "--config", env.cfgPath, "--format", "json")
	var got cinc.PolicyRevisions
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("policy show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if _, ok := got.Revisions["1.0.0"]; !ok {
		t.Errorf("policy show revisions = %v, want a 1.0.0 entry", got.Revisions)
	}
}

// TestPolicyDeleteAgainstCincZero deletes the seeded policy and asserts
// it disappears from the index.
func TestPolicyDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "policy", "delete", "appserver", "--config", env.cfgPath)
	if out != "Deleted policy \"appserver\"\n" {
		t.Errorf("policy delete output = %q", out)
	}

	after := runCinc(t, env.binary, "policy", "list", "--config", env.cfgPath)
	if after != "" {
		t.Errorf("policy list after delete = %q, want empty", after)
	}
}
