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

// environmentServer starts an httptest server that serves an environment
// index for org "acme" and returns the server.
func environmentServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/environments/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchEnvironmentNamesReturnsSortedNames(t *testing.T) {
	srv := environmentServer(t, "prod", "_default", "staging")

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

	names, err := fetchEnvironmentNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchEnvironmentNames: %v", err)
	}
	want := []string{"_default", "prod", "staging"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchEnvironmentNames = %v, want %v", names, want)
	}
}

func TestEnvironmentDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments/staging", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "staging"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "staging"})
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
	root.SetArgs([]string{"environment", "delete", "staging", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment delete: %v", err)
	}
	if deleted != "staging" {
		t.Errorf("server saw delete of %q, want %q", deleted, "staging")
	}
	if got := buf.String(); got != "Deleted environment \"staging\"\n" {
		t.Errorf("environment delete output = %q", got)
	}
}

func TestEnvironmentCreateCommandEndToEnd(t *testing.T) {
	var (
		method   string
		path     string
		received cinc.Environment
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "https://example.test/environments/staging"})
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
	root.SetArgs([]string{"environment", "create", "staging", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment create: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if path != "/organizations/acme/environments" {
		t.Errorf("path = %q, want /organizations/acme/environments", path)
	}
	if received.Name != "staging" {
		t.Errorf("posted environment = %+v, want name=staging", received)
	}
	if received.Description != "" {
		t.Errorf("description = %q, want empty when --description not supplied", received.Description)
	}
	if got := buf.String(); got != "Created environment \"staging\"\n" {
		t.Errorf("environment create output = %q", got)
	}
}

func TestEnvironmentCreateCommandSendsDescription(t *testing.T) {
	var received cinc.Environment
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"uri":"https://example.test/environments/prod"}`))
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
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"environment", "create", "prod",
		"--description", "Production environment",
		"--config", cfgPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment create: %v", err)
	}
	if received.Description != "Production environment" {
		t.Errorf("description = %q, want %q", received.Description, "Production environment")
	}
}

func TestEnvironmentCreateCommandReadsFile(t *testing.T) {
	var received cinc.Environment
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"uri":"https://example.test/environments/staging"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	envFile := filepath.Join(dir, "staging.json")
	body := `{
  "name": "ignored-by-cli",
  "description": "from file",
  "cookbook_versions": { "apache2": "~> 1.2.0" }
}`
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"environment", "create", "staging",
		"--file", envFile,
		"--config", cfgPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment create --file: %v", err)
	}
	if received.Name != "staging" {
		t.Errorf("name = %q, want positional arg to override the file's name", received.Name)
	}
	if received.Description != "from file" {
		t.Errorf("description = %q, want value from the file", received.Description)
	}
	if v := received.CookbookVersions["apache2"]; v != "~> 1.2.0" {
		t.Errorf("cookbook_versions[apache2] = %q, want from file", v)
	}
}

func TestEnvironmentShowCommandEndToEnd(t *testing.T) {
	want := cinc.Environment{
		Name:             "prod",
		Description:      "Production",
		CookbookVersions: map[string]string{"apache2": "= 1.2.0"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments/prod", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
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
	root.SetArgs([]string{"environment", "show", "prod", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment show: %v", err)
	}

	var got cinc.Environment
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != "prod" || got.Description != "Production" {
		t.Errorf("show returned %+v, want name=prod description=Production", got)
	}
	if got.CookbookVersions["apache2"] != "= 1.2.0" {
		t.Errorf("show cookbook_versions[apache2] = %q", got.CookbookVersions["apache2"])
	}
}

// environmentItemServer serves GET of current at /environments/<name> and
// records the PUT body into *gotPut.
func environmentItemServer(t *testing.T, name string, current cinc.Environment, gotPut *cinc.Environment) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/environments/"+name, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(gotPut); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gotPut)
		default:
			t.Errorf("unexpected method %q on %s", r.Method, r.URL.Path)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withStubEnvironmentEditor(t *testing.T, stub func(*cinc.Environment) (*cinc.Environment, error)) {
	t.Helper()
	orig := editEnvironment
	editEnvironment = stub
	t.Cleanup(func() { editEnvironment = orig })
}

func TestEnvironmentEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.Environment
	current := cinc.Environment{Name: "staging", Description: "old"}
	srv := environmentItemServer(t, "staging", current, &gotPut)

	withStubEnvironmentEditor(t, func(in *cinc.Environment) (*cinc.Environment, error) {
		out := *in
		out.Description = "new"
		return &out, nil
	})

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
	root.SetArgs([]string{"environment", "edit", "staging", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment edit: %v", err)
	}
	if gotPut.Name != "staging" || gotPut.Description != "new" {
		t.Errorf("PUT body = %+v, want name=staging description=new", gotPut)
	}
	if got := buf.String(); got != "Updated environment \"staging\"\n" {
		t.Errorf("environment edit output = %q", got)
	}
}

func TestEnvironmentEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.Environment
	current := cinc.Environment{Name: "staging", Description: "old"}
	srv := environmentItemServer(t, "staging", current, &gotPut)

	withStubEnvironmentEditor(t, func(*cinc.Environment) (*cinc.Environment, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "env.json")
	body, _ := json.Marshal(cinc.Environment{Name: "ignored", Description: "from file"})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

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
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"environment", "edit", "staging", "--file", filePath, "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment edit --file: %v", err)
	}
	if gotPut.Name != "staging" || gotPut.Description != "from file" {
		t.Errorf("PUT body = %+v, want name=staging description=from file", gotPut)
	}
}

func TestEnvironmentListCommandEndToEnd(t *testing.T) {
	srv := environmentServer(t, "prod", "_default", "staging")

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
	root.SetArgs([]string{"environment", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment list: %v", err)
	}
	if got := buf.String(); got != "_default\nprod\nstaging\n" {
		t.Errorf("environment list output = %q, want sorted environment names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"environment", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc environment list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"_default", "prod", "staging"}) {
		t.Errorf("json list output = %v, want [_default prod staging]", names)
	}
}
