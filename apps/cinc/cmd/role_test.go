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

// roleServer starts an httptest server that serves a role index for org
// "acme" and returns the server.
func roleServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/roles/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchRoleNamesReturnsSortedNames(t *testing.T) {
	srv := roleServer(t, "web", "base", "db")

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

	names, err := fetchRoleNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchRoleNames: %v", err)
	}
	want := []string{"base", "db", "web"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchRoleNames = %v, want %v", names, want)
	}
}

func TestRoleDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles/web", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "web"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "web"})
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
	root.SetArgs([]string{"role", "delete", "web", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role delete: %v", err)
	}
	if deleted != "web" {
		t.Errorf("server saw delete of %q, want %q", deleted, "web")
	}
	if got := buf.String(); got != "Deleted role \"web\"\n" {
		t.Errorf("role delete output = %q", got)
	}
}

func TestRoleShowCommandEndToEnd(t *testing.T) {
	role := cinc.Role{
		Name:        "web",
		Description: "Web tier",
		RunList:     []string{"recipe[apache]", "recipe[base]"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles/web", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(role)
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
	root.SetArgs([]string{"role", "show", "web", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role show: %v", err)
	}

	var got cinc.Role
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != "web" || got.Description != "Web tier" {
		t.Errorf("show returned %+v, want name=web description=Web tier", got)
	}
	if !slices.Equal(got.RunList, []string{"recipe[apache]", "recipe[base]"}) {
		t.Errorf("show run_list = %v", got.RunList)
	}
}

// roleItemServer serves GET of current at /roles/<name> and records the
// PUT body into *gotPut.
func roleItemServer(t *testing.T, name string, current cinc.Role, gotPut *cinc.Role) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles/"+name, func(w http.ResponseWriter, r *http.Request) {
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

func withStubRoleEditor(t *testing.T, stub func(*cinc.Role) (*cinc.Role, error)) {
	t.Helper()
	orig := editRole
	editRole = stub
	t.Cleanup(func() { editRole = orig })
}

func TestRoleCreateCommandEndToEnd(t *testing.T) {
	var created cinc.Role
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
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
	root.SetArgs([]string{"role", "create", "web", "--description", "Web tier", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role create: %v", err)
	}
	if created.Name != "web" || created.Description != "Web tier" {
		t.Errorf("server received %+v, want name=web description=Web tier", created)
	}
	if created.RunList == nil {
		t.Errorf("run_list should serialize as [] not null")
	}
	if got := buf.String(); got != "Created role \"web\"\n" {
		t.Errorf("role create output = %q", got)
	}
}

func TestRoleCreateCommandReadsFromFile(t *testing.T) {
	var created cinc.Role
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/roles", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
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

	filePath := filepath.Join(t.TempDir(), "role.json")
	body, _ := json.Marshal(cinc.Role{Name: "ignored-in-file", Description: "From file", RunList: []string{"recipe[base]"}})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"role", "create", "web", "--file", filePath, "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role create --file: %v", err)
	}
	if created.Name != "web" {
		t.Errorf("create body name = %q, want web (path arg must win over file)", created.Name)
	}
	if !slices.Equal(created.RunList, []string{"recipe[base]"}) {
		t.Errorf("create body run_list = %v, want from file", created.RunList)
	}
}

func TestRoleEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.Role
	current := cinc.Role{Name: "web", RunList: []string{"recipe[base]"}}
	srv := roleItemServer(t, "web", current, &gotPut)

	withStubRoleEditor(t, func(in *cinc.Role) (*cinc.Role, error) {
		out := *in
		out.RunList = append(slices.Clone(in.RunList), "recipe[apache]")
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
	root.SetArgs([]string{"role", "edit", "web", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role edit: %v", err)
	}
	if gotPut.Name != "web" || !slices.Equal(gotPut.RunList, []string{"recipe[base]", "recipe[apache]"}) {
		t.Errorf("PUT body = %+v, want name=web with appended recipe", gotPut)
	}
	if got := buf.String(); got != "Updated role \"web\"\n" {
		t.Errorf("role edit output = %q", got)
	}
}

func TestRoleEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.Role
	current := cinc.Role{Name: "web", RunList: []string{"recipe[base]"}}
	srv := roleItemServer(t, "web", current, &gotPut)

	withStubRoleEditor(t, func(*cinc.Role) (*cinc.Role, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "role.json")
	body, _ := json.Marshal(cinc.Role{Name: "ignored", RunList: []string{"recipe[x]"}})
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
	root.SetArgs([]string{"role", "edit", "web", "--file", filePath, "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role edit --file: %v", err)
	}
	if gotPut.Name != "web" || !slices.Equal(gotPut.RunList, []string{"recipe[x]"}) {
		t.Errorf("PUT body = %+v, want name=web run_list=[recipe[x]]", gotPut)
	}
}

func TestRoleListCommandEndToEnd(t *testing.T) {
	srv := roleServer(t, "web", "base", "db")

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
	root.SetArgs([]string{"role", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role list: %v", err)
	}
	if got := buf.String(); got != "base\ndb\nweb\n" {
		t.Errorf("role list output = %q, want sorted role names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"role", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc role list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"base", "db", "web"}) {
		t.Errorf("json list output = %v, want [base db web]", names)
	}
}
