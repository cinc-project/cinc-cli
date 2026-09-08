package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// databagServer starts an httptest server that serves a databag index
// for org "acme" and returns the server. The Chef API exposes data bags
// under /data, not /data_bags.
func databagServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/data/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchDataBagNamesReturnsSortedNames(t *testing.T) {
	srv := databagServer(t, "users", "apps", "secrets")

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

	names, err := fetchDataBagNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchDataBagNames: %v", err)
	}
	want := []string{"apps", "secrets", "users"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchDataBagNames = %v, want %v", names, want)
	}
}

// databagItemServer serves the per-bag item index at /data/<bag> and
// returns the server.
func databagItemIndexServer(t *testing.T, bag string, ids ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(ids))
	for _, id := range ids {
		index[id] = "https://example.test/data/" + bag + "/" + id
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data/"+bag, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchDataBagItemIDsReturnsSortedIDs(t *testing.T) {
	srv := databagItemIndexServer(t, "users", "bob", "alice", "carol")

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

	ids, err := fetchDataBagItemIDs(context.Background(), c, "users")
	if err != nil {
		t.Fatalf("fetchDataBagItemIDs: %v", err)
	}
	want := []string{"alice", "bob", "carol"}
	if !slices.Equal(ids, want) {
		t.Errorf("fetchDataBagItemIDs = %v, want %v", ids, want)
	}
}

func TestDataBagItemListCommandEndToEnd(t *testing.T) {
	srv := databagItemIndexServer(t, "users", "bob", "alice", "carol")

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
	root.SetArgs([]string{"databag", "item", "list", "users", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item list: %v", err)
	}
	if got := buf.String(); got != "alice\nbob\ncarol\n" {
		t.Errorf("databag item list output = %q, want sorted item ids", got)
	}

	// The same list under --format json renders a JSON array of the ids.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "list", "users", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item list --format json: %v", err)
	}
	var ids []string
	if err := json.Unmarshal(buf.Bytes(), &ids); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(ids, []string{"alice", "bob", "carol"}) {
		t.Errorf("json list output = %v, want [alice bob carol]", ids)
	}
}

func TestDataBagCreateCommandEndToEnd(t *testing.T) {
	var created string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal create body: %v", err)
		}
		created = payload.Name
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "create", "secrets", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag create: %v", err)
	}
	if created != "secrets" {
		t.Errorf("server saw create of %q, want %q", created, "secrets")
	}
	if got := buf.String(); got != "Created data bag \"secrets\"\n" {
		t.Errorf("databag create output = %q", got)
	}
}

// databagCreateBagAndItemServer responds to POST /data with 201 (or
// the supplied bagStatus, allowing 409 simulations) and to POST
// /data/<bag> with 201, capturing the item body for assertions.
func databagCreateBagAndItemServer(t *testing.T, bag string, bagStatus int, gotItem *cinc.DataBagItem) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q on /data, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		status := bagStatus
		if status == 0 {
			status = http.StatusCreated
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusConflict {
			_, _ = w.Write([]byte(`{"error":["Data bag '` + bag + `' already exists"]}`))
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/organizations/acme/data/"+bag, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q on /data/%s, want POST", r.Method, bag)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, gotItem); err != nil {
			t.Fatalf("unmarshal item body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDataBagCreateCommandWithItemUsesEditor(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagCreateBagAndItemServer(t, "secrets", 0, &gotItem)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		// Editor seed should carry id; user fills in additional fields.
		if in["id"] != "db-password" {
			t.Errorf("editor seed id = %v, want db-password", in["id"])
		}
		out := cinc.DataBagItem{"id": "db-password", "password": "hunter2"}
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "create", "secrets", "db-password", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag create with item: %v", err)
	}
	if gotItem["id"] != "db-password" || gotItem["password"] != "hunter2" {
		t.Errorf("server received item %+v, want id=db-password password=hunter2", gotItem)
	}
	got := buf.String()
	if !strings.Contains(got, "Created data bag \"secrets\"") {
		t.Errorf("missing bag-create line:\n%s", got)
	}
	if !strings.Contains(got, "Created item \"db-password\" in data bag \"secrets\"") {
		t.Errorf("missing item-create line:\n%s", got)
	}
}

func TestDataBagCreateCommandWithItemReadsFromFile(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagCreateBagAndItemServer(t, "secrets", 0, &gotItem)

	withStubDataBagItemEditor(t, func(cinc.DataBagItem) (cinc.DataBagItem, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "item.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "ignored-in-file", "password": "from-file"})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "create", "secrets", "db-password", "--file", filePath, "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag create --file: %v", err)
	}
	if gotItem["id"] != "db-password" {
		t.Errorf("PUT body id = %v, want db-password (path arg must win over file)", gotItem["id"])
	}
	if gotItem["password"] != "from-file" {
		t.Errorf("PUT body password = %v, want from-file", gotItem["password"])
	}
}

func TestDataBagCreateCommandItemSkipsConflictOnBag(t *testing.T) {
	var gotItem cinc.DataBagItem
	// Server returns 409 for the bag-create; the command should
	// continue and create the item anyway.
	srv := databagCreateBagAndItemServer(t, "secrets", http.StatusConflict, &gotItem)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		out := cinc.DataBagItem{}
		for k, v := range in {
			out[k] = v
		}
		out["password"] = "hunter2"
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "create", "secrets", "db-password", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag create with item (bag exists): %v", err)
	}
	if gotItem["id"] != "db-password" {
		t.Errorf("server did not receive item: gotItem = %+v", gotItem)
	}
	got := buf.String()
	if strings.Contains(got, "Created data bag") {
		t.Errorf("output should not claim bag creation when it already existed:\n%s", got)
	}
	if !strings.Contains(got, "Created item \"db-password\" in data bag \"secrets\"") {
		t.Errorf("missing item-create line:\n%s", got)
	}
}

func TestDataBagCreateCommandBagOnlySurfacesConflict(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagCreateBagAndItemServer(t, "secrets", http.StatusConflict, &gotItem)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "create", "secrets", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err == nil {
		t.Error("expected an error when creating an existing bag with no item")
	}
}

func TestDataBagDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "users"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "users"})
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
	root.SetArgs([]string{"databag", "delete", "users", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag delete: %v", err)
	}
	if deleted != "users" {
		t.Errorf("server saw delete of %q, want %q", deleted, "users")
	}
	if got := buf.String(); got != "Deleted data bag \"users\"\n" {
		t.Errorf("databag delete output = %q", got)
	}
}

// databagItemServer serves GET of the supplied current item and
// records the PUT body into *gotPut, *gotPath for assertion.
func databagItemServer(t *testing.T, bag, id string, current cinc.DataBagItem, gotPut *cinc.DataBagItem, gotPath *string) *httptest.Server {
	t.Helper()
	path := "/organizations/acme/data/" + bag + "/" + id
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			*gotPath = r.URL.Path
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, gotPut); err != nil {
				t.Fatalf("unmarshal PUT body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected method %q on %s", r.Method, r.URL.Path)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withStubDataBagItemEditor(t *testing.T, stub func(cinc.DataBagItem) (cinc.DataBagItem, error)) {
	t.Helper()
	orig := editDataBagItem
	editDataBagItem = stub
	t.Cleanup(func() { editDataBagItem = orig })
}

func writeDataBagConfig(t *testing.T, serverURL string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, serverURL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestDataBagItemEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := cinc.DataBagItem{"id": "alice", "role": "admin"}
	srv := databagItemServer(t, "users", "alice", current, &gotPut, &gotPath)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		out := cinc.DataBagItem{}
		for k, v := range in {
			out[k] = v
		}
		out["role"] = "editor"
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "edit", "users", "alice", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item edit: %v", err)
	}
	if gotPath != "/organizations/acme/data/users/alice" {
		t.Errorf("PUT path = %q", gotPath)
	}
	if gotPut["role"] != "editor" || gotPut["id"] != "alice" {
		t.Errorf("PUT body = %+v, want id=alice role=editor", gotPut)
	}
	if got := buf.String(); got != "Updated item \"alice\" in bag \"users\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestDataBagItemEditCommandSkipsPutWhenUnchanged(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := cinc.DataBagItem{"id": "alice", "role": "admin"}
	srv := databagItemServer(t, "users", "alice", current, &gotPut, &gotPath)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		out := cinc.DataBagItem{}
		for k, v := range in {
			out[k] = v
		}
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "edit", "users", "alice", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item edit (unchanged): %v", err)
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT at %q for an unchanged edit", gotPath)
	}
	if got := buf.String(); got != "Item \"alice\" in bag \"users\" unchanged\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestDataBagItemEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := cinc.DataBagItem{"id": "alice", "role": "admin"}
	srv := databagItemServer(t, "users", "alice", current, &gotPut, &gotPath)

	withStubDataBagItemEditor(t, func(cinc.DataBagItem) (cinc.DataBagItem, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "item.json")
	body, err := json.Marshal(cinc.DataBagItem{"id": "ignored-in-file", "role": "editor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "item", "edit", "users", "alice", "--file", filePath, "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item edit --file: %v", err)
	}
	if gotPut["role"] != "editor" {
		t.Errorf("PUT body role = %v, want editor", gotPut["role"])
	}
	if gotPut["id"] != "alice" {
		t.Errorf("PUT body id = %v, want alice (path arg must win over file)", gotPut["id"])
	}
}

func TestDataBagItemEditCommandRejectsFileMissingID(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := cinc.DataBagItem{"id": "alice"}
	srv := databagItemServer(t, "users", "alice", current, &gotPut, &gotPath)

	filePath := filepath.Join(t.TempDir(), "item.json")
	if err := os.WriteFile(filePath, []byte(`{"role": "editor"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "item", "edit", "users", "alice", "--file", filePath, "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err == nil {
		t.Error("expected an error for a --file payload with no id")
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT %q despite validation failure", gotPath)
	}
}

func TestDataBagShowCommandEndToEnd(t *testing.T) {
	srv := databagItemIndexServer(t, "users", "bob", "alice", "carol")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "show", "users", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag show: %v", err)
	}
	if got := buf.String(); got != "alice\nbob\ncarol\n" {
		t.Errorf("databag show output = %q, want sorted item ids", got)
	}
}

func TestDataBagItemShowCommandEndToEnd(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := cinc.DataBagItem{"id": "alice", "role": "admin"}
	srv := databagItemServer(t, "users", "alice", current, &gotPut, &gotPath)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "show", "users", "alice", "--config", writeDataBagConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item show: %v", err)
	}
	if gotPath != "" {
		t.Errorf("show issued a PUT at %q, want read-only", gotPath)
	}
	var got cinc.DataBagItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got["id"] != "alice" || got["role"] != "admin" {
		t.Errorf("show item = %+v, want id=alice role=admin", got)
	}
}

func TestDataBagItemDeleteCommandEndToEnd(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data/users/alice", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cinc.DataBagItem{"id": "alice"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "delete", "users", "alice", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item delete: %v", err)
	}
	if !deleted {
		t.Error("server never saw the DELETE")
	}
	if got := buf.String(); got != "Deleted item \"alice\" from data bag \"users\"\n" {
		t.Errorf("databag item delete output = %q", got)
	}
}

// databagItemCreateServer responds to POST /data/<bag> with 201, capturing
// the item body for assertions.
func databagItemCreateServer(t *testing.T, bag string, gotItem *cinc.DataBagItem) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data/"+bag, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q on /data/%s, want POST", r.Method, bag)
		}
		if err := json.NewDecoder(r.Body).Decode(gotItem); err != nil {
			t.Fatalf("decode item body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(gotItem)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDataBagItemCreateCommandUsesEditor(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "secrets", &gotItem)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		if in["id"] != "db-password" {
			t.Errorf("editor seed id = %v, want db-password", in["id"])
		}
		return cinc.DataBagItem{"id": "db-password", "password": "hunter2"}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "item", "create", "secrets", "db-password", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item create: %v", err)
	}
	if gotItem["id"] != "db-password" || gotItem["password"] != "hunter2" {
		t.Errorf("server received item %+v, want id=db-password password=hunter2", gotItem)
	}
	if got := buf.String(); got != "Created item \"db-password\" in data bag \"secrets\"\n" {
		t.Errorf("databag item create output = %q", got)
	}
}

func TestDataBagItemCreateCommandReadsFromFile(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "secrets", &gotItem)

	withStubDataBagItemEditor(t, func(cinc.DataBagItem) (cinc.DataBagItem, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "item.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "ignored-in-file", "password": "from-file"})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "item", "create", "secrets", "db-password", "--file", filePath, "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag item create --file: %v", err)
	}
	if gotItem["id"] != "db-password" {
		t.Errorf("POST body id = %v, want db-password (path arg must win over file)", gotItem["id"])
	}
	if gotItem["password"] != "from-file" {
		t.Errorf("POST body password = %v, want from-file", gotItem["password"])
	}
}

func TestDataBagListCommandEndToEnd(t *testing.T) {
	srv := databagServer(t, "users", "apps", "secrets")

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
	root.SetArgs([]string{"databag", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag list: %v", err)
	}
	if got := buf.String(); got != "apps\nsecrets\nusers\n" {
		t.Errorf("databag list output = %q, want sorted databag names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc databag list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"apps", "secrets", "users"}) {
		t.Errorf("json list output = %v, want [apps secrets users]", names)
	}
}
