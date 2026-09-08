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

// policyServer starts an httptest server that serves a policy index
// for org "acme" and returns the server. Each policy entry carries a
// URI and one dummy revision, matching the Chef API shape.
func policyServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	type entry struct {
		URI       string                     `json:"uri"`
		Revisions map[string]json.RawMessage `json:"revisions"`
	}
	index := make(map[string]entry, len(names))
	for _, n := range names {
		index[n] = entry{
			URI:       "https://example.test/policies/" + n,
			Revisions: map[string]json.RawMessage{"deadbeef": json.RawMessage(`{}`)},
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policies", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchPolicyNamesReturnsSortedNames(t *testing.T) {
	srv := policyServer(t, "web", "base", "db")

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

	names, err := fetchPolicyNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchPolicyNames: %v", err)
	}
	want := []string{"base", "db", "web"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchPolicyNames = %v, want %v", names, want)
	}
}

func TestPolicyShowCommandEndToEnd(t *testing.T) {
	// GET /policies/NAME returns the revisions map keyed by revision ID.
	revisions := map[string]any{
		"revisions": map[string]any{
			"1.0.0": map[string]any{"name": "appserver", "revision_id": "1.0.0", "run_list": []string{"recipe[appserver]"}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policies/appserver", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(revisions)
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
	root.SetArgs([]string{"policy", "show", "appserver", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy show: %v", err)
	}

	var got cinc.PolicyRevisions
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if _, ok := got.Revisions["1.0.0"]; !ok {
		t.Errorf("show revisions = %v, want a 1.0.0 entry", got.Revisions)
	}
}

func TestPolicyDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policies/appserver", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "appserver"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "appserver"})
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
	root.SetArgs([]string{"policy", "delete", "appserver", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy delete: %v", err)
	}
	if deleted != "appserver" {
		t.Errorf("server saw delete of %q, want %q", deleted, "appserver")
	}
	if got := buf.String(); got != "Deleted policy \"appserver\"\n" {
		t.Errorf("policy delete output = %q", got)
	}
}

func TestPolicyListCommandEndToEnd(t *testing.T) {
	srv := policyServer(t, "web", "base", "db")

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
	root.SetArgs([]string{"policy", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy list: %v", err)
	}
	if got := buf.String(); got != "base\ndb\nweb\n" {
		t.Errorf("policy list output = %q, want sorted policy names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"base", "db", "web"}) {
		t.Errorf("json list output = %v, want [base db web]", names)
	}
}
