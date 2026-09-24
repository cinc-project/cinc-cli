package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// groupMemberConfig writes a credentials file pointed at srv for org "acme".
func groupMemberConfig(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// runGroupMember runs `cinc group member <args>` against cfg and returns
// stdout and the error.
func runGroupMember(t *testing.T, cfg string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append(append([]string{"group", "member"}, args...), "--config", cfg))
	err := root.Execute()
	return out.String(), err
}

// An unknown --type is a flag error, raised before any request: against a
// group that does not exist it must not turn into a not-found.
func TestGroupMemberRejectsBadTypeBeforeAnyRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	cfg := groupMemberConfig(t, srv)

	for _, verb := range []string{"add", "remove"} {
		_, err := runGroupMember(t, cfg, verb, "ghost", "alice", "--type", "robot")
		if err == nil {
			t.Fatalf("group member %s --type robot succeeded", verb)
		}
		if !strings.Contains(err.Error(), `"robot"`) || !strings.Contains(err.Error(), "user, client, or group") {
			t.Errorf("group member %s --type robot error = %v, want it to name the valid types", verb, err)
		}
	}
}

func TestGroupMemberAddExistingMemberIsNoOp(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice"}, &gotUsers)
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "add", "admins", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if gotUsers != nil {
		t.Errorf("a no-op add still PUT the group: users %v", gotUsers)
	}
	if want := "No change: alice is already in group \"admins\".\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestGroupMemberRemoveNonMemberIsNoOp(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice"}, &gotUsers)
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "remove", "admins", "carol", "dave")
	if err != nil {
		t.Fatal(err)
	}
	if gotUsers != nil {
		t.Errorf("a no-op remove still PUT the group: users %v", gotUsers)
	}
	if want := "No change: carol, dave aren't in group \"admins\".\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// A partly redundant change applies the rest and says only what changed,
// noting what was already so.
func TestGroupMemberAddReportsOnlyNewMembers(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice"}, &gotUsers)
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "add", "admins", "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotUsers, ",") != "alice,bob" {
		t.Errorf("PUT users = %v, want [alice bob]", gotUsers)
	}
	if want := "Added bob to group \"admins\" (alice was already in it)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestGroupMemberRemoveReportsOnlyRemovedMembers(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice", "bob"}, &gotUsers)
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "remove", "admins", "bob", "carol")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotUsers, ",") != "alice" {
		t.Errorf("PUT users = %v, want [alice]", gotUsers)
	}
	if want := "Removed bob from group \"admins\" (carol wasn't in it)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestGroupMemberAddReportsDroppedMembers adds a real user and one the
// server has never heard of. erchef accepts the group PUT but silently
// drops the unknown name, so the command checks what the group holds
// afterwards: it reports only the member that was added, and fails naming
// the one that wasn't.
func TestGroupMemberAddReportsDroppedMembers(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServerKnowing(t, "admins", []string{"alice"}, &gotUsers, []string{"alice", "bob"})
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "add", "admins", "bob", "ghost")
	if err == nil {
		t.Fatalf("adding an unknown user succeeded: %q", out)
	}
	if !strings.Contains(err.Error(), "ghost") || strings.Contains(err.Error(), "bob") {
		t.Errorf("error = %v, want it to name ghost and only ghost", err)
	}
	if want := "Added bob to group \"admins\"\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
