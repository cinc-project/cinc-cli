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

// groupServer starts an httptest server that serves a group index for
// org "acme" and returns the server.
func groupServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/groups/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchGroupNamesReturnsSortedNames(t *testing.T) {
	srv := groupServer(t, "users", "admins", "clients")

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

	names, err := fetchGroupNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchGroupNames: %v", err)
	}
	want := []string{"admins", "clients", "users"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchGroupNames = %v, want %v", names, want)
	}
}

func TestGroupListCommandEndToEnd(t *testing.T) {
	srv := groupServer(t, "users", "admins", "clients")

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
	root.SetArgs([]string{"group", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group list: %v", err)
	}
	if got := buf.String(); got != "admins\nclients\nusers\n" {
		t.Errorf("group list output = %q, want sorted group names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"group", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"admins", "clients", "users"}) {
		t.Errorf("json list output = %v, want [admins clients users]", names)
	}
}

func TestGroupCreateCommandEndToEnd(t *testing.T) {
	var posted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "team"})
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
	root.SetArgs([]string{"group", "create", "team", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group create: %v", err)
	}
	if posted["groupname"] != "team" {
		t.Errorf("create body = %v, want groupname=team", posted)
	}
	if got := buf.String(); got != "Created group \"team\"\n" {
		t.Errorf("group create output = %q", got)
	}
}

func TestGroupDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups/team", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "team"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "team"})
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
	root.SetArgs([]string{"group", "delete", "team", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group delete: %v", err)
	}
	if deleted != "team" {
		t.Errorf("server saw delete of %q, want team", deleted)
	}
	if got := buf.String(); got != "Deleted group \"team\"\n" {
		t.Errorf("group delete output = %q", got)
	}
}

// groupMemberServer serves GET and PUT for a single group, recording
// the actors.users slice from any PUT body.
func groupMemberServer(t *testing.T, name string, users []string, gotUsers *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(cinc.Group{GroupName: name, Name: name, Users: users})
		case http.MethodPut:
			var body struct {
				Actors struct {
					Users []string `json:"users"`
				} `json:"actors"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*gotUsers = body.Actors.Users
			_ = json.NewEncoder(w).Encode(cinc.Group{GroupName: name, Name: name, Users: body.Actors.Users})
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGroupMemberAddCommandEndToEnd(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice"}, &gotUsers)

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
	root.SetArgs([]string{"group", "member", "add", "admins", "bob", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group member add: %v", err)
	}
	if !slices.Equal(gotUsers, []string{"alice", "bob"}) {
		t.Errorf("PUT users = %v, want [alice bob]", gotUsers)
	}
	if got := buf.String(); got != "Added bob to group \"admins\"\n" {
		t.Errorf("group member add output = %q", got)
	}
}

func TestGroupMemberRemoveCommandEndToEnd(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "admins", []string{"alice", "bob"}, &gotUsers)

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
	root.SetArgs([]string{"group", "member", "remove", "admins", "bob", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group member remove: %v", err)
	}
	if !slices.Equal(gotUsers, []string{"alice"}) {
		t.Errorf("PUT users = %v, want [alice]", gotUsers)
	}
	if got := buf.String(); got != "Removed bob from group \"admins\"\n" {
		t.Errorf("group member remove output = %q", got)
	}
}

func TestGroupShowCommandEndToEnd(t *testing.T) {
	group := cinc.Group{
		Name:      "admins",
		GroupName: "admins",
		OrgName:   "acme",
		Users:     []string{"alice", "pivotal"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/groups/admins", func(w http.ResponseWriter, r *http.Request) {
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
	root.SetArgs([]string{"group", "show", "admins", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group show: %v", err)
	}

	var got cinc.Group
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.GroupName != "admins" {
		t.Errorf("show returned groupname=%q, want admins", got.GroupName)
	}
	if !slices.Equal(got.Users, []string{"alice", "pivotal"}) {
		t.Errorf("show users = %v", got.Users)
	}
}

func withStubGroupEditor(t *testing.T, stub func(*cinc.Group) (*cinc.Group, error)) {
	t.Helper()
	orig := editGroup
	editGroup = stub
	t.Cleanup(func() { editGroup = orig })
}

func TestGroupEditCommandPutsEditorResult(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "devs", []string{"alice"}, &gotUsers)

	withStubGroupEditor(t, func(in *cinc.Group) (*cinc.Group, error) {
		out := *in
		out.Users = append(slices.Clone(in.Users), "carol")
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
	root.SetArgs([]string{"group", "edit", "devs", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group edit: %v", err)
	}
	if !slices.Equal(gotUsers, []string{"alice", "carol"}) {
		t.Errorf("PUT users = %v, want [alice carol]", gotUsers)
	}
	if got := buf.String(); got != "Updated group \"devs\"\n" {
		t.Errorf("group edit output = %q", got)
	}
}

func TestGroupEditCommandReadsFromFile(t *testing.T) {
	var gotUsers []string
	srv := groupMemberServer(t, "devs", []string{"alice"}, &gotUsers)

	withStubGroupEditor(t, func(*cinc.Group) (*cinc.Group, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "group.json")
	body, _ := json.Marshal(cinc.Group{GroupName: "ignored", Users: []string{"dave"}})
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
	root.SetArgs([]string{"group", "edit", "devs", "--file", filePath, "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc group edit --file: %v", err)
	}
	if !slices.Equal(gotUsers, []string{"dave"}) {
		t.Errorf("PUT users = %v, want [dave]", gotUsers)
	}
}
