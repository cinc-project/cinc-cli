package remote

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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
