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
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeOrgConfig writes a credentials file pointed at srv for org "acme"
// and returns its path. Org-root verbs ignore the org segment; the
// member/invite verbs derive "acme" from it.
func writeOrgConfig(t *testing.T, srvURL string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "pivotal"
client_key      = %q
`, srvURL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// orgIndexServer serves the org name->URL index at the server root.
func orgIndexServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/organizations/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchOrgNamesReturnsSortedNames(t *testing.T) {
	srv := orgIndexServer(t, "zeta", "acme", "mondoo")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: srv.URL, Org: "acme", ClientName: "pivotal", Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}

	names, err := fetchOrgNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchOrgNames: %v", err)
	}
	want := []string{"acme", "mondoo", "zeta"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchOrgNames = %v, want %v", names, want)
	}
}

func TestOrgListCommandEndToEnd(t *testing.T) {
	srv := orgIndexServer(t, "zeta", "acme", "mondoo")
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "list", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org list: %v", err)
	}
	if got := buf.String(); got != "acme\nmondoo\nzeta\n" {
		t.Errorf("org list output = %q, want sorted org names", got)
	}

	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"acme", "mondoo", "zeta"}) {
		t.Errorf("json list output = %v, want [acme mondoo zeta]", names)
	}
}

func TestOrgShowCommandEndToEnd(t *testing.T) {
	org := cinc.Org{Name: "acme", FullName: "Acme Corporation", GUID: "abc123"}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(org)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "show", "acme", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org show: %v", err)
	}
	var got cinc.Org
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != "acme" || got.FullName != "Acme Corporation" {
		t.Errorf("show returned %+v, want name=acme full_name=Acme Corporation", got)
	}
}

func TestOrgShowNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/ghost", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"not found"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"org", "show", "ghost", "--config", cfgPath})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for a 404 org show, got nil")
	}
}

func TestOrgCreateCommandStreamsValidatorKey(t *testing.T) {
	var posted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uri":         "https://example.test/organizations/acme",
			"clientname":  "acme-validator",
			"private_key": "-----BEGIN RSA PRIVATE KEY-----\nFAKE\n-----END RSA PRIVATE KEY-----\n",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"org", "create", "acme", "Acme Corporation", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org create: %v", err)
	}
	if posted["name"] != "acme" || posted["full_name"] != "Acme Corporation" {
		t.Errorf("create body = %v, want name=acme full_name=Acme Corporation", posted)
	}
	if !strings.Contains(out.String(), "BEGIN RSA PRIVATE KEY") {
		t.Errorf("org create stdout missing validator key:\n%s", out.String())
	}
	// The "save this now" heads-up goes to stderr so the key on stdout stays pipeable.
	if !strings.Contains(strings.ToLower(errBuf.String()), "save this") {
		t.Errorf("org create stderr missing save-the-key warning:\n%s", errBuf.String())
	}
}

func TestOrgCreateCommandWritesKeyFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"clientname":  "acme-validator",
			"private_key": "VALIDATOR-KEY-PEM\n",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)
	keyPath := filepath.Join(t.TempDir(), "acme-validator.pem")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "create", "acme", "Acme Corporation", "--filename", keyPath, "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org create: %v", err)
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("validator key file not written: %v", err)
	}
	if string(data) != "VALIDATOR-KEY-PEM\n" {
		t.Errorf("key file = %q", string(data))
	}
	if got := buf.String(); !strings.Contains(got, "acme") || !strings.Contains(got, keyPath) {
		t.Errorf("org create output = %q, want it to name the org and key path", got)
	}
}

func TestOrgCreateConflict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"already exists"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"org", "create", "acme", "Acme Corporation", "--config", cfgPath})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for a 409 org create, got nil")
	}
}

func TestOrgDeleteCommandEndToEnd(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "acme"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "delete", "acme", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org delete: %v", err)
	}
	if !deleted {
		t.Error("server never saw the delete")
	}
	if got := buf.String(); got != "Deleted organization \"acme\"\n" {
		t.Errorf("org delete output = %q", got)
	}
}

// orgItemServer serves GET of current and records the PUT body at the
// org-root /organizations/<name> path.
func orgItemServer(t *testing.T, name string, current cinc.Org, gotPut *cinc.Org) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(gotPut); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(gotPut)
		default:
			t.Errorf("unexpected method %q on %s", r.Method, r.URL.Path)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withStubOrgEditor(t *testing.T, stub func(*cinc.Org) (*cinc.Org, error)) {
	t.Helper()
	orig := editOrg
	editOrg = stub
	t.Cleanup(func() { editOrg = orig })
}

func TestOrgEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.Org
	current := cinc.Org{Name: "acme", FullName: "Acme Corporation", GUID: "abc123"}
	srv := orgItemServer(t, "acme", current, &gotPut)

	withStubOrgEditor(t, func(in *cinc.Org) (*cinc.Org, error) {
		out := *in
		out.FullName = "Acme Industries"
		return &out, nil
	})
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "edit", "acme", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org edit: %v", err)
	}
	if gotPut.Name != "acme" || gotPut.FullName != "Acme Industries" {
		t.Errorf("PUT body = %+v, want name=acme full_name=Acme Industries", gotPut)
	}
	if got := buf.String(); got != "Updated organization \"acme\"\n" {
		t.Errorf("org edit output = %q", got)
	}
}

func TestOrgEditCommandUnchangedIsNoOp(t *testing.T) {
	var gotPut cinc.Org
	current := cinc.Org{Name: "acme", FullName: "Acme Corporation"}
	srv := orgItemServer(t, "acme", current, &gotPut)

	withStubOrgEditor(t, func(in *cinc.Org) (*cinc.Org, error) {
		out := *in // unchanged
		return &out, nil
	})
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "edit", "acme", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org edit: %v", err)
	}
	if gotPut.Name != "" {
		t.Errorf("server saw a PUT despite no change: %+v", gotPut)
	}
	if got := buf.String(); got != "Organization \"acme\" unchanged\n" {
		t.Errorf("org edit output = %q", got)
	}
}

func TestOrgEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.Org
	current := cinc.Org{Name: "acme", FullName: "Acme Corporation"}
	srv := orgItemServer(t, "acme", current, &gotPut)

	withStubOrgEditor(t, func(*cinc.Org) (*cinc.Org, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "org.json")
	body, _ := json.Marshal(cinc.Org{Name: "ignored", FullName: "From File"})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"org", "edit", "acme", "--file", filePath, "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org edit --file: %v", err)
	}
	if gotPut.Name != "acme" || gotPut.FullName != "From File" {
		t.Errorf("PUT body = %+v, want name=acme full_name=From File", gotPut)
	}
}

func TestOrgMemberListCommandEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"user": map[string]string{"username": "ben"}},
			{"user": map[string]string{"username": "anna"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "member", "list", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org member list: %v", err)
	}
	if got := buf.String(); got != "anna\nben\n" {
		t.Errorf("org member list output = %q, want sorted member names", got)
	}
}

func TestOrgMemberAddCommandEndToEnd(t *testing.T) {
	var posted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "member", "add", "alice", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org member add: %v", err)
	}
	if posted["username"] != "alice" {
		t.Errorf("add body = %v, want username=alice", posted)
	}
	if got := buf.String(); got != "Added \"alice\" to organization \"acme\"\n" {
		t.Errorf("org member add output = %q", got)
	}
}

func TestOrgMemberRemoveCommandEndToEnd(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/users/alice", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "alice"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "member", "remove", "alice", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org member remove: %v", err)
	}
	if !deleted {
		t.Error("server never saw the member delete")
	}
	if got := buf.String(); got != "Removed \"alice\" from organization \"acme\"\n" {
		t.Errorf("org member remove output = %q", got)
	}
}

func TestOrgInviteListCommandEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/association_requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "acme-carol", "username": "carol"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "invite", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org invite list: %v", err)
	}
	var got []cinc.Invitation
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invite list output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].ID != "acme-carol" || got[0].Username != "carol" {
		t.Errorf("invite list = %+v, want one invite for carol", got)
	}
}

func TestOrgInviteCreateCommandEndToEnd(t *testing.T) {
	var posted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/association_requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"uri": "https://example.test/organizations/acme/association_requests/acme-carol"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "invite", "create", "carol", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org invite create: %v", err)
	}
	if posted["user"] != "carol" {
		t.Errorf("invite body = %v, want user=carol", posted)
	}
	if got := buf.String(); got != "Invited \"carol\" to organization \"acme\"\n" {
		t.Errorf("org invite create output = %q", got)
	}
}

func TestOrgInviteRescindCommandEndToEnd(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/association_requests/acme-carol", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeOrgConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"org", "invite", "rescind", "acme-carol", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc org invite rescind: %v", err)
	}
	if !deleted {
		t.Error("server never saw the invite delete")
	}
	if got := buf.String(); got != "Rescinded invitation \"acme-carol\"\n" {
		t.Errorf("org invite rescind output = %q", got)
	}
}
