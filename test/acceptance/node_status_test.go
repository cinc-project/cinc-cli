//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestNodeStatusAgainstCincZero confirms `node status` reports the seeded
// nodes. They carry no ohai_time, so they show as never having checked in.
func TestNodeStatusAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "status", "--config", env.cfgPath)
	for _, name := range []string{"db01", "web01", "web02"} {
		if !strings.Contains(out, name) {
			t.Errorf("node status missing %q\ngot: %s", name, out)
		}
	}
	if !strings.Contains(out, "never") {
		t.Errorf("seeded nodes have no check-in; expected 'never'\ngot: %s", out)
	}
}

// TestNodeEnvironmentSetAgainstCincZero sets a seeded node's environment and
// confirms it persisted.
func TestNodeEnvironmentSetAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "environment-set", "web01", "staging", "--config", env.cfgPath)
	if !strings.Contains(out, `node "web01" environment to "staging"`) {
		t.Fatalf("environment-set output = %q", out)
	}

	show := runCinc(t, env.binary, "node", "show", "web01", "--config", env.cfgPath, "--format", "json")
	var node cinc.Node
	if err := json.Unmarshal([]byte(show), &node); err != nil {
		t.Fatalf("node show not valid JSON: %v\n%s", err, show)
	}
	if node.Environment != "staging" {
		t.Errorf("node environment = %q, want staging", node.Environment)
	}
}

// TestNodePolicySetAgainstCincZero points a seeded node at a policy and
// confirms policy_name/policy_group persisted.
func TestNodePolicySetAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "policy-set", "web02", "prod", "appserver", "--config", env.cfgPath)
	if !strings.Contains(out, `node "web02" to policy "appserver" in group "prod"`) {
		t.Fatalf("policy-set output = %q", out)
	}

	show := runCinc(t, env.binary, "node", "show", "web02", "--config", env.cfgPath, "--format", "json")
	var node cinc.Node
	if err := json.Unmarshal([]byte(show), &node); err != nil {
		t.Fatalf("node show not valid JSON: %v\n%s", err, show)
	}
	if node.PolicyName != "appserver" || node.PolicyGroup != "prod" {
		t.Errorf("node policy = %q/%q, want appserver/prod", node.PolicyName, node.PolicyGroup)
	}
}
