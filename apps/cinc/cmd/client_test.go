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

// clientServer starts an httptest server that serves a client index for
// org "acme" and returns the server.
func clientServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/clients/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchClientNamesReturnsSortedNames(t *testing.T) {
	srv := clientServer(t, "worker-02", "admin", "worker-01")

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

	names, err := fetchClientNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchClientNames: %v", err)
	}
	want := []string{"admin", "worker-01", "worker-02"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchClientNames = %v, want %v", names, want)
	}
}

func TestClientListCommandEndToEnd(t *testing.T) {
	srv := clientServer(t, "worker-02", "admin", "worker-01")

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
	root.SetArgs([]string{"client", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client list: %v", err)
	}
	if got := buf.String(); got != "admin\nworker-01\nworker-02\n" {
		t.Errorf("client list output = %q, want sorted client names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"admin", "worker-01", "worker-02"}) {
		t.Errorf("json list output = %v, want [admin worker-01 worker-02]", names)
	}
}

// clientCreateServer returns an httptest server whose POST handler
// records the create request body it received and replies with the
// supplied response body and 201 status. The recorded body lets each
// test assert what the CLI actually sent (name, validator, public key).
func clientCreateServer(t *testing.T, gotBody *[]byte, respBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, r *http.Request) {
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

func writeCreateConfig(t *testing.T, serverURL string) string {
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

func TestClientCreateCommandPrintsPrivateKeyToStdout(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJ...\n-----END RSA PRIVATE KEY-----\n"
	var gotBody []byte
	srv := clientCreateServer(t, &gotBody,
		fmt.Sprintf(`{"uri":"http://x/clients/worker-01","chef_key":{"private_key":%q}}`, privKey))

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "create", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client create: %v", err)
	}

	var sent cinc.APIClient
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if sent.Name != "worker-01" || sent.Validator {
		t.Errorf("server saw %+v, want name=worker-01 validator=false", sent)
	}
	if got := buf.String(); got != privKey {
		t.Errorf("stdout = %q, want raw private key", got)
	}
}

func TestClientCreateCommandWithValidatorFlag(t *testing.T) {
	var gotBody []byte
	srv := clientCreateServer(t, &gotBody,
		`{"uri":"http://x/clients/validator1","chef_key":{"private_key":"-----BEGIN-----"}}`)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"client", "create", "validator1", "--validator", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client create --validator: %v", err)
	}

	var sent cinc.APIClient
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if !sent.Validator {
		t.Errorf("server saw validator=%v, want true", sent.Validator)
	}
}

func TestClientCreateCommandWritesKeyToFile(t *testing.T) {
	const privKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJ\n-----END RSA PRIVATE KEY-----\n"
	var gotBody []byte
	srv := clientCreateServer(t, &gotBody,
		fmt.Sprintf(`{"uri":"http://x/clients/worker-02","chef_key":{"private_key":%q}}`, privKey))

	keyPath := filepath.Join(t.TempDir(), "worker-02.pem")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "create", "worker-02", "--key-file", keyPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client create --key-file: %v", err)
	}
	got, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != privKey {
		t.Errorf("key file = %q, want raw private key", got)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 0600", perm)
	}
	if out := buf.String(); !strings.Contains(out, "Created client \"worker-02\"") || !strings.Contains(out, keyPath) {
		t.Errorf("stdout = %q, want confirmation referencing key file", out)
	}
}

func TestClientCreateCommandWithPublicKeyFile(t *testing.T) {
	pubPEM := []byte("-----BEGIN PUBLIC KEY-----\nMIIBIjAN\n-----END PUBLIC KEY-----\n")
	pubPath := filepath.Join(t.TempDir(), "byo.pub")
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotBody []byte
	srv := clientCreateServer(t, &gotBody,
		`{"uri":"http://x/clients/worker-03"}`)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "create", "worker-03", "--public-key", pubPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client create --public-key: %v", err)
	}

	var sent cinc.APIClient
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if sent.ChefKey.PublicKey != string(pubPEM) {
		t.Errorf("server saw public_key %q, want %q", sent.ChefKey.PublicKey, pubPEM)
	}
	if got := buf.String(); got != "Created client \"worker-03\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

// clientEditServer returns an httptest server that responds to GET
// with the supplied current client state and captures the PUT body
// (decoded into *gotPut) for assertion. gotPath records the path of
// the last PUT so tests can verify routing.
func clientEditServer(t *testing.T, name string, current cinc.APIClient, gotPut *cinc.APIClient, gotPath *string) *httptest.Server {
	t.Helper()
	path := "/organizations/acme/clients/" + name
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

// withStubEditor replaces editClient with stub for the duration of
// the test and restores the original on cleanup.
func withStubEditor(t *testing.T, stub func(*cinc.APIClient) (*cinc.APIClient, error)) {
	t.Helper()
	orig := editClient
	editClient = stub
	t.Cleanup(func() { editClient = orig })
}

func TestClientEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.APIClient
	var gotPath string
	current := cinc.APIClient{Name: "worker-01", Validator: false}
	srv := clientEditServer(t, "worker-01", current, &gotPut, &gotPath)

	withStubEditor(t, func(in *cinc.APIClient) (*cinc.APIClient, error) {
		updated := *in
		updated.Validator = true
		return &updated, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "edit", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client edit: %v", err)
	}
	if gotPath != "/organizations/acme/clients/worker-01" {
		t.Errorf("PUT path = %q", gotPath)
	}
	if !gotPut.Validator || gotPut.Name != "worker-01" {
		t.Errorf("PUT body = %+v, want validator=true name=worker-01", gotPut)
	}
	if got := buf.String(); got != "Updated client \"worker-01\"\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestClientEditCommandSkipsPutWhenUnchanged(t *testing.T) {
	var gotPut cinc.APIClient
	var gotPath string
	current := cinc.APIClient{Name: "worker-01", Validator: false}
	srv := clientEditServer(t, "worker-01", current, &gotPut, &gotPath)

	withStubEditor(t, func(in *cinc.APIClient) (*cinc.APIClient, error) {
		copy := *in
		return &copy, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "edit", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client edit (unchanged): %v", err)
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT at %q for an unchanged edit", gotPath)
	}
	if got := buf.String(); got != "Client \"worker-01\" unchanged\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestClientEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.APIClient
	var gotPath string
	current := cinc.APIClient{Name: "worker-01", Validator: false}
	srv := clientEditServer(t, "worker-01", current, &gotPut, &gotPath)

	// Editor must NOT be invoked when --file is supplied; stub it to
	// fail the test if it is.
	withStubEditor(t, func(*cinc.APIClient) (*cinc.APIClient, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "client.json")
	body, err := json.Marshal(cinc.APIClient{Name: "ignored-in-file", Validator: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "edit", "worker-01", "--file", filePath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client edit --file: %v", err)
	}
	if !gotPut.Validator {
		t.Errorf("PUT body validator = %v, want true", gotPut.Validator)
	}
	if gotPut.Name != "worker-01" {
		t.Errorf("PUT body name = %q, want worker-01 (path arg must win over file)", gotPut.Name)
	}
}

func TestClientEditCommandRejectsInvalidFileJSON(t *testing.T) {
	var gotPut cinc.APIClient
	var gotPath string
	current := cinc.APIClient{Name: "worker-01"}
	srv := clientEditServer(t, "worker-01", current, &gotPut, &gotPath)

	filePath := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(filePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"client", "edit", "worker-01", "--file", filePath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err == nil {
		t.Error("expected an error for malformed JSON in --file")
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT for a malformed --file at %q", gotPath)
	}
}

func TestClientDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "worker-01"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "worker-01"})
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
	root.SetArgs([]string{"client", "delete", "worker-01", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client delete: %v", err)
	}
	if deleted != "worker-01" {
		t.Errorf("server saw delete of %q, want %q", deleted, "worker-01")
	}
	if got := buf.String(); got != "Deleted client \"worker-01\"\n" {
		t.Errorf("client delete output = %q", got)
	}
}

func TestClientShowCommandEndToEnd(t *testing.T) {
	current := cinc.APIClient{Name: "worker-01", Validator: false}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(current)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "show", "worker-01", "--config", writeCreateConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client show: %v", err)
	}

	var got cinc.APIClient
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != "worker-01" || got.Validator {
		t.Errorf("show returned %+v, want name=worker-01 validator=false", got)
	}
}

func TestClientShowCommandHumanFormatIsPrettyJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients/worker-01", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cinc.APIClient{Name: "worker-01", Validator: true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"client", "show", "worker-01", "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc client show: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\"name\": \"worker-01\"") {
		t.Errorf("human show output missing pretty-printed name field:\n%s", out)
	}
	if !strings.Contains(out, "\"validator\": true") {
		t.Errorf("human show output missing validator field:\n%s", out)
	}
}

func TestClientListCommandReportsConfigError(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"client", "list", "--config", filepath.Join(t.TempDir(), "missing.toml")})

	if err := root.Execute(); err == nil {
		t.Error("expected an error when the config file is missing")
	}
}
