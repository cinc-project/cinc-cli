package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testSSHServer is an in-process SSH server that accepts user "tester" with
// password "secret" and answers every exec with exit status 0.
type testSSHServer struct {
	port    int
	hostKey ssh.PublicKey
}

func startTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == "tester" && string(password) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	config.AddHostKey(signer)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go serveTestSSHConn(conn, config)
		}
	}()
	return &testSSHServer{port: l.Addr().(*net.TCPAddr).Port, hostKey: signer.PublicKey()}
}

func serveTestSSHConn(conn net.Conn, config *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer func() { _ = channel.Close() }()
			for req := range requests {
				_ = req.Reply(req.Type == "exec", nil)
				if req.Type == "exec" {
					var status [4]byte
					_, _ = channel.SendRequest("exit-status", false, status[:])
					return
				}
			}
		}()
	}
}

// isolateHome points HOME at an empty directory with a .ssh folder, so the
// tests never read the developer's own known_hosts, keys, or agent.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeKnownHosts(t *testing.T, home, content string) string {
	t.Helper()
	path := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s *testSSHServer) opts() SSHOptions {
	return SSHOptions{User: "tester", Password: "secret", Port: s.port, Timeout: 5 * time.Second, VerifyHost: true}
}

func wantAll(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s should mention %q, got:\n%s", what, want, got)
		}
	}
	if strings.Contains(got, "—") {
		t.Errorf("%s should not use an em dash, got:\n%s", what, got)
	}
}

func TestRunExplainsUnknownHostKey(t *testing.T) {
	home := isolateHome(t)
	writeKnownHosts(t, home, "")
	s := startTestSSHServer(t)

	r := NativeRunner{}.Run(context.Background(), Target{Host: "127.0.0.1"}, "true", s.opts())

	if r.Reason != ReasonUnknownHostKey {
		t.Fatalf("reason = %q, want %q (result %+v)", r.Reason, ReasonUnknownHostKey, r)
	}
	wantAll(t, "error", r.Error, "127.0.0.1", "known_hosts")
	wantAll(t, "hint", r.Hint,
		ssh.FingerprintSHA256(s.hostKey),
		fmt.Sprintf("ssh -p %d tester@127.0.0.1", s.port),
		fmt.Sprintf("ssh-keyscan -p %d 127.0.0.1 >> %s", s.port, filepath.Join(home, ".ssh", "known_hosts")),
		"--no-host-key-verify",
	)
}

func TestRunExplainsHostKeyMismatch(t *testing.T) {
	home := isolateHome(t)
	s := startTestSSHServer(t)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ssh.NewPublicKey(other)
	if err != nil {
		t.Fatal(err)
	}
	addr := knownhosts.Normalize(fmt.Sprintf("127.0.0.1:%d", s.port))
	khPath := writeKnownHosts(t, home, knownhosts.Line([]string{addr}, otherKey)+"\n")

	r := NativeRunner{}.Run(context.Background(), Target{Host: "127.0.0.1"}, "true", s.opts())

	if r.Reason != ReasonHostKeyMismatch {
		t.Fatalf("reason = %q, want %q (result %+v)", r.Reason, ReasonHostKeyMismatch, r)
	}
	wantAll(t, "error", r.Error, "127.0.0.1", "doesn't match")
	wantAll(t, "hint", r.Hint,
		ssh.FingerprintSHA256(s.hostKey),
		ssh.FingerprintSHA256(otherKey),
		khPath+" line 1",
		"man-in-the-middle",
		fmt.Sprintf("ssh-keygen -R '[127.0.0.1]:%d'", s.port),
	)
}

func TestRunExplainsRejectedLogin(t *testing.T) {
	isolateHome(t)
	s := startTestSSHServer(t)
	opts := s.opts()
	opts.VerifyHost = false
	opts.Password = "wrong"

	r := NativeRunner{}.Run(context.Background(), Target{Host: "127.0.0.1"}, "true", opts)

	if r.Reason != ReasonAuth {
		t.Fatalf("reason = %q, want %q (result %+v)", r.Reason, ReasonAuth, r)
	}
	wantAll(t, "error", r.Error, "127.0.0.1", `"tester"`)
	wantAll(t, "hint", r.Hint, "--ssh-password", "--ssh-user", "authorized_keys", fmt.Sprintf("ssh -p %d tester@127.0.0.1", s.port))
}

func TestRunExplainsConnectionRefused(t *testing.T) {
	isolateHome(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	r := NativeRunner{}.Run(context.Background(), Target{Host: "127.0.0.1"}, "true",
		SSHOptions{User: "tester", Password: "secret", Port: port, Timeout: 5 * time.Second})

	if r.Reason != ReasonRefused {
		t.Fatalf("reason = %q, want %q (result %+v)", r.Reason, ReasonRefused, r)
	}
	wantAll(t, "error", r.Error, "127.0.0.1", fmt.Sprint(port))
	wantAll(t, "hint", r.Hint, "sshd", "--ssh-port")
}

func TestRunExplainsNoWayToLogIn(t *testing.T) {
	isolateHome(t)

	r := NativeRunner{}.Run(context.Background(), Target{Host: "web01.example.com"}, "true",
		SSHOptions{User: "tester", Port: 22})

	if r.Reason != ReasonConfig {
		t.Fatalf("reason = %q, want %q (result %+v)", r.Reason, ReasonConfig, r)
	}
	wantAll(t, "error", r.Error, `"tester"`)
	wantAll(t, "hint", r.Hint, "--ssh-identity-file", "--ssh-password", "--ssh-agent=false")
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestConnectErrorExplainsTimeoutAndUnknownHost(t *testing.T) {
	timeout := connectError("web01.example.com", SSHOptions{User: "ubuntu", Port: 2222, Timeout: 30 * time.Second}, &net.OpError{Op: "dial", Err: timeoutErr{}})
	if timeout.Reason != ReasonTimeout {
		t.Errorf("reason = %q, want %q", timeout.Reason, ReasonTimeout)
	}
	wantAll(t, "timeout error", timeout.Error(), "30s", "web01.example.com", "2222", "--ssh-timeout", "ssh -p 2222 ubuntu@web01.example.com")

	dnsErr := &net.DNSError{Err: "no such host", Name: "web01.example.com", IsNotFound: true}
	dns := connectError("web01.example.com", SSHOptions{User: "ubuntu", Port: 22, Timeout: time.Second}, &net.OpError{Op: "dial", Err: dnsErr})
	if dns.Reason != ReasonDNS {
		t.Errorf("reason = %q, want %q", dns.Reason, ReasonDNS)
	}
	wantAll(t, "dns error", dns.Error(), "web01.example.com", "--attribute ipaddress")
	if !errors.Is(dns, dnsErr) {
		t.Error("the DNS error should still unwrap to *net.DNSError")
	}
}

func TestCheckOptionsNeedsUser(t *testing.T) {
	isolateHome(t)
	err := CheckOptions(SSHOptions{Password: "x"})
	if err == nil {
		t.Fatal("expected an error without a user")
	}
	wantAll(t, "error", err.Error(), "--ssh-user")
}

func TestCheckOptionsExplainsMissingKeyFile(t *testing.T) {
	home := isolateHome(t)
	path := filepath.Join(home, ".ssh", "id_missing")
	err := CheckOptions(SSHOptions{User: "tester", IdentityFile: path})
	if err == nil {
		t.Fatal("expected an error for a missing identity file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should still be fs.ErrNotExist: %v", err)
	}
	wantAll(t, "error", err.Error(), path, "--ssh-identity-file", "~/.ssh")
}

func TestCheckOptionsExplainsPassphraseProtectedKey(t *testing.T) {
	home := isolateHome(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	err = CheckOptions(SSHOptions{User: "tester", IdentityFile: path})
	if err == nil {
		t.Fatal("expected an error for a passphrase-protected key")
	}
	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); !ok {
		t.Errorf("error should still be *ssh.PassphraseMissingError: %v", err)
	}
	wantAll(t, "error", err.Error(), path, "passphrase", "ssh-add "+path)
}

func TestCheckOptionsExplainsPublicKeyGivenAsIdentity(t *testing.T) {
	home := isolateHome(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "id_ed25519.pub")
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(sshPub), 0o644); err != nil {
		t.Fatal(err)
	}

	err = CheckOptions(SSHOptions{User: "tester", IdentityFile: path})
	if err == nil {
		t.Fatal("expected an error for a public key")
	}
	wantAll(t, "error", err.Error(), "public key", "--ssh-identity-file "+strings.TrimSuffix(path, ".pub"))
}

func TestCheckOptionsExplainsMissingKnownHosts(t *testing.T) {
	home := isolateHome(t)
	err := CheckOptions(SSHOptions{User: "tester", Password: "x", VerifyHost: true})
	if err == nil {
		t.Fatal("expected an error without a known_hosts file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should still be fs.ErrNotExist: %v", err)
	}
	wantAll(t, "error", err.Error(), filepath.Join(home, ".ssh", "known_hosts"), "ssh-keyscan", "--no-host-key-verify")
}

func TestCheckOptionsAcceptsAWorkingSetup(t *testing.T) {
	home := isolateHome(t)
	writeKnownHosts(t, home, "")
	if err := CheckOptions(SSHOptions{User: "tester", Password: "x", VerifyHost: true}); err != nil {
		t.Fatalf("CheckOptions: %v", err)
	}
}

func TestApplyOpenSSHConfigExplainsUnreadableConfig(t *testing.T) {
	old := sshConfigGet
	sshConfigGet = func(_, _ string) (string, error) { return "", errors.New("ssh_config: bad line 3") }
	t.Cleanup(func() { sshConfigGet = old })

	_, err := applyOpenSSHConfig("web01.example.com", SSHOptions{UseAgent: true})
	if err == nil {
		t.Fatal("expected an error")
	}
	wantAll(t, "error", err.Error(), "bad line 3", "IdentityAgent", "web01.example.com", "--ssh-agent-socket")
}

// A host name from search is whatever the node reported about itself, and
// the host key type is whatever the server sent. Neither may reach a
// suggested command unquoted, or reach the terminal with escape sequences.
const hostileHost = "evil;touch /tmp/pwned\x1b[2J"

func TestSuggestedCommandsQuoteUntrustedHosts(t *testing.T) {
	opts := SSHOptions{User: "ubuntu", Port: 22, Timeout: time.Second}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	khPath := "/home/a b/.ssh/known_hosts"
	for name, e := range map[string]*Error{
		"unknown host key": handshakeError(hostileHost, opts, khPath, signer.PublicKey(), nil, &knownhosts.KeyError{}),
		"mismatch": handshakeError(hostileHost, opts, khPath, signer.PublicKey(), nil, &knownhosts.KeyError{
			Want: []knownhosts.KnownKey{{Key: signer.PublicKey(), Filename: khPath, Line: 3}},
		}),
		"auth":    handshakeError(hostileHost, opts, khPath, nil, []string{"the password from --ssh-password"}, errors.New("ssh: unable to authenticate, attempted methods [none password], no supported methods remain")),
		"timeout": connectError(hostileHost, opts, &net.OpError{Op: "dial", Err: timeoutErr{}}),
	} {
		t.Run(name, func(t *testing.T) {
			got := e.Error()
			if strings.ContainsAny(got, "\x1b\r") {
				t.Errorf("message carries a raw control character:\n%q", got)
			}
			for _, line := range strings.Split(got, "\n") {
				if strings.Contains(line, "evil;touch") && strings.HasPrefix(strings.TrimSpace(line), "ssh") && !strings.Contains(line, "'") {
					t.Errorf("suggested command leaves the host unquoted: %s", line)
				}
			}
		})
	}
	keyscan := handshakeError(hostileHost, opts, khPath, signer.PublicKey(), nil, &knownhosts.KeyError{}).Hint
	if !strings.Contains(keyscan, "ssh-keyscan 'evil;touch /tmp/pwned' >> '/home/a b/.ssh/known_hosts'") {
		t.Errorf("ssh-keyscan should quote the host and path, got:\n%s", keyscan)
	}
}

func TestIdentityFileCommandsQuoteThePath(t *testing.T) {
	path := "/home/a b/.ssh/id;rm"
	e := identityReadError(path, fs.ErrPermission)
	if !strings.Contains(e.Hint, "chmod 600 '/home/a b/.ssh/id;rm'") {
		t.Errorf("chmod should quote the path, got:\n%s", e.Hint)
	}
	e = identityParseError(path, []byte("x"), &ssh.PassphraseMissingError{})
	if !strings.Contains(e.Hint, "ssh-add '/home/a b/.ssh/id;rm'") {
		t.Errorf("ssh-add should quote the path, got:\n%s", e.Hint)
	}
}

func TestSanitizeText(t *testing.T) {
	got := SanitizeText("\x1b[31mred\x1b[0m\r\nnext\tline\x07\u202e")
	if got != "red\nnext\tline" {
		t.Errorf("SanitizeText = %q", got)
	}
	if got := SanitizeLine("a\nb\x1b]0;title\x07c"); got != "a bc" {
		t.Errorf("SanitizeLine = %q", got)
	}
}
