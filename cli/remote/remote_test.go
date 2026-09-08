package remote

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// shortTempDir returns a temp directory with a path short enough to hold a
// unix socket. t.TempDir() embeds the test name and easily exceeds the 104
// byte sun_path limit on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cincsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// serveAgentSocket listens on path and reports, on the returned channel, when
// a connected client closes its end.
func serveAgentSocket(t *testing.T, path string) <-chan struct{} {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	closed := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Blocks until the peer goes away; Read then returns io.EOF.
		_, _ = conn.Read(make([]byte, 1))
		closed <- struct{}{}
	}()
	return closed
}

// TestAuthMethodsClosesAgentConnection pins the agent socket as a resource
// with an owner. Each `cinc node ssh` host dials the agent once, so leaking
// the connection exhausts the process file descriptor limit partway through a
// fleet run (the macOS default of 256 is reached quickly).
func TestAuthMethodsClosesAgentConnection(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "a.sock")
	closed := serveAgentSocket(t, sock)

	_, cleanup, err := authMethods(SSHOptions{User: "tim", UseAgent: true, AgentSocket: sock})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not close the agent socket connection")
	}
}

// TestAuthMethodsUsesOnlyOneAgent guards against blowing sshd's MaxAuthTries.
// Every key an agent holds costs one authentication attempt, so offering two
// agents can exhaust the default budget of 6 before the password method is
// ever reached.
func TestAuthMethodsUsesOnlyOneAgent(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	serveAgentSocket(t, filepath.Join(home, ".1password", "agent.sock"))

	sshAuth := filepath.Join(shortTempDir(t), "s.sock")
	serveAgentSocket(t, sshAuth)
	t.Setenv("SSH_AUTH_SOCK", sshAuth)

	methods, cleanup, err := authMethods(SSHOptions{User: "tim", UseAgent: true})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if len(methods) != 1 {
		t.Fatalf("auth methods = %d, want 1 (a single agent) even with two reachable sockets", len(methods))
	}
}

// TestAgentSocketCandidatesPrefersSSHAuthSockOverGuesses pins the order that
// decides which agent wins. An exported SSH_AUTH_SOCK is the user saying which
// agent to use; the 1Password paths are guesses for when it is unset.
func TestAgentSocketCandidatesPrefersSSHAuthSockOverGuesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")

	got := agentSocketCandidates("")
	if len(got) == 0 || got[0] != "/tmp/ssh-agent.sock" {
		t.Fatalf("agent socket candidates = %#v, want SSH_AUTH_SOCK first", got)
	}
}

// stubRunner fails the hosts named in fail and records what it was asked to run.
type stubRunner struct {
	mu   sync.Mutex
	ran  []string
	fail map[string]bool
}

func (r *stubRunner) Run(_ context.Context, target Target, _ string, _ SSHOptions) CommandResult {
	r.mu.Lock()
	r.ran = append(r.ran, target.Host)
	r.mu.Unlock()
	if r.fail[target.Host] {
		return CommandResult{Host: target.Host, ExitCode: 1, Stderr: "boom\n"}
	}
	return CommandResult{Host: target.Host, Stdout: "ok\n"}
}

func testTargets(n int) []Target {
	targets := make([]Target, n)
	for i := range targets {
		targets[i] = Target{Host: fmt.Sprintf("h%02d", i)}
	}
	return targets
}

// TestRunManyExitOnErrorMarksRemainingSkipped pins what --exit-on-error means.
// The flag says it stops launching new sessions after a failure, so the hosts
// that were never attempted are skipped, not failed: reporting them as
// failures tells the operator that twenty machines broke when one command
// returned non-zero.
func TestRunManyExitOnErrorMarksRemainingSkipped(t *testing.T) {
	targets := testTargets(20)
	runner := &stubRunner{fail: map[string]bool{"h00": true}}

	results := RunMany(context.Background(), runner, targets, "true", SSHOptions{}, 1, true)

	if len(results) != len(targets) {
		t.Fatalf("results = %d, want %d", len(results), len(targets))
	}
	if results[0].ExitCode != 1 {
		t.Errorf("h00 should have failed, got %+v", results[0])
	}
	for i, r := range results[1:] {
		if !r.Skipped {
			t.Fatalf("result %d (%s) = %+v, want it marked skipped", i+1, r.Host, r)
		}
		if r.ExitCode != 0 {
			t.Errorf("skipped host %s should not carry a failure exit code, got %d", r.Host, r.ExitCode)
		}
		if r.Host == "" {
			t.Errorf("skipped result %d has no host name", i+1)
		}
	}
	if len(runner.ran) != 1 {
		t.Errorf("runner attempted %v, want only h00", runner.ran)
	}
}

// barrierRunner blocks every command until `n` of them have started, so the
// test can be sure all of them are genuinely in flight together.
type barrierRunner struct {
	mu      sync.Mutex
	ran     []string
	fail    map[string]bool
	arrived chan struct{}
	release chan struct{}
}

func (r *barrierRunner) Run(_ context.Context, target Target, _ string, _ SSHOptions) CommandResult {
	r.mu.Lock()
	r.ran = append(r.ran, target.Host)
	r.mu.Unlock()
	r.arrived <- struct{}{}
	<-r.release
	if r.fail[target.Host] {
		return CommandResult{Host: target.Host, ExitCode: 1, Stderr: "boom\n"}
	}
	return CommandResult{Host: target.Host, Stdout: "ok\n"}
}

// TestRunManyExitOnErrorLetsInFlightWorkFinish covers the other half of the
// flag's promise. Cancelling a shared context to stop the queue also killed
// commands that had already started; only new launches should stop.
func TestRunManyExitOnErrorLetsInFlightWorkFinish(t *testing.T) {
	const n = 4
	targets := testTargets(n)
	runner := &barrierRunner{
		fail:    map[string]bool{"h00": true},
		arrived: make(chan struct{}, n),
		release: make(chan struct{}),
	}

	done := make(chan []CommandResult, 1)
	go func() {
		done <- RunMany(context.Background(), runner, targets, "true", SSHOptions{}, n, true)
	}()

	// Wait until all four are inside Run, then let them all return at once.
	for i := 0; i < n; i++ {
		<-runner.arrived
	}
	close(runner.release)

	for _, r := range <-done {
		if r.Skipped {
			t.Errorf("host %s was skipped even though it had already started", r.Host)
		}
	}
	if len(runner.ran) != n {
		t.Errorf("runner attempted %v, want all %d hosts", runner.ran, n)
	}
}

// Without --exit-on-error every host is attempted, failures and all.
func TestRunManyWithoutExitOnErrorRunsEveryHost(t *testing.T) {
	targets := testTargets(5)
	runner := &stubRunner{fail: map[string]bool{"h00": true, "h02": true}}

	results := RunMany(context.Background(), runner, targets, "true", SSHOptions{}, 2, false)

	if len(runner.ran) != 5 {
		t.Errorf("runner attempted %v, want all five hosts", runner.ran)
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("host %s was skipped without --exit-on-error", r.Host)
		}
	}
}

func TestApplyOpenSSHConfigUsesIdentityAgent(t *testing.T) {
	old := sshConfigGet
	sshConfigGet = func(host, key string) (string, error) {
		if host != "node-0b0715" || key != "IdentityAgent" {
			return "", fmt.Errorf("unexpected lookup %s %s", host, key)
		}
		return "~/.1password/agent.sock", nil
	}
	t.Cleanup(func() { sshConfigGet = old })

	opts, err := applyOpenSSHConfig("node-0b0715", SSHOptions{UseAgent: true})
	if err != nil {
		t.Fatalf("applyOpenSSHConfig: %v", err)
	}
	if opts.AgentSocket != "~/.1password/agent.sock" {
		t.Fatalf("AgentSocket = %q", opts.AgentSocket)
	}
}

func TestApplyOpenSSHConfigHonorsIdentityAgentNone(t *testing.T) {
	old := sshConfigGet
	sshConfigGet = func(_, _ string) (string, error) { return "none", nil }
	t.Cleanup(func() { sshConfigGet = old })

	opts, err := applyOpenSSHConfig("host", SSHOptions{UseAgent: true})
	if err != nil {
		t.Fatalf("applyOpenSSHConfig: %v", err)
	}
	if opts.UseAgent {
		t.Fatal("UseAgent = true, want disabled by IdentityAgent none")
	}
}

func TestApplyOpenSSHConfigKeepsExplicitAgentSocket(t *testing.T) {
	old := sshConfigGet
	sshConfigGet = func(_, _ string) (string, error) {
		t.Fatal("ssh config should not be read when AgentSocket is explicit")
		return "", nil
	}
	t.Cleanup(func() { sshConfigGet = old })

	opts, err := applyOpenSSHConfig("host", SSHOptions{UseAgent: true, AgentSocket: "/tmp/agent.sock"})
	if err != nil {
		t.Fatalf("applyOpenSSHConfig: %v", err)
	}
	if opts.AgentSocket != "/tmp/agent.sock" {
		t.Fatalf("AgentSocket = %q", opts.AgentSocket)
	}
}

func TestAgentSocketCandidatesIncludesOnePasswordFallbacks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")

	// The explicit --ssh-agent-socket wins, then the agent the environment
	// names, then the 1Password paths as guesses for when SSH_AUTH_SOCK is
	// unset. Order decides which agent is used, because only the first
	// reachable socket is dialed.
	got := agentSocketCandidates("~/explicit-agent.sock")
	want := []string{
		filepath.Join(home, "explicit-agent.sock"),
		"/tmp/ssh-agent.sock",
		filepath.Join(home, ".1password", "agent.sock"),
		filepath.Join(home, "Library", "Group Containers", "2BUA8C4S2C.com.1password", "t", "agent.sock"),
	}
	if len(got) != len(want) {
		t.Fatalf("agent socket candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("agent socket candidate %d = %q, want %q", i, got[i], want[i])
		}
	}
}
