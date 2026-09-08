//go:build acceptance

package acceptance

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
	"golang.org/x/crypto/ssh"
)

// TestNodeListAgainstCincZero verifies that `cinc node list` against a
// real cinc-zero server returns the seeded nodes in both human and
// JSON output formats.
func TestNodeListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "node", "list", "--config", env.cfgPath)
	if human != "db01\nweb01\nweb02\n" {
		t.Errorf("node list (human) = %q, want sorted node names", human)
	}

	jsonOut := runCinc(t, env.binary, "node", "list", "--config", env.cfgPath, "--format", "json")
	for _, name := range []string{"db01", "web01", "web02"} {
		if !strings.Contains(jsonOut, name) {
			t.Errorf("node list (json) missing %q\ngot: %s", name, jsonOut)
		}
	}
}

// TestNodeShowAgainstCincZero fetches a seeded node and asserts on
// both the default (human summary) and `--format json` outputs.
func TestNodeShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "node", "show", "web01", "--config", env.cfgPath)
	// Piped (non-TTY) output is unstyled, so the name is the bare first line.
	if first, _, _ := strings.Cut(human, "\n"); first != "web01" {
		t.Errorf("node show (human) first line = %q, want the node name:\n%s", first, human)
	}
	for _, want := range []string{"Run List", "Environment", "_default"} {
		if !strings.Contains(human, want) {
			t.Errorf("node show (human) missing %q:\n%s", want, human)
		}
	}

	jsonOut := runCinc(t, env.binary, "node", "show", "web01", "--config", env.cfgPath, "--format", "json")
	var got cinc.Node
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("node show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "web01" {
		t.Errorf("node show returned name=%q, want web01", got.Name)
	}
}

// TestNodeDeleteAgainstCincZero deletes one of the seeded nodes and
// verifies a follow-up list no longer includes it.
func TestNodeDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "delete", "web02", "--config", env.cfgPath)
	if out != "Deleted node \"web02\"\n" {
		t.Errorf("node delete output = %q", out)
	}

	after := runCinc(t, env.binary, "node", "list", "--config", env.cfgPath)
	if after != "db01\nweb01\n" {
		t.Errorf("node list after delete = %q, want web02 absent", after)
	}
}

func TestNodeSSHSkipSearchAgainstSSHServer(t *testing.T) {
	server := startAcceptanceSSHServer(t, "hello from ssh\n")

	out := runCinc(t, buildCinc(t),
		"node", "ssh", "127.0.0.1", "echo hello",
		"--skip-search",
		"--ssh-user", "tester",
		"--ssh-password", "secret",
		"--ssh-port", fmt.Sprint(server.port),
		"--no-host-key-verify",
	)
	if out != "127.0.0.1\thello from ssh\n" {
		t.Fatalf("node ssh output = %q", out)
	}
	if got := server.lastCommand(); got != "echo hello" {
		t.Fatalf("ssh command = %q", got)
	}
}

func TestNodeBootstrapAgainstCincZeroAndSSHServer(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()
	server := startAcceptanceSSHServer(t, "bootstrap ok\n")

	out := runCinc(t, env.binary,
		"node", "bootstrap", "127.0.0.1",
		"--node-name", "boot01",
		"--ssh-user", "tester",
		"--ssh-password", "secret",
		"--ssh-port", fmt.Sprint(server.port),
		"--no-host-key-verify",
		"--config", env.cfgPath,
	)
	if out != "Bootstrapped node \"boot01\" on 127.0.0.1\n" {
		t.Fatalf("node bootstrap output = %q", out)
	}
	command := server.lastCommand()
	for _, want := range []string{
		"curl -L 'https://omnitruck.cinc.sh/install.sh'",
		"node_name 'boot01'",
		"cinc-client -j /etc/cinc/first-boot.json",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("bootstrap command missing %q:\n%s", want, command)
		}
	}
}

func TestNodeBootstrapPolicyOmitsEnvironment(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()
	server := startAcceptanceSSHServer(t, "bootstrap ok\n")

	runCinc(t, env.binary,
		"node", "bootstrap", "127.0.0.1",
		"--node-name", "boot02",
		"--ssh-user", "tester",
		"--ssh-password", "secret",
		"--ssh-port", fmt.Sprint(server.port),
		"--no-host-key-verify",
		"--policy-name", "appserver",
		"--policy-group", "prod",
		"--config", env.cfgPath,
	)
	command := server.lastCommand()
	for _, want := range []string{`"policy_name": "appserver"`, `"policy_group": "prod"`} {
		if !strings.Contains(command, want) {
			t.Fatalf("policy bootstrap command missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, "chef_environment") {
		t.Fatalf("policy bootstrap must not set chef_environment:\n%s", command)
	}
}

type acceptanceSSHServer struct {
	port    int
	output  string
	mu      sync.Mutex
	command string
}

func (s *acceptanceSSHServer) lastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.command
}

func startAcceptanceSSHServer(t *testing.T, output string) *acceptanceSSHServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &acceptanceSSHServer{
		port:   listener.Addr().(*net.TCPAddr).Port,
		output: output,
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.handleConn(conn, config)
		}
	}()
	return server
}

func (s *acceptanceSSHServer) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, requests)
	}
}

func (s *acceptanceSSHServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		command := parseExecCommand(req.Payload)
		s.mu.Lock()
		s.command = command
		s.mu.Unlock()
		_ = req.Reply(true, nil)
		_, _ = io.WriteString(channel, s.output)
		_, _ = channel.SendRequest("exit-status", false, exitStatusPayload(0))
		return
	}
}

func parseExecCommand(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}

func exitStatusPayload(status uint32) []byte {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], status)
	return payload[:]
}

// TestNodeCreateAgainstCincZero creates a node with an environment and run
// list, then confirms it is persisted.
func TestNodeCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "create", "app01",
		"--environment", "_default",
		"--run-list", "recipe[base],recipe[app]",
		"--config", env.cfgPath)
	if out != "Created node \"app01\"\n" {
		t.Errorf("node create output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "node", "show", "app01", "--config", env.cfgPath, "--format", "json")
	var got cinc.Node
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("node show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "app01" || got.Environment != "_default" {
		t.Errorf("created node = %+v, want name=app01 environment=_default", got)
	}
	if !contains(got.RunList, "recipe[base]") || !contains(got.RunList, "recipe[app]") {
		t.Errorf("created node run_list = %v, want both recipes", got.RunList)
	}
}

func TestNodeCreateWithPolicyAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "node", "create", "policynode",
		"--policy-group", "prod", "--policy-name", "appserver",
		"--config", env.cfgPath)
	if out != "Created node \"policynode\"\n" {
		t.Errorf("node create output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "node", "show", "policynode", "--config", env.cfgPath, "--format", "json")
	var got cinc.Node
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("node show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.PolicyName != "appserver" || got.PolicyGroup != "prod" {
		t.Errorf("created policy node = %q/%q, want appserver/prod", got.PolicyName, got.PolicyGroup)
	}
}

// TestNodeEditAgainstCincZero edits a seeded node through --file (moving it to
// the seeded "staging" environment) and confirms the change is persisted.
func TestNodeEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.Node{
		Name:        "ignored",
		Environment: "staging",
		RunList:     []string{"recipe[base]"},
	})
	out := runCinc(t, env.binary, "node", "edit", "web01", "--file", file, "--config", env.cfgPath)
	if out != "Updated node \"web01\"\n" {
		t.Errorf("node edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "node", "show", "web01", "--config", env.cfgPath, "--format", "json")
	var got cinc.Node
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("node show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "web01" || got.Environment != "staging" {
		t.Errorf("edited node = %+v, want name=web01 environment=staging", got)
	}
}
