package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// withStubKeyEditor swaps editKey for a stub for the test's duration.
func withStubKeyEditor(t *testing.T, stub func(*cinc.Key) (*cinc.Key, error)) {
	t.Helper()
	orig := editKey
	editKey = stub
	t.Cleanup(func() { editKey = orig })
}

func TestKeyListCommandRendersSortedNames(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"name":"k2"},{"name":"default"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "list", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key list: %v", err)
	}
	if got := buf.String(); got != "default\nk2\n" {
		t.Errorf("key list output = %q, want sorted key names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "list", "worker-01", "--config", writeCreateConfig(t, srv.URL), "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if len(names) != 2 || names[0] != "default" || names[1] != "k2" {
		t.Errorf("json list output = %v, want [default k2]", names)
	}
}

func TestKeyShowCommandReturnsJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01/keys/default", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"default","expiration_date":"infinity","expired":false}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "show", "worker-01", "default", "--config", writeCreateConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key show: %v", err)
	}
	var got cinc.Key
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\n%s", err, buf.String())
	}
	if got.Name != "default" || got.ExpirationDate != "infinity" {
		t.Errorf("show returned %+v, want name=default expiration=infinity", got)
	}
}

// keyCreateServer records the POST body sent to a key collection and
// replies with respBody at 201.
func keyCreateServer(t *testing.T, path string, gotBody *[]byte, respBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		*gotBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, respBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestKeyCreateServerGeneratedPrintsPrivateKey(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nGEN\n-----END RSA PRIVATE KEY-----\n"
	var gotBody []byte
	srv := keyCreateServer(t, "/organizations/acme/clients/worker-01/keys", &gotBody,
		`{"uri":"http://x/clients/worker-01/keys/k2","private_key":"`+strings.ReplaceAll(privKey, "\n", `\n`)+`"}`)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "create", "worker-01", "k2", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key create: %v", err)
	}

	var sent cinc.Key
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if sent.Name != "k2" || !sent.CreateKey || sent.ExpirationDate != "infinity" {
		t.Errorf("server saw %+v, want name=k2 create_key=true expiration=infinity", sent)
	}
	if got := buf.String(); got != privKey {
		t.Errorf("stdout = %q, want raw private key", got)
	}
}

func TestKeyCreateWithExpiresAndKeyFile(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nGEN\n-----END RSA PRIVATE KEY-----\n"
	var gotBody []byte
	srv := keyCreateServer(t, "/organizations/acme/clients/worker-01/keys", &gotBody,
		`{"private_key":"`+strings.ReplaceAll(privKey, "\n", `\n`)+`"}`)

	keyPath := filepath.Join(t.TempDir(), "k2.pem")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "create", "worker-01", "k2",
		"--expires", "2030-01-01T00:00:00Z", "--key-file", keyPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key create --expires --key-file: %v", err)
	}

	var sent cinc.Key
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if sent.ExpirationDate != "2030-01-01T00:00:00Z" {
		t.Errorf("server saw expiration_date %q, want the --expires value", sent.ExpirationDate)
	}
	got, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != privKey {
		t.Errorf("key file = %q, want raw private key", got)
	}
	if out := buf.String(); !strings.Contains(out, "Added key \"k2\"") || !strings.Contains(out, keyPath) {
		t.Errorf("stdout = %q, want confirmation referencing key file", out)
	}
}

func TestKeyCreateWithPublicKeyFile(t *testing.T) {
	pubPEM := []byte("-----BEGIN PUBLIC KEY-----\nMIIBIjAN\n-----END PUBLIC KEY-----\n")
	pubPath := filepath.Join(t.TempDir(), "byo.pub")
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotBody []byte
	srv := keyCreateServer(t, "/organizations/acme/clients/worker-01/keys", &gotBody,
		`{"uri":"http://x/clients/worker-01/keys/byo"}`)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "create", "worker-01", "byo", "--public-key", pubPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key create --public-key: %v", err)
	}

	var sent cinc.Key
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if sent.PublicKey != string(pubPEM) {
		t.Errorf("server saw public_key %q, want %q", sent.PublicKey, pubPEM)
	}
	if sent.CreateKey {
		t.Error("create_key should be false when a public key is supplied")
	}
	if got := buf.String(); got != "Added key \"byo\" to client \"worker-01\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

// keyEditServer answers GET with the current key and captures the PUT body
// (decoded into *gotPut) and path (*gotPath).
func keyEditServer(t *testing.T, path string, current cinc.Key, gotPut *cinc.Key, gotPath *string) *httptest.Server {
	t.Helper()
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

func TestKeyEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.Key
	var gotPath string
	current := cinc.Key{Name: "default", ExpirationDate: "infinity"}
	srv := keyEditServer(t, "/organizations/acme/clients/worker-01/keys/default", current, &gotPut, &gotPath)

	withStubKeyEditor(t, func(in *cinc.Key) (*cinc.Key, error) {
		edited := *in
		edited.ExpirationDate = "2031-01-01T00:00:00Z"
		return &edited, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "edit", "worker-01", "default", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key edit: %v", err)
	}
	if gotPath != "/organizations/acme/clients/worker-01/keys/default" {
		t.Errorf("PUT path = %q", gotPath)
	}
	if gotPut.ExpirationDate != "2031-01-01T00:00:00Z" || gotPut.Name != "default" {
		t.Errorf("PUT body = %+v, want updated expiration and name=default", gotPut)
	}
	if got := buf.String(); got != "Updated key \"default\" on client \"worker-01\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestKeyEditCommandSkipsPutWhenUnchanged(t *testing.T) {
	var gotPut cinc.Key
	var gotPath string
	current := cinc.Key{Name: "default", ExpirationDate: "infinity"}
	srv := keyEditServer(t, "/organizations/acme/clients/worker-01/keys/default", current, &gotPut, &gotPath)

	withStubKeyEditor(t, func(in *cinc.Key) (*cinc.Key, error) {
		clone := *in
		return &clone, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "edit", "worker-01", "default", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key edit (unchanged): %v", err)
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT at %q for an unchanged edit", gotPath)
	}
	if got := buf.String(); got != "Key \"default\" on client \"worker-01\" unchanged\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestKeyEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.Key
	var gotPath string
	current := cinc.Key{Name: "default", ExpirationDate: "infinity"}
	srv := keyEditServer(t, "/organizations/acme/clients/worker-01/keys/default", current, &gotPut, &gotPath)

	withStubKeyEditor(t, func(*cinc.Key) (*cinc.Key, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "key.json")
	body, err := json.Marshal(cinc.Key{ExpirationDate: "2032-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"client", "key", "edit", "worker-01", "default", "--file", filePath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key edit --file: %v", err)
	}
	if gotPut.ExpirationDate != "2032-01-01T00:00:00Z" {
		t.Errorf("PUT body expiration = %q, want the --file value", gotPut.ExpirationDate)
	}
	// An empty name in the file is backfilled from the path arg.
	if gotPut.Name != "default" {
		t.Errorf("PUT body name = %q, want default (backfilled from path)", gotPut.Name)
	}
}

// TestWritePrivateKeyForces0600 confirms the written key is mode 0600 even
// when a looser-permissioned file already exists at the path (the
// os.WriteFile-with-mode approach would have preserved the old 0644).
func TestWritePrivateKeyForces0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pem")
	// Pre-create a world/group-readable file at the destination.
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := writePrivateKey(&out, "-----BEGIN KEY-----\nabc\n-----END KEY-----\n", path, "wrote it"); err != nil {
		t.Fatalf("writePrivateKey: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("key file mode = %o, want 600", got)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "BEGIN KEY") {
		t.Errorf("key file body = %q, want the new private key", body)
	}
}

func TestKeyDeleteCommand(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01/keys/k2", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "k2"
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"k2"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "key", "delete", "worker-01", "k2", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client key delete: %v", err)
	}
	if deleted != "k2" {
		t.Errorf("server saw delete of %q, want k2", deleted)
	}
	if got := buf.String(); got != "Deleted key \"k2\" from client \"worker-01\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

// TestUserKeyListUsesUserScope proves the shared key builder targets the
// per-user (not org-scoped) keys path when wired under `cinc user`.
func TestUserKeyListUsesUserScope(t *testing.T) {
	var hitPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/users/alice/keys", func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"name":"default"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"user", "key", "list", "alice", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc user key list: %v", err)
	}
	if hitPath != "/users/alice/keys" {
		t.Errorf("user key list hit %q, want the global /users path", hitPath)
	}
	if got := buf.String(); got != "default\n" {
		t.Errorf("stdout = %q", got)
	}
}

// reregisterServer handles the DELETE of the default key followed by the
// POST that recreates it, recording both so tests can assert the sequence.
func reregisterServer(t *testing.T, name, privKey string, sawDelete, sawCreate *bool, createBody *[]byte) *httptest.Server {
	t.Helper()
	base := "/organizations/acme/clients/" + name + "/keys"
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/default", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method on %s = %q, want DELETE", r.URL.Path, r.Method)
		}
		*sawDelete = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"default"}`)
	})
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method on %s = %q, want POST", r.URL.Path, r.Method)
		}
		*sawCreate = true
		body, _ := io.ReadAll(r.Body)
		*createBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"private_key":"`+strings.ReplaceAll(privKey, "\n", `\n`)+`"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClientReregisterRegeneratesDefaultKey(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n"
	var sawDelete, sawCreate bool
	var createBody []byte
	srv := reregisterServer(t, "worker-01", privKey, &sawDelete, &sawCreate, &createBody)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "reregister", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client reregister: %v", err)
	}
	if !sawDelete || !sawCreate {
		t.Errorf("reregister calls: delete=%v create=%v, want both true", sawDelete, sawCreate)
	}
	var sent cinc.Key
	if err := json.Unmarshal(createBody, &sent); err != nil {
		t.Fatalf("unmarshal create body: %v", err)
	}
	if sent.Name != "default" || !sent.CreateKey {
		t.Errorf("recreate body = %+v, want name=default create_key=true", sent)
	}
	if got := buf.String(); got != privKey {
		t.Errorf("stdout = %q, want the new private key", got)
	}
}

func TestClientReregisterWritesKeyToFile(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nNEW\n-----END RSA PRIVATE KEY-----\n"
	var sawDelete, sawCreate bool
	var createBody []byte
	srv := reregisterServer(t, "worker-01", privKey, &sawDelete, &sawCreate, &createBody)

	keyPath := filepath.Join(t.TempDir(), "worker-01.pem")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "reregister", "worker-01", "--key-file", keyPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client reregister --key-file: %v", err)
	}
	got, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != privKey {
		t.Errorf("key file = %q, want the new private key", got)
	}
	if out := buf.String(); !strings.Contains(out, "Reregistered client \"worker-01\"") || !strings.Contains(out, keyPath) {
		t.Errorf("stdout = %q, want confirmation referencing key file", out)
	}
}
