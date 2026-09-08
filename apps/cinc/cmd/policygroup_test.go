package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// policyGroupServer starts an httptest server that serves a
// policy-group index for org "acme" and returns the server.
func policyGroupServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	type entry struct {
		URI      string         `json:"uri"`
		Policies map[string]any `json:"policies,omitempty"`
	}
	index := make(map[string]entry, len(names))
	for _, n := range names {
		index[n] = entry{URI: "https://example.test/policy_groups/" + n}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policy_groups", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchPolicyGroupNamesReturnsSortedNames(t *testing.T) {
	srv := policyGroupServer(t, "prod", "dev", "stage")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: srv.URL, Org: "acme", ClientName: "tim", Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}

	names, err := fetchPolicyGroupNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchPolicyGroupNames: %v", err)
	}
	want := []string{"dev", "prod", "stage"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchPolicyGroupNames = %v, want %v", names, want)
	}
}

func TestPolicyGroupShowCommandEndToEnd(t *testing.T) {
	group := map[string]any{
		"uri": "https://example.test/policy_groups/prod",
		"policies": map[string]any{
			"appserver": map[string]any{"revision_id": "1.0.0"},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policy_groups/prod", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(group)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy-group", "show", "prod", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy-group show: %v", err)
	}

	var got cinc.PolicyGroup
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	rev, ok := got.Policies["appserver"]
	if !ok {
		t.Fatalf("show policies = %v, want an appserver entry", got.Policies)
	}
	if rev.RevisionID != "1.0.0" {
		t.Errorf("appserver revision = %q, want 1.0.0", rev.RevisionID)
	}
}

func TestPolicyGroupDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policy_groups/prod", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "prod"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "https://example.test/policy_groups/prod"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy-group", "delete", "prod", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy-group delete: %v", err)
	}
	if deleted != "prod" {
		t.Errorf("server saw delete of %q, want %q", deleted, "prod")
	}
	if got := buf.String(); got != "Deleted policy group \"prod\"\n" {
		t.Errorf("policy-group delete output = %q", got)
	}
}

func TestPolicyGroupListCommandEndToEnd(t *testing.T) {
	srv := policyGroupServer(t, "prod", "dev", "stage")

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy-group", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy-group list: %v", err)
	}
	if got := buf.String(); got != "dev\nprod\nstage\n" {
		t.Errorf("policy-group list output = %q, want sorted policy-group names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"policy-group", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy-group list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"dev", "prod", "stage"}) {
		t.Errorf("json list output = %v, want [dev prod stage]", names)
	}
}
