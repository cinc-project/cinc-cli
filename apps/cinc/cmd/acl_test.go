package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeACLConfig writes a credentials file pointed at srv for org "acme"
// and returns its path.
func writeACLConfig(t *testing.T, srvURL string) string {
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

// fullACL returns an ACL fixture with one group on read so tests have a
// known starting point.
func fullACL() cinc.ACL {
	mk := func(actors, groups []string) cinc.ACE {
		return cinc.ACE{Actors: actors, Groups: groups}
	}
	return cinc.ACL{
		Create: mk([]string{"pivotal"}, []string{"admins"}),
		Read:   mk([]string{"pivotal"}, []string{"admins", "users"}),
		Update: mk([]string{"pivotal"}, []string{"admins"}),
		Delete: mk([]string{"pivotal"}, []string{"admins"}),
		Grant:  mk([]string{"pivotal"}, []string{"admins"}),
	}
}

// aclServer serves GET and PUT against the _acl subresource rooted at base
// (e.g. "/organizations/acme/nodes/web01" or "/organizations/acme" or
// "/users/alice"). It records each PUT body keyed by permission name.
type aclServer struct {
	srv  *httptest.Server
	acl  cinc.ACL
	puts map[string]cinc.ACE // perm -> ACE last PUT
}

func newACLServer(t *testing.T, base string) *aclServer {
	t.Helper()
	a := &aclServer{acl: fullACL(), puts: map[string]cinc.ACE{}}
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/_acl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method on %s = %q, want GET", r.URL.Path, r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.acl)
	})
	for _, perm := range []string{"create", "read", "update", "delete", "grant"} {
		perm := perm
		mux.HandleFunc(base+"/_acl/"+perm, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method on %s = %q, want PUT", r.URL.Path, r.Method)
			}
			var body map[string]cinc.ACE
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			ace, ok := body[perm]
			if !ok {
				t.Errorf("PUT body for %s missing %q key: %v", r.URL.Path, perm, body)
			}
			a.puts[perm] = ace
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{})
		})
	}
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestNodeACLShowRendersJSON(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, err := runRoot(t, "node", "acl", "show", "web01", "--config", cfg, "--format", "json")
	if err != nil {
		t.Fatalf("node acl show: %v", err)
	}
	var got cinc.ACL
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("acl show output not valid JSON: %v\n%s", err, out)
	}
	if !slicesEqual(got.Read.Groups, []string{"admins", "users"}) {
		t.Errorf("read groups = %v, want [admins users]", got.Read.Groups)
	}
}

func TestNodeACLShowHumanIsReadable(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, err := runRoot(t, "node", "acl", "show", "web01", "--config", cfg)
	if err != nil {
		t.Fatalf("node acl show: %v", err)
	}
	// Human render is pretty JSON (matching the rest of the CLI); the five
	// permission keys must all be present.
	for _, perm := range []string{"create", "read", "update", "delete", "grant"} {
		if !strings.Contains(out, "\""+perm+"\"") {
			t.Errorf("human acl show missing %q permission:\n%s", perm, out)
		}
	}
}

func TestNodeACLGrantReadAddsGroup(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, err := runRoot(t, "node", "acl", "grant", "read", "web01", "--group", "ops", "--config", cfg)
	if err != nil {
		t.Fatalf("node acl grant: %v", err)
	}
	put, ok := a.puts["read"]
	if !ok {
		t.Fatal("server never saw a PUT to the read permission")
	}
	// The merged ACE must keep the existing groups and add the new one.
	if !slicesEqual(put.Groups, []string{"admins", "users", "ops"}) {
		t.Errorf("read groups PUT = %v, want [admins users ops]", put.Groups)
	}
	if !slicesEqual(put.Actors, []string{"pivotal"}) {
		t.Errorf("read actors PUT = %v, want [pivotal] (unchanged)", put.Actors)
	}
	if len(a.puts) != 1 {
		t.Errorf("grant read touched %d permissions, want only read: %v", len(a.puts), a.puts)
	}
	if !strings.Contains(out, "ops") || !strings.Contains(strings.ToLower(out), "grant") {
		t.Errorf("grant output should confirm the change:\n%s", out)
	}
}

func TestNodeACLGrantUserAndClientLandInActors(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	if _, _, err := runRoot(t, "node", "acl", "grant", "update", "web01",
		"--user", "alice", "--client", "worker-01", "--config", cfg); err != nil {
		t.Fatalf("node acl grant: %v", err)
	}
	put := a.puts["update"]
	if !slicesEqual(put.Actors, []string{"pivotal", "alice", "worker-01"}) {
		t.Errorf("update actors PUT = %v, want [pivotal alice worker-01]", put.Actors)
	}
}

func TestNodeACLGrantAllTouchesEveryPermission(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	if _, _, err := runRoot(t, "node", "acl", "grant", "all", "web01",
		"--group", "ops", "--config", cfg); err != nil {
		t.Fatalf("node acl grant all: %v", err)
	}
	for _, perm := range []string{"create", "read", "update", "delete", "grant"} {
		put, ok := a.puts[perm]
		if !ok {
			t.Errorf("grant all did not PUT permission %q", perm)
			continue
		}
		if !slicesContains(put.Groups, "ops") {
			t.Errorf("%s groups = %v, want it to contain ops", perm, put.Groups)
		}
	}
}

func TestNodeACLRevokeRemovesGroup(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	if _, _, err := runRoot(t, "node", "acl", "revoke", "read", "web01",
		"--group", "users", "--config", cfg); err != nil {
		t.Fatalf("node acl revoke: %v", err)
	}
	put, ok := a.puts["read"]
	if !ok {
		t.Fatal("server never saw a PUT to the read permission")
	}
	if !slicesEqual(put.Groups, []string{"admins"}) {
		t.Errorf("read groups after revoke = %v, want [admins]", put.Groups)
	}
}

func TestNodeACLGrantNoChangeIsFriendlyNoOp(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	// admins is already on read, so granting it again should change nothing.
	out, _, err := runRoot(t, "node", "acl", "grant", "read", "web01",
		"--group", "admins", "--config", cfg)
	if err != nil {
		t.Fatalf("node acl grant: %v", err)
	}
	if len(a.puts) != 0 {
		t.Errorf("no-op grant still issued a PUT: %v", a.puts)
	}
	if !strings.Contains(strings.ToLower(out), "no change") {
		t.Errorf("no-op grant should print a friendly message:\n%s", out)
	}
}

func TestACLGrantRequiresAMemberFlag(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	_, errOut, err := runRoot(t, "node", "acl", "grant", "read", "web01", "--config", cfg)
	if err == nil {
		t.Fatal("expected an error when no member flag is given")
	}
	if !strings.Contains(strings.ToLower(err.Error()+errOut), "user") {
		t.Errorf("missing-member error should mention the member flags: %v / %s", err, errOut)
	}
}

func TestACLGrantRejectsUnknownPermission(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	_, _, err := runRoot(t, "node", "acl", "grant", "frobnicate", "web01",
		"--group", "ops", "--config", cfg)
	if err == nil {
		t.Fatal("expected an error for an unknown permission")
	}
}

func TestRoleACLRoutesToRolesPath(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/roles/web")
	cfg := writeACLConfig(t, a.srv.URL)

	if _, _, err := runRoot(t, "role", "acl", "show", "web", "--config", cfg, "--format", "json"); err != nil {
		t.Fatalf("role acl show should route to the roles path: %v", err)
	}
}

func TestOrgACLRoutesToOrgEndpoint(t *testing.T) {
	// The org's own ACL lives at /organizations/acme/_acl with no
	// object-type segment, and takes no name argument.
	a := newACLServer(t, "/organizations/acme")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, err := runRoot(t, "org", "acl", "show", "--config", cfg, "--format", "json")
	if err != nil {
		t.Fatalf("org acl show: %v", err)
	}
	var got cinc.ACL
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("org acl show not valid JSON: %v\n%s", err, out)
	}

	if _, _, err := runRoot(t, "org", "acl", "grant", "read", "--group", "ops", "--config", cfg); err != nil {
		t.Fatalf("org acl grant: %v", err)
	}
	if !slicesContains(a.puts["read"].Groups, "ops") {
		t.Errorf("org acl grant read groups = %v, want it to contain ops", a.puts["read"].Groups)
	}
}

func TestUserACLRoutesToUsersEndpoint(t *testing.T) {
	// User ACLs are global: /users/alice/_acl, not org-scoped.
	a := newACLServer(t, "/users/alice")
	cfg := writeACLConfig(t, a.srv.URL)

	if _, _, err := runRoot(t, "user", "acl", "show", "alice", "--config", cfg, "--format", "json"); err != nil {
		t.Fatalf("user acl show: %v", err)
	}
	if _, _, err := runRoot(t, "user", "acl", "grant", "grant", "alice", "--user", "bob", "--config", cfg); err != nil {
		t.Fatalf("user acl grant: %v", err)
	}
	if !slicesContains(a.puts["grant"].Actors, "bob") {
		t.Errorf("user acl grant actors = %v, want it to contain bob", a.puts["grant"].Actors)
	}
}

// slicesEqual and slicesContains are tiny local helpers so the test file
// doesn't depend on generic-slices import quirks across Go versions.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slicesContains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
