//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestPolicyGroupListAgainstCincZero asserts the seeded policy-group
// index is returned in both formats. The cinc-zero seed contains a
// single "prod" policy group.
func TestPolicyGroupListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "policy-group", "list", "--config", env.cfgPath)
	if human != "prod\n" {
		t.Errorf("policy-group list (human) = %q, want \"prod\\n\"", human)
	}

	jsonOut := runCinc(t, env.binary, "policy-group", "list", "--config", env.cfgPath, "--format", "json")
	if !strings.Contains(jsonOut, "prod") {
		t.Errorf("policy-group list (json) missing prod\ngot: %s", jsonOut)
	}
}

// TestPolicyGroupShowAgainstCincZero fetches the seeded policy group
// and asserts the active policy revision is present in both formats.
func TestPolicyGroupShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "policy-group", "show", "prod", "--config", env.cfgPath)
	if !strings.Contains(human, "appserver") {
		t.Errorf("policy-group show (human) missing appserver:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "policy-group", "show", "prod", "--config", env.cfgPath, "--format", "json")
	var got cinc.PolicyGroup
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("policy-group show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	rev, ok := got.Policies["appserver"]
	if !ok {
		t.Fatalf("policy-group show policies = %v, want an appserver entry", got.Policies)
	}
	if rev.RevisionID != "1.0.0" {
		t.Errorf("appserver revision = %q, want 1.0.0", rev.RevisionID)
	}
}

// TestPolicyGroupDeleteAgainstCincZero deletes the seeded policy group
// and asserts it disappears from the index.
func TestPolicyGroupDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "policy-group", "delete", "prod", "--config", env.cfgPath)
	if out != "Deleted policy group \"prod\"\n" {
		t.Errorf("policy-group delete output = %q", out)
	}

	after := runCinc(t, env.binary, "policy-group", "list", "--config", env.cfgPath)
	if after != "" {
		t.Errorf("policy-group list after delete = %q, want empty", after)
	}
}
