//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// nodeRunList fetches a node and returns its run list via `node show --json`.
func nodeRunList(t *testing.T, env acceptanceEnv, name string) []string {
	t.Helper()
	out := runCinc(t, env.binary, "node", "show", name, "--config", env.cfgPath, "--format", "json")
	var node cinc.Node
	if err := json.Unmarshal([]byte(out), &node); err != nil {
		t.Fatalf("node show (json) not valid JSON: %v\n%s", err, out)
	}
	return node.RunList
}

// TestNodeRunListAgainstCincZero exercises add/remove/set against a seeded
// node and confirms each change is persisted on the server.
func TestNodeRunListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	runCinc(t, env.binary, "node", "run-list", "set", "web01", "recipe[base],role[web]", "--config", env.cfgPath)
	if got := nodeRunList(t, env, "web01"); strings.Join(got, ",") != "recipe[base],role[web]" {
		t.Fatalf("after set, run list = %v, want [recipe[base] role[web]]", got)
	}

	runCinc(t, env.binary, "node", "run-list", "add", "web01", "recipe[ntp]", "--config", env.cfgPath)
	if got := nodeRunList(t, env, "web01"); strings.Join(got, ",") != "recipe[base],role[web],recipe[ntp]" {
		t.Fatalf("after add, run list = %v, want recipe[ntp] appended", got)
	}

	out := runCinc(t, env.binary, "node", "run-list", "remove", "web01", "role[web]", "--config", env.cfgPath)
	if !strings.Contains(out, "recipe[base]") || strings.Contains(out, "role[web]") {
		t.Errorf("remove output = %q, want role[web] gone", out)
	}
	if got := nodeRunList(t, env, "web01"); strings.Join(got, ",") != "recipe[base],recipe[ntp]" {
		t.Errorf("after remove, run list = %v, want role[web] gone", got)
	}

	// The read-only list verb reports the same entries without changing them.
	listed := runCinc(t, env.binary, "node", "run-list", "list", "web01", "--config", env.cfgPath)
	if !strings.Contains(listed, "recipe[base]") || !strings.Contains(listed, "recipe[ntp]") {
		t.Errorf("run-list list = %q, want both remaining entries", listed)
	}
}

// TestNodeTagAgainstCincZero exercises add/list/remove of node tags and
// confirms they round-trip through the node's normal attributes.
func TestNodeTagAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	runCinc(t, env.binary, "node", "tag", "add", "db01", "prod,primary", "--config", env.cfgPath)

	list := runCinc(t, env.binary, "node", "tag", "list", "db01", "--config", env.cfgPath)
	if !strings.Contains(list, "prod") || !strings.Contains(list, "primary") {
		t.Errorf("tag list = %q, want both tags", list)
	}

	runCinc(t, env.binary, "node", "tag", "remove", "db01", "primary", "--config", env.cfgPath)
	after := runCinc(t, env.binary, "node", "tag", "list", "db01", "--config", env.cfgPath)
	if !strings.Contains(after, "prod") || strings.Contains(after, "primary") {
		t.Errorf("tag list after remove = %q, want only prod", after)
	}

	// set replaces the whole tag set wholesale.
	runCinc(t, env.binary, "node", "tag", "set", "db01", "alpha,beta", "--config", env.cfgPath)
	final := runCinc(t, env.binary, "node", "tag", "list", "db01", "--config", env.cfgPath)
	if !strings.Contains(final, "alpha") || !strings.Contains(final, "beta") || strings.Contains(final, "prod") {
		t.Errorf("tag list after set = %q, want only alpha,beta", final)
	}
}
