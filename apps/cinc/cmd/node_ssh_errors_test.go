package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/remote"
)

// hostRunner answers each host with its own canned result.
type hostRunner struct {
	mu      sync.Mutex
	results map[string]remote.CommandResult
	calls   int
}

func (r *hostRunner) Run(_ context.Context, target remote.Target, _ string, _ remote.SSHOptions) remote.CommandResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	result := r.results[target.Host]
	result.Host = target.Host
	return result
}

func useRunner(t *testing.T, r remote.Runner) {
	t.Helper()
	old := nodeRemoteRunner
	nodeRemoteRunner = r
	t.Cleanup(func() { nodeRemoteRunner = old })
}

// runNode runs the command tree with empty stdin, so prompts get no answer,
// and returns stdout and the error.
func runNode(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func wantErrMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "—") {
		t.Errorf("error should not use an em dash, got:\n%s", err)
	}
}

func TestNodeSSHNamesTheMissingUser(t *testing.T) {
	_, err := runNode(t, "node", "ssh", "role:web", "uptime", "--no-host-key-verify")
	wantErrMentions(t, err, "--ssh-user", "cinc node ssh role:web uptime --ssh-user ubuntu")
	if strings.Contains(err.Error(), "search query") {
		t.Errorf("the query was given, so the error shouldn't ask for it:\n%s", err)
	}
}

func TestNodeSSHExplainsAMissingQuery(t *testing.T) {
	_, err := runNode(t, "node", "ssh", "--ssh-user", "ubuntu", "--no-host-key-verify")
	wantErrMentions(t, err, "which nodes", "command to run", "role:web", "--skip-search", "cinc node ssh role:web uptime --ssh-user ubuntu")
}

func TestNodeBootstrapNamesTheMissingUser(t *testing.T) {
	_, err := runNode(t, "node", "bootstrap", "web01.example.com", "--dry-run")
	wantErrMentions(t, err, "--ssh-user", "cinc node bootstrap web01.example.com --ssh-user ubuntu")
}

func TestNodeSSHNoHostsExplainsSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/node", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"total":0,"start":0,"rows":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfgPath := writeCommandConfig(t, srv.URL)
	runner := &hostRunner{}
	useRunner(t, runner)

	_, err := runNode(t, "node", "ssh", "web", "uptime", "--ssh-user", "ubuntu", "--config", cfgPath, "--no-host-key-verify")
	wantErrMentions(t, err, `"web"`, "tags:*web*", "role:web", "cinc search node", "--skip-search")
	if runner.calls != 0 {
		t.Errorf("runner ran %d times with no hosts", runner.calls)
	}
}

// TestNodeSSHFailureSummaryExplainsEachProblemOnce: a fleet run where many
// hosts fail the same way should explain that problem once, name the hosts
// it hit, and not repeat a long explanation per host.
func TestNodeSSHFailureSummaryExplainsEachProblemOnce(t *testing.T) {
	useRunner(t, &hostRunner{results: map[string]remote.CommandResult{
		"web01.example.com": {ExitCode: 255, Reason: remote.ReasonUnknownHostKey, Error: "web01.example.com isn't in your known_hosts file", Hint: "HINT-web01"},
		"web02.example.com": {ExitCode: 255, Reason: remote.ReasonUnknownHostKey, Error: "web02.example.com isn't in your known_hosts file", Hint: "HINT-web02"},
		"web03.example.com": {ExitCode: 1, Error: "Process exited with status 1", Stderr: "nginx: not found\n"},
		"web04.example.com": {Stdout: "ok\n"},
	}})

	out, err := runNode(t, "node", "ssh", "web01.example.com web02.example.com web03.example.com web04.example.com", "nginx -t",
		"--skip-search", "--ssh-user", "ubuntu", "--no-host-key-verify")
	wantErrMentions(t, err,
		"failed on 3 of 4 hosts",
		"web01.example.com: web01.example.com isn't in your known_hosts file",
		"HINT-web01",
		"web02.example.com",
		"web03.example.com",
	)
	if strings.Contains(err.Error(), "HINT-web02") {
		t.Errorf("the same problem should be explained once, got:\n%s", err)
	}
	if !strings.Contains(out, "web01.example.com\tweb01.example.com isn't in your known_hosts file") {
		t.Errorf("per-host output should carry the one-line error, got:\n%s", out)
	}
	if strings.Contains(out, "HINT") {
		t.Errorf("per-host output should not repeat the explanation, got:\n%s", out)
	}
}

func TestNodeBootstrapScriptFailureShowsOutputAndCleanup(t *testing.T) {
	useRunner(t, &hostRunner{results: map[string]remote.CommandResult{
		"web01.example.com": {ExitCode: 1, Error: "Process exited with status 1", Stderr: "curl: (6) Could not resolve host: omnitruck.cinc.sh\n"},
	}})
	srv := bootstrapClientServer(t, new(bool))
	cfgPath := writeCommandConfig(t, srv.URL)

	_, err := runNode(t, "node", "bootstrap", "web01.example.com", "--node-name", "web01",
		"--ssh-user", "ubuntu", "--config", cfgPath, "--no-host-key-verify")
	wantErrMentions(t, err,
		"web01.example.com",
		"status 1",
		"curl: (6) Could not resolve host: omnitruck.cinc.sh",
		`client named "web01"`,
		"cinc client delete web01",
		"cinc node show web01",
		"cinc node delete web01",
	)
}

func TestNodeBootstrapLoginFailureExplainsAndCleansUp(t *testing.T) {
	useRunner(t, &hostRunner{results: map[string]remote.CommandResult{
		"web01.example.com": {ExitCode: 255, Reason: remote.ReasonAuth, Error: `web01.example.com didn't accept the login for user "ubuntu"`, Hint: "HINT-auth"},
	}})
	srv := bootstrapClientServer(t, new(bool))
	cfgPath := writeCommandConfig(t, srv.URL)

	_, err := runNode(t, "node", "bootstrap", "web01.example.com", "--node-name", "web01",
		"--ssh-user", "ubuntu", "--config", cfgPath, "--no-host-key-verify")
	wantErrMentions(t, err, "didn't accept the login", "HINT-auth", "Nothing was installed", "cinc client delete web01")
	if strings.Contains(err.Error(), "cinc node delete") {
		t.Errorf("Cinc Client never ran, so there's no node to delete:\n%s", err)
	}
}

func bootstrapClientServer(t *testing.T, created *bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, r *http.Request) {
		*created = true
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"web01","validator":false}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestNodeBootstrapChecksSSHSetupBeforeCreatingClient: bootstrap registers a
// client on the server before connecting, so a bad --ssh-identity-file found
// only at connect time leaves a client behind that blocks the retry.
func TestNodeBootstrapChecksSSHSetupBeforeCreatingClient(t *testing.T) {
	runner := &hostRunner{}
	useRunner(t, runner)
	created := false
	srv := bootstrapClientServer(t, &created)
	cfgPath := writeCommandConfig(t, srv.URL)
	missing := filepath.Join(t.TempDir(), "id_missing")

	_, err := runNode(t, "node", "bootstrap", "web01.example.com", "--node-name", "web01",
		"--ssh-user", "ubuntu", "--ssh-identity-file", missing, "--config", cfgPath, "--no-host-key-verify")
	wantErrMentions(t, err, missing)
	if created {
		t.Error("a client was created before the SSH key file was checked")
	}
	if runner.calls != 0 {
		t.Error("bootstrap connected before the SSH key file was checked")
	}
}

func TestNodeBootstrapRejectsNewlineNodeNameBeforeCreatingClient(t *testing.T) {
	useRunner(t, &hostRunner{})
	created := false
	srv := bootstrapClientServer(t, &created)
	cfgPath := writeCommandConfig(t, srv.URL)

	_, err := runNode(t, "node", "bootstrap", "web01.example.com", "--node-name", "web01\nid",
		"--ssh-user", "ubuntu", "--config", cfgPath, "--no-host-key-verify")
	wantErrMentions(t, err, "line break", "--node-name")
	if created {
		t.Error("a client was created for a node name the script refuses")
	}
}

func TestNodeSSHChecksSSHSetupBeforeSearching(t *testing.T) {
	runner := &hostRunner{}
	useRunner(t, runner)
	missing := filepath.Join(t.TempDir(), "id_missing")

	_, err := runNode(t, "node", "ssh", "web01.example.com", "uptime", "--skip-search",
		"--ssh-user", "ubuntu", "--ssh-identity-file", missing, "--no-host-key-verify")
	wantErrMentions(t, err, missing, "--ssh-identity-file")
	if runner.calls != 0 {
		t.Errorf("runner ran %d times with an unusable key file", runner.calls)
	}
}

func TestNodeBootstrapPolicyFlagErrorsExplainTheChoice(t *testing.T) {
	cfgPath := writeCommandConfig(t, "https://cinc.example.com")
	for name, tc := range map[string]struct {
		args  []string
		wants []string
	}{
		"name without group": {
			[]string{"--policy-name", "base"},
			[]string{"--policy-group", "cinc node bootstrap web01.example.com --ssh-user ubuntu --policy-name base --policy-group prod"},
		},
		"group without name": {
			[]string{"--policy-group", "prod"},
			[]string{"--policy-name", "--policy-group prod"},
		},
		"run-list with policy": {
			[]string{"--policy-name", "base", "--policy-group", "prod", "--run-list", "recipe[nginx]"},
			[]string{"--run-list", "run-list or from a Policyfile", `"base"`},
		},
		"environment with policy": {
			[]string{"--policy-name", "base", "--policy-group", "prod", "--environment", "production"},
			[]string{"--environment", "policy group", `"prod"`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"node", "bootstrap", "web01.example.com", "--ssh-user", "ubuntu", "--config", cfgPath, "--dry-run"}, tc.args...)
			_, err := runNode(t, args...)
			wantErrMentions(t, err, tc.wants...)
		})
	}
}
