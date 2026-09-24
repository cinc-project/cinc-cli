package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// newFailingACLServer serves the ACL rooted at base and answers the GET or
// the PUTs (per failOn, "GET" or "PUT") with status and a Chef error body.
func newFailingACLServer(t *testing.T, base, failOn string, status int, message string) *httptest.Server {
	t.Helper()
	fail := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{message}})
	}
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/_acl", func(w http.ResponseWriter, _ *http.Request) {
		if failOn == http.MethodGet {
			fail(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fullACL())
	})
	mux.HandleFunc(base+"/_acl/", func(w http.ResponseWriter, _ *http.Request) {
		if failOn == http.MethodPut {
			fail(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// wantNoRawRequest fails when err reads as a raw request line rather than a
// sentence about the object.
func wantNoRawRequest(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "/organizations/") || strings.Contains(err.Error(), "/_acl") {
		t.Errorf("error should not be a raw request line: %v", err)
	}
}

func TestACLShowForbiddenSaysWhatIsMissing(t *testing.T) {
	srv := newFailingACLServer(t, "/organizations/acme/nodes/web01", http.MethodGet, http.StatusForbidden, "missing grant permission")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "node", "acl", "show", "web01", "--config", cfg)
	if err == nil {
		t.Fatal("expected a permission error")
	}
	if !strings.Contains(err.Error(), `grant permission on node "web01"`) {
		t.Errorf("error should name the missing permission and the object: %v", err)
	}
	wantNoRawRequest(t, err)
	if !errors.Is(err, cinc.ErrForbidden) {
		t.Errorf("error should still unwrap to cinc.ErrForbidden: %v", err)
	}
}

func TestACLGrantForbiddenOnWriteSaysWhatIsMissing(t *testing.T) {
	srv := newFailingACLServer(t, "/organizations/acme/roles/web", http.MethodPut, http.StatusForbidden, "missing grant permission")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "role", "acl", "grant", "read", "web", "--group", "ops", "--config", cfg)
	if err == nil {
		t.Fatal("expected a permission error")
	}
	if !strings.Contains(err.Error(), `grant permission on role "web"`) {
		t.Errorf("error should name the missing permission and the object: %v", err)
	}
	wantNoRawRequest(t, err)
}

func TestOrgACLForbiddenNamesTheOrganization(t *testing.T) {
	srv := newFailingACLServer(t, "/organizations/acme/organizations", http.MethodGet, http.StatusForbidden, "missing grant permission")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "org", "acl", "show", "--config", cfg)
	if err == nil {
		t.Fatal("expected a permission error")
	}
	if !strings.Contains(err.Error(), `grant permission on organization "acme"`) {
		t.Errorf("error should name the organization: %v", err)
	}
}

func TestACLShowMissingObjectIsFriendly(t *testing.T) {
	srv := newFailingACLServer(t, "/organizations/acme/nodes/web01", http.MethodGet, http.StatusNotFound, "Cannot find node web01")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "node", "acl", "show", "web01", "--config", cfg)
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), `couldn't find node "web01"`) {
		t.Errorf("error should say the node was not found: %v", err)
	}
	wantNoRawRequest(t, err)
	if !errors.Is(err, cinc.ErrNotFound) {
		t.Errorf("error should still unwrap to cinc.ErrNotFound: %v", err)
	}
}

// erchef refuses an ACE naming an actor or group that does not exist with a
// 400; the CLI passes on what the server said and what to check.
func TestACLGrantUnknownMemberSaysWhatToCheck(t *testing.T) {
	srv := newFailingACLServer(t, "/organizations/acme/nodes/web01", http.MethodPut, http.StatusBadRequest, "Invalid/missing actors: ghost")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "node", "acl", "grant", "read", "web01", "--client", "ghost", "--config", cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{`couldn't grant read on node "web01" to ghost`, "Invalid/missing actors: ghost", "exists"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
	wantNoRawRequest(t, err)
}

func TestACLNoChangeMessages(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, err := runRoot(t, "node", "acl", "grant", "read", "web01", "--group", "admins", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "No change: admins already has read on node \"web01\".\n"; out != want {
		t.Errorf("no-op grant = %q, want %q", out, want)
	}

	out, _, err = runRoot(t, "node", "acl", "revoke", "update", "web01", "--group", "users", "--client", "ghost", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "No change: ghost, users don't have update on node \"web01\".\n"; out != want {
		t.Errorf("no-op revoke = %q, want %q", out, want)
	}
	if len(a.puts) != 0 {
		t.Errorf("a no-op issued PUTs: %v", a.puts)
	}
}

func TestACLMessagesHaveNoEmDash(t *testing.T) {
	a := newACLServer(t, "/organizations/acme/nodes/web01")
	cfg := writeACLConfig(t, a.srv.URL)

	out, _, _ := runRoot(t, "node", "acl", "grant", "read", "web01", "--group", "admins", "--config", cfg)
	_, _, err := runRoot(t, "node", "acl", "grant", "read", "web01", "--config", cfg)
	if err == nil {
		t.Fatal("expected a missing-member error")
	}
	for _, s := range []string{out, err.Error()} {
		if strings.ContainsRune(s, '—') {
			t.Errorf("message contains an em dash: %q", s)
		}
	}
}

// newACLServerFailingPerm serves the ACL rooted at base, accepting every
// permission PUT except failPerm, which it answers with status and message.
func newACLServerFailingPerm(t *testing.T, base, failPerm string, status int, message string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/_acl", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fullACL())
	})
	mux.HandleFunc(base+"/_acl/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/"+failPerm) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{message}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Each permission is its own PUT, so a failure part way leaves the earlier
// ones changed. The error has to say which, or the user can't tell what
// state the ACL is in.
func TestACLGrantAllPartialFailureSaysWhatChanged(t *testing.T) {
	srv := newACLServerFailingPerm(t, "/organizations/acme/nodes/web01", "update", http.StatusBadRequest, "Invalid/missing actors: ghost")
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "node", "acl", "grant", "all", "web01", "--group", "ops", "--config", cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{`couldn't grant update on node "web01" to ops`, "create, read", "Invalid/missing actors: ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
	wantNoRawRequest(t, err)
	if !errors.Is(err, cinc.ErrBadRequest) {
		t.Errorf("error should still unwrap to cinc.ErrBadRequest: %v", err)
	}
}

// erchef refuses to take the admins group off grant unless the caller is
// the superuser, with a 403 that has nothing to do with the caller's own
// grant permission. Telling them they lack grant would send them chasing
// the wrong problem.
func TestACLRevokeAdminsFromGrantSurfacesServerMessage(t *testing.T) {
	const msg = "Admin group cannot be removed from the Grant ACE"
	srv := newACLServerFailingPerm(t, "/organizations/acme/nodes/web01", "grant", http.StatusForbidden, msg)
	cfg := writeACLConfig(t, srv.URL)

	_, _, err := runRoot(t, "node", "acl", "revoke", "grant", "web01", "--group", "admins", "--config", cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error should pass on the server's message: %v", err)
	}
	if strings.Contains(err.Error(), "you don't have grant permission") {
		t.Errorf("error blames the caller's grant permission: %v", err)
	}
	wantNoRawRequest(t, err)
	if !errors.Is(err, cinc.ErrForbidden) {
		t.Errorf("error should still unwrap to cinc.ErrForbidden: %v", err)
	}
}
