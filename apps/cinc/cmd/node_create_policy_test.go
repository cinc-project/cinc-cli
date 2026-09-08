package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// nodeCreateServer captures the POST body sent to create a node.
func nodeCreateServer(t *testing.T, got *cinc.Node) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(got)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNodeCreateWithPolicy(t *testing.T) {
	var created cinc.Node
	srv := nodeCreateServer(t, &created)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "create", "web01", "--policy-group", "prod", "--policy-name", "base", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node create --policy-*: %v", err)
	}
	if created.PolicyName != "base" || created.PolicyGroup != "prod" {
		t.Errorf("server received policy_name=%q policy_group=%q, want base/prod", created.PolicyName, created.PolicyGroup)
	}
	if len(created.RunList) != 0 {
		t.Errorf("a policy-managed node should have an empty run_list, got %v", created.RunList)
	}
}

func TestNodeCreatePolicyFlagsRequireBoth(t *testing.T) {
	for _, args := range [][]string{
		{"node", "create", "web01", "--policy-name", "base"},
		{"node", "create", "web01", "--policy-group", "prod"},
	} {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append(args, "--config", writeNodeConfig(t, "http://127.0.0.1:0")))
		if err := root.Execute(); err == nil {
			t.Errorf("args %v: expected an error when only one policy flag is given", args)
		}
	}
}

func TestNodeCreatePolicyConflictsWithRunListOrEnvironment(t *testing.T) {
	// Policyfile management and run-list/environment management are mutually
	// exclusive — combining the policy flags with either must error.
	cases := map[string][]string{
		"run-list":    {"--run-list", "recipe[base]"},
		"environment": {"--environment", "prod"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			args := append([]string{
				"node", "create", "web01",
				"--policy-name", "base", "--policy-group", "prod",
			}, extra...)
			args = append(args, "--config", writeNodeConfig(t, "http://127.0.0.1:0"))
			root := newRootCmd()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			if err := root.Execute(); err == nil {
				t.Errorf("expected an error when policy flags are combined with --%s", name)
			}
		})
	}
}
