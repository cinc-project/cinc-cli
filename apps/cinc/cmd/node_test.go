package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	cinc "github.com/tas50/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/remote"
)

// writeTestKey generates an RSA key, writes it as PEM to a temp file, and
// returns the path.
func writeTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// nodeServer starts an httptest server that serves a node index for org
// "acme" and returns the server.
func nodeServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	index := make(map[string]string, len(names))
	for _, n := range names {
		index[n] = "https://example.test/nodes/" + n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchNodeNamesReturnsSortedNames(t *testing.T) {
	srv := nodeServer(t, "web02", "db01", "web01")

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

	names, err := fetchNodeNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchNodeNames: %v", err)
	}
	want := []string{"db01", "web01", "web02"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchNodeNames = %v, want %v", names, want)
	}
}

func TestNodeListCommandEndToEnd(t *testing.T) {
	srv := nodeServer(t, "web02", "db01", "web01")

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
	root.SetArgs([]string{"node", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node list: %v", err)
	}
	if got := buf.String(); got != "db01\nweb01\nweb02\n" {
		t.Errorf("node list output = %q, want sorted node names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"db01", "web01", "web02"}) {
		t.Errorf("json list output = %v, want [db01 web01 web02]", names)
	}
}

func TestNodeDeleteCommandEndToEnd(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/web01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deleted = "web01"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "web01"})
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
	root.SetArgs([]string{"node", "delete", "web01", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node delete: %v", err)
	}
	if deleted != "web01" {
		t.Errorf("server saw delete of %q, want %q", deleted, "web01")
	}
	if got := buf.String(); got != "Deleted node \"web01\"\n" {
		t.Errorf("node delete output = %q", got)
	}
}

func TestNodeShowCommandEndToEnd(t *testing.T) {
	node := cinc.Node{
		Name:        "web01",
		Environment: "prod",
		RunList:     []string{"recipe[apache]", "recipe[base]"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/web01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(node)
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
	root.SetArgs([]string{"node", "show", "web01", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node show: %v", err)
	}

	var got cinc.Node
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != "web01" || got.Environment != "prod" {
		t.Errorf("show returned %+v, want name=web01 environment=prod", got)
	}
	if !slices.Equal(got.RunList, []string{"recipe[apache]", "recipe[base]"}) {
		t.Errorf("show run_list = %v", got.RunList)
	}
}

// nodeShowHuman runs `cinc node show <name>` in the default (human) format
// against an httptest server returning the given node, and returns stdout.
func nodeShowHuman(t *testing.T, node cinc.Node) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/"+node.Name, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(node)
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
	root.SetArgs([]string{"node", "show", node.Name, "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node show: %v", err)
	}
	return buf.String()
}

func TestNodeShowCommandHumanFormat(t *testing.T) {
	out := nodeShowHuman(t, cinc.Node{
		Name:        "web01",
		Environment: "prod",
		RunList:     []string{"recipe[apache]", "role[base]"},
		Automatic: cinc.Attributes{
			"platform":         "ubuntu",
			"platform_version": "22.04",
		},
	})

	// The node name is the first line; in a non-TTY buffer it is unstyled.
	if first, _, _ := strings.Cut(out, "\n"); first != "web01" {
		t.Errorf("first line = %q, want the node name %q", first, "web01")
	}
	for _, want := range []string{
		"Platform", "ubuntu",
		"Platform Version", "22.04",
		"Run List", "recipe[apache], role[base]",
		"Environment", "prod",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
	// Human format is a summary, not the raw object dump.
	if strings.Contains(out, "\"run_list\"") {
		t.Errorf("human output should not be JSON:\n%s", out)
	}
}

func TestNodeShowCommandHumanFormatPolicyNode(t *testing.T) {
	out := nodeShowHuman(t, cinc.Node{
		Name:        "app01",
		RunList:     []string{},
		PolicyName:  "base",
		PolicyGroup: "prod",
	})

	for _, want := range []string{"Policy Name", "base", "Policy Group", "prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
	// A node that never converged has no platform facts; don't show empty rows.
	if strings.Contains(out, "Platform") {
		t.Errorf("empty platform should be omitted:\n%s", out)
	}
	// Environment always shows, defaulting to the server's _default.
	if !strings.Contains(out, "_default") {
		t.Errorf("empty environment should render as _default:\n%s", out)
	}
}

func TestNodeListCommandReportsConfigError(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"node", "list", "--config", filepath.Join(t.TempDir(), "missing.toml")})

	if err := root.Execute(); err == nil {
		t.Error("expected an error when the config file is missing")
	}
}

func TestNodeSSHSkipSearchCommand(t *testing.T) {
	runner := &recordingRunner{result: remote.CommandResult{Stdout: "ok\n"}}
	old := nodeRemoteRunner
	nodeRemoteRunner = runner
	t.Cleanup(func() { nodeRemoteRunner = old })

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"node", "ssh", "web01 web02", "uptime",
		"--skip-search",
		"--ssh-user", "ubuntu",
		"--ssh-agent-socket", "/tmp/cinc-agent.sock",
		"--no-host-key-verify",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node ssh: %v", err)
	}
	if got, want := buf.String(), "web01\tok\nweb02\tok\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	slices.SortFunc(runner.calls, func(a, b remoteCall) int {
		return strings.Compare(a.target.Host, b.target.Host)
	})
	if len(runner.calls) != 2 || runner.calls[0].target.Host != "web01" || runner.calls[1].target.Host != "web02" {
		t.Fatalf("runner calls = %+v", runner.calls)
	}
	if runner.calls[0].opts.User != "ubuntu" || runner.calls[0].opts.AgentSocket != "/tmp/cinc-agent.sock" || runner.calls[0].opts.VerifyHost {
		t.Fatalf("ssh opts = %+v", runner.calls[0].opts)
	}
}

func TestNodeSSHSearchBackedCommand(t *testing.T) {
	runner := &recordingRunner{result: remote.CommandResult{Stdout: "done\n"}}
	old := nodeRemoteRunner
	nodeRemoteRunner = runner
	t.Cleanup(func() { nodeRemoteRunner = old })

	var sawQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/node", func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"total":1,"start":0,"rows":[{"automatic":{"fqdn":"web01.example.test"}}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeCommandConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"node", "ssh", "web", "hostname",
		"--ssh-user", "ubuntu",
		"--config", cfgPath,
		"--no-host-key-verify",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node ssh: %v", err)
	}
	if !strings.Contains(sawQuery, "tags:*web*") || !strings.Contains(sawQuery, "fqdn:*web*") {
		t.Fatalf("query = %q, want expanded Knife-style node query", sawQuery)
	}
	if len(runner.calls) != 1 || runner.calls[0].target.Host != "web01.example.test" {
		t.Fatalf("runner calls = %+v", runner.calls)
	}
	if got := buf.String(); got != "web01.example.test\tdone\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestNodeBootstrapPolicyExcludesEnvironmentInFirstBoot(t *testing.T) {
	cfgPath := writeCommandConfig(t, "https://cinc.example.test")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"node", "bootstrap", "web01.example.test",
		"--node-name", "web01",
		"--ssh-user", "ubuntu",
		"--policy-name", "base",
		"--policy-group", "prod",
		"--config", cfgPath,
		"--dry-run",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node bootstrap --dry-run: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "chef_environment") {
		t.Fatalf("policy bootstrap must not emit chef_environment:\n%s", got)
	}
	for _, want := range []string{`"policy_name": "base"`, `"policy_group": "prod"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("policy bootstrap output missing %q:\n%s", want, got)
		}
	}
}

func TestNodeBootstrapRejectsEnvironmentWithPolicy(t *testing.T) {
	cfgPath := writeCommandConfig(t, "https://cinc.example.test")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{
		"node", "bootstrap", "web01.example.test",
		"--ssh-user", "ubuntu",
		"--policy-name", "base",
		"--policy-group", "prod",
		"--environment", "prod",
		"--config", cfgPath,
		"--dry-run",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error combining --environment with policy flags")
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Fatalf("error should mention environment: %v", err)
	}
}

// TestNodeBootstrapDryRunRejectsProfileWithoutServer covers a profile that is
// perfectly valid on disk but names no Cinc Server, such as a Supermarket-only
// one. A real bootstrap catches this in resolveClient; a dry run never calls
// it, and used to emit a script whose client.rb pointed at "/organizations/".
func TestNodeBootstrapDryRunRejectsProfileWithoutServer(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
supermarket_site = "https://supermarket.cinc.sh"
client_name      = "tim"
client_key       = %q
`, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{
		"node", "bootstrap", "web01.example.test",
		"--ssh-user", "ubuntu", "--config", cfgPath, "--dry-run",
	})

	if err := root.Execute(); err == nil {
		t.Fatalf("dry run accepted a profile with no server URL and printed:\n%s", out.String())
	}
	if strings.Contains(out.String(), "chef_server_url") {
		t.Errorf("dry run emitted a bootstrap script anyway:\n%s", out.String())
	}
}

func TestNodeBootstrapDryRunCommand(t *testing.T) {
	cfgPath := writeCommandConfig(t, "https://cinc.example.test")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"node", "bootstrap", "web01.example.test",
		"--node-name", "web01",
		"--ssh-user", "ubuntu",
		"--run-list", "recipe[apt],recipe[nginx]",
		"--config", cfgPath,
		"--dry-run",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node bootstrap --dry-run: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"curl -L 'https://omnitruck.cinc.sh/install.sh'",
		"chef_server_url 'https://cinc.example.test/organizations/acme'",
		"node_name 'web01'",
		"\"run_list\": [",
		"recipe[nginx]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, got)
		}
	}
}

func TestNodeBootstrapCreatesClientAndRunsRemoteCommand(t *testing.T) {
	runner := &recordingRunner{result: remote.CommandResult{Stdout: "bootstrap ok\n"}}
	old := nodeRemoteRunner
	nodeRemoteRunner = runner
	t.Cleanup(func() { nodeRemoteRunner = old })

	var clientBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		clientBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"web01","validator":false}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeCommandConfig(t, srv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"node", "bootstrap", "web01.example.test",
		"--node-name", "web01",
		"--ssh-user", "ubuntu",
		"--config", cfgPath,
		"--no-host-key-verify",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node bootstrap: %v", err)
	}
	if !strings.Contains(clientBody, `"name":"web01"`) {
		t.Fatalf("client create body = %s", clientBody)
	}
	if !strings.Contains(clientBody, `"public_key":"-----BEGIN PUBLIC KEY-----`) {
		t.Fatalf("client create body missing generated public key: %s", clientBody)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %+v", runner.calls)
	}
	call := runner.calls[0]
	if call.target.Host != "web01.example.test" || call.opts.User != "ubuntu" || call.opts.VerifyHost {
		t.Fatalf("runner call = %+v", call)
	}
	if !strings.Contains(call.command, "-----BEGIN RSA PRIVATE KEY-----") || !strings.Contains(call.command, "cinc-client -j /etc/cinc/first-boot.json") {
		t.Fatalf("bootstrap command = %s", call.command)
	}
	if got := buf.String(); got != "Bootstrapped node \"web01\" on web01.example.test\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func writeCommandConfig(t *testing.T, serverURL string) string {
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

type recordingRunner struct {
	mu     sync.Mutex
	calls  []remoteCall
	result remote.CommandResult
}

type remoteCall struct {
	target  remote.Target
	command string
	opts    remote.SSHOptions
}

func (r *recordingRunner) Run(_ context.Context, target remote.Target, command string, opts remote.SSHOptions) remote.CommandResult {
	r.mu.Lock()
	r.calls = append(r.calls, remoteCall{target: target, command: command, opts: opts})
	result := r.result
	r.mu.Unlock()
	result.Host = target.Host
	if result.ExitCode == 0 && result.Error == "" {
		result.ExitCode = 0
	}
	if result.Stdout == "" && r.result.Stdout == "" {
		result.Stdout = "ok\n"
	}
	return result
}

// nodeItemServer serves GET of current at /nodes/<name> and records the PUT
// body into *gotPut.
func nodeItemServer(t *testing.T, name string, current cinc.Node, gotPut *cinc.Node) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/"+name, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(gotPut); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gotPut)
		default:
			t.Errorf("unexpected method %q on %s", r.Method, r.URL.Path)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withStubNodeEditor(t *testing.T, stub func(*cinc.Node) (*cinc.Node, bool, error)) {
	t.Helper()
	orig := editNodeForm
	editNodeForm = stub
	t.Cleanup(func() { editNodeForm = orig })
}

func writeNodeConfig(t *testing.T, serverURL string) string {
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

func TestNodeCreateCommandEndToEnd(t *testing.T) {
	var created cinc.Node
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "create", "web01", "--environment", "prod", "--run-list", "recipe[base],recipe[apache]", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node create: %v", err)
	}
	if created.Name != "web01" || created.Environment != "prod" {
		t.Errorf("server received %+v, want name=web01 environment=prod", created)
	}
	if !slices.Equal(created.RunList, []string{"recipe[base]", "recipe[apache]"}) {
		t.Errorf("create run_list = %v, want from --run-list", created.RunList)
	}
	if got := buf.String(); got != "Created node \"web01\"\n" {
		t.Errorf("node create output = %q", got)
	}
}

func TestNodeCreateCommandDefaultsRunListToEmpty(t *testing.T) {
	var created cinc.Node
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "create", "web01", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node create: %v", err)
	}
	if created.RunList == nil {
		t.Errorf("run_list should serialize as [] not null")
	}
}

func TestNodeCreateCommandReadsFromFile(t *testing.T) {
	var created cinc.Node
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	filePath := filepath.Join(t.TempDir(), "node.json")
	body, _ := json.Marshal(cinc.Node{Name: "ignored", Environment: "staging", RunList: []string{"recipe[base]"}})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "create", "web01", "--file", filePath, "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node create --file: %v", err)
	}
	if created.Name != "web01" {
		t.Errorf("create body name = %q, want web01 (path arg must win over file)", created.Name)
	}
	if created.Environment != "staging" || !slices.Equal(created.RunList, []string{"recipe[base]"}) {
		t.Errorf("create body = %+v, want from file", created)
	}
}

func TestNodeEditCommandPutsEditorResult(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", Environment: "prod", RunList: []string{"recipe[base]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	withStubNodeEditor(t, func(in *cinc.Node) (*cinc.Node, bool, error) {
		out := *in
		out.Environment = "staging"
		return &out, true, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "edit", "web01", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node edit: %v", err)
	}
	if gotPut.Name != "web01" || gotPut.Environment != "staging" {
		t.Errorf("PUT body = %+v, want name=web01 environment=staging", gotPut)
	}
	if got := buf.String(); got != "Updated node \"web01\"\n" {
		t.Errorf("node edit output = %q", got)
	}
}

func TestNodeEditCommandReportsUnchanged(t *testing.T) {
	current := cinc.Node{Name: "web01", Environment: "prod"}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/web01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s on an unchanged edit", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(current)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	withStubNodeEditor(t, func(in *cinc.Node) (*cinc.Node, bool, error) {
		return in, false, nil // no change
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "edit", "web01", "--config", writeNodeConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node edit: %v", err)
	}
	if got := buf.String(); got != "Node \"web01\" unchanged\n" {
		t.Errorf("output = %q, want unchanged message", got)
	}
}

func TestNodeEditCommandReadsFromFile(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{"recipe[base]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	withStubNodeEditor(t, func(*cinc.Node) (*cinc.Node, bool, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, false, nil
	})

	filePath := filepath.Join(t.TempDir(), "node.json")
	body, _ := json.Marshal(cinc.Node{Name: "ignored", Environment: "qa", RunList: []string{"recipe[x]"}})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "edit", "web01", "--file", filePath, "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node edit --file: %v", err)
	}
	if gotPut.Name != "web01" || gotPut.Environment != "qa" {
		t.Errorf("PUT body = %+v, want name=web01 environment=qa", gotPut)
	}
}
