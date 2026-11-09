package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeTempJSON writes body to name in a fresh temp dir and returns the path.
func writeTempJSON(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A misspelled key in a --file document would be silently dropped by a plain
// unmarshal, and the edit would report success without the change. It has to
// fail the way the editor does, naming the file and the key.
func TestReadJSONFileRejectsUnknownField(t *testing.T) {
	path := writeTempJSON(t, "role.json", `{"name":"web","run_list":[],"overide_attributes":{"a":1}}`)
	_, err := readJSONFile[cinc.Role](path)
	if err == nil {
		t.Fatal("expected an error for a misspelled key")
	}
	for _, want := range []string{"role.json", `"overide_attributes"`, "Check the spelling"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

// A group has its own UnmarshalJSON, which a decoder's DisallowUnknownFields
// does not reach. The check still has to catch a typo in a group file.
func TestReadJSONFileRejectsUnknownGroupField(t *testing.T) {
	path := writeTempJSON(t, "group.json", `{"groupname":"devs","usres":["alice"]}`)
	if _, err := readJSONFile[cinc.Group](path); err == nil || !strings.Contains(err.Error(), `"usres"`) {
		t.Fatalf("expected an unknown-field error naming \"usres\", got %v", err)
	}
}

// Files exported from knife or chef carry json_class and chef_type, which the
// cinc types don't model. They must still load.
func TestReadJSONFileAcceptsKnifeKeys(t *testing.T) {
	path := writeTempJSON(t, "role.json", `{"name":"web","json_class":"Chef::Role","chef_type":"role","run_list":["recipe[nginx]"]}`)
	role, err := readJSONFile[cinc.Role](path)
	if err != nil {
		t.Fatalf("knife export should load: %v", err)
	}
	if role.Name != "web" || len(role.RunList) != 1 {
		t.Errorf("decoded role = %+v", role)
	}
}

// A data bag item is free-form, so any key is the user's data.
func TestReadJSONFileDataBagItemAcceptsAnyKey(t *testing.T) {
	path := writeTempJSON(t, "item.json", `{"id":"alice","overide_attributes":1,"anything":{"x":true}}`)
	item, err := readJSONFile[cinc.DataBagItem](path)
	if err != nil {
		t.Fatalf("data bag item should load: %v", err)
	}
	if _, ok := item["overide_attributes"]; !ok {
		t.Errorf("item lost a key: %v", item)
	}
}

func TestReadJSONFileRejectsMalformed(t *testing.T) {
	path := writeTempJSON(t, "bad.json", `{`)
	if _, err := readJSONFile[cinc.Role](path); err == nil || !strings.Contains(err.Error(), "bad.json isn't valid JSON") {
		t.Fatalf("expected an invalid-JSON error, got %v", err)
	}
}

// The editor and --file share one decoder, so the editor accepts the knife
// keys too and catches group typos.
func TestStrictJSONMatchesFileRules(t *testing.T) {
	if err := strictJSON[cinc.Role]()([]byte(`{"name":"web","json_class":"Chef::Role","chef_type":"role"}`)); err != nil {
		t.Errorf("editor should accept json_class and chef_type: %v", err)
	}
	err := strictJSON[cinc.Group]()([]byte(`{"groupname":"devs","usres":[]}`))
	if err == nil || !strings.Contains(err.Error(), `"usres"`) {
		t.Errorf("editor should reject a group typo, got %v", err)
	}
}

// A typo in the file must stop the edit before anything reaches the server.
func TestRoleEditFileWithTypoSendsNoRequest(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	path := writeTempJSON(t, "role.json", `{"name":"web","run_list":[],"overide_attributes":{"a":1}}`)
	_, _, err := runRoot(t, "role", "edit", "web", "--file", path, "--config", groupMemberConfig(t, srv))
	if err == nil || !strings.Contains(err.Error(), `"overide_attributes"`) {
		t.Fatalf("expected an unknown-field error, got %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("server got %d requests, want none", n)
	}
}

// fileRoundTripCases is one entry per noun whose edit takes --file. get is
// what a real server (erchef) returns on GET, which is also what `knife ...
// show -F json` prints, extra keys and all.
var fileRoundTripCases = []struct {
	noun string
	path string
	show []string
	edit []string
	get  string
}{
	{
		noun: "node", path: "/organizations/acme/nodes/web01",
		show: []string{"node", "show", "web01"}, edit: []string{"node", "edit", "web01"},
		get: `{"name":"web01","chef_environment":"_default","json_class":"Chef::Node","chef_type":"node",
			"automatic":{"os":"linux"},"normal":{"a":1},"default":{},"override":{},"run_list":["recipe[nginx]"],
			"policy_name":null,"policy_group":null}`,
	},
	{
		noun: "role", path: "/organizations/acme/roles/web",
		show: []string{"role", "show", "web"}, edit: []string{"role", "edit", "web"},
		get: `{"name":"web","description":"","json_class":"Chef::Role","chef_type":"role",
			"default_attributes":{},"override_attributes":{"a":1},"run_list":["recipe[nginx]"],"env_run_lists":{}}`,
	},
	{
		noun: "environment", path: "/organizations/acme/environments/prod",
		show: []string{"environment", "show", "prod"}, edit: []string{"environment", "edit", "prod"},
		get: `{"name":"prod","description":"","cookbook_versions":{"nginx":"= 1.0.0"},"json_class":"Chef::Environment",
			"chef_type":"environment","default_attributes":{},"override_attributes":{}}`,
	},
	{
		noun: "client", path: "/organizations/acme/clients/worker",
		show: []string{"client", "show", "worker"}, edit: []string{"client", "edit", "worker"},
		get: `{"name":"worker","clientname":"worker","validator":false,"orgname":"acme",
			"json_class":"Chef::ApiClient","chef_type":"client"}`,
	},
	{
		noun: "user", path: "/users/alice",
		show: []string{"user", "show", "alice"}, edit: []string{"user", "edit", "alice"},
		get: `{"username":"alice","email":"alice@example.com","display_name":"Alice","first_name":"Alice",
			"last_name":"Smith","middle_name":"","city":"Seattle","country":"USA","twitter_account":"alice",
			"image_file_name":"","recovery_authentication_enabled":false,"external_authentication_uid":"alice"}`,
	},
	{
		noun: "group", path: "/organizations/acme/groups/devs",
		show: []string{"group", "show", "devs"}, edit: []string{"group", "edit", "devs"},
		get: `{"actors":["alice","worker"],"users":["alice"],"clients":["worker"],"groups":[],
			"orgname":"acme","name":"devs","groupname":"devs"}`,
	},
	{
		noun: "org", path: "/organizations/acme",
		show: []string{"org", "show", "acme"}, edit: []string{"org", "edit", "acme"},
		get: `{"name":"acme","full_name":"Acme Inc","guid":"0123456789abcdef0123456789abcdef"}`,
	},
	{
		noun: "client key", path: "/organizations/acme/clients/worker/keys/default",
		show: []string{"client", "key", "show", "worker", "default"}, edit: []string{"client", "key", "edit", "worker", "default"},
		get: `{"name":"default","public_key":"-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----\n","expiration_date":"infinity"}`,
	},
}

// fileRoundTripServer serves get on GET and records each PUT at path.
func fileRoundTripServer(t *testing.T, path, get string, puts *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			puts.Add(1)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(get))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// `cinc <noun> show --format json > f.json; cinc <noun> edit x --file f.json`
// has to keep working for every noun, as does feeding edit a knife export
// (the server's GET body verbatim).
func TestFileRoundTripEveryNoun(t *testing.T) {
	for _, tc := range fileRoundTripCases {
		t.Run(tc.noun+"/show then edit", func(t *testing.T) {
			var puts atomic.Int32
			srv := fileRoundTripServer(t, tc.path, tc.get, &puts)
			cfg := groupMemberConfig(t, srv)

			out, _, err := runRoot(t, append(tc.show, "--format", "json", "--config", cfg)...)
			if err != nil {
				t.Fatalf("show: %v", err)
			}
			file := writeTempJSON(t, "export.json", out)
			if _, _, err := runRoot(t, append(tc.edit, "--file", file, "--config", cfg)...); err != nil {
				t.Fatalf("edit --file with show's output: %v\nfile:\n%s", err, out)
			}
			if puts.Load() != 1 {
				t.Errorf("server got %d PUTs, want 1", puts.Load())
			}
		})
		t.Run(tc.noun+"/knife export", func(t *testing.T) {
			var puts atomic.Int32
			srv := fileRoundTripServer(t, tc.path, tc.get, &puts)
			file := writeTempJSON(t, "export.json", tc.get)
			if _, _, err := runRoot(t, append(tc.edit, "--file", file, "--config", groupMemberConfig(t, srv))...); err != nil {
				t.Fatalf("edit --file with the server's GET body: %v", err)
			}
			if puts.Load() != 1 {
				t.Errorf("server got %d PUTs, want 1", puts.Load())
			}
		})
	}
}
