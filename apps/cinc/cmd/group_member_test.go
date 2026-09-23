package cmd

import (
	"bytes"
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

// erchefLikeGroupServer serves one group the way erchef does: a PUT
// answers 200 and echoes the body, but members whose names are in unknown
// are dropped, so the next GET does not list them.
func erchefLikeGroupServer(t *testing.T, name string, clients []string, unknown ...string) *httptest.Server {
	t.Helper()
	current := cinc.Group{Name: name, GroupName: name, Clients: clients}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			var body struct {
				Actors struct {
					Clients []string `json:"clients"`
				} `json:"actors"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			current.Clients = slices.DeleteFunc(slices.Clone(body.Actors.Clients),
				func(n string) bool { return slices.Contains(unknown, n) })
			_ = json.NewEncoder(w).Encode(body)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// erchef drops a member it cannot resolve without an error, so the CLI
// checks the group afterwards and names what didn't land.
func TestGroupMemberAddUnknownNameFails(t *testing.T) {
	srv := erchefLikeGroupServer(t, "devs", nil, "ghost")
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "add", "devs", "worker-01", "ghost", "--type", "client")
	if err == nil {
		t.Fatalf("adding an unknown client succeeded: %q", out)
	}
	if want := `we couldn't add ghost to group "devs": there's no client named ghost in this org`; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if want := "Added worker-01 to group \"devs\"\n"; out != want {
		t.Errorf("output = %q, want %q (the member that did land)", out, want)
	}
}

func TestGroupMemberAddOnlyUnknownNamesSaysNothingWasAdded(t *testing.T) {
	srv := erchefLikeGroupServer(t, "devs", nil, "ghost", "phantom")
	cfg := groupMemberConfig(t, srv)

	out, err := runGroupMember(t, cfg, "add", "devs", "ghost", "phantom", "--type", "client")
	if err == nil {
		t.Fatal("adding unknown clients succeeded")
	}
	if want := `we couldn't add ghost, phantom to group "devs": there are no clients named ghost, phantom in this org`; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if out != "" {
		t.Errorf("output = %q, want nothing, since nothing was added", out)
	}
}
