package suite

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshServer is an in-process SSH server that accepts user "tester" with
// password "secret", records each exec request's command, and answers with a
// fixed output and exit status 0. It stands in for a node, so node ssh and
// node bootstrap run end to end without a real host.
type sshServer struct {
	port   int
	output string

	mu       sync.Mutex
	commands []string
}

// sshArgs are the flags that point a node ssh or bootstrap at the server.
func (s *sshServer) sshArgs() []string {
	return []string{
		"--ssh-user", "tester",
		"--ssh-password", "secret",
		"--ssh-port", fmt.Sprint(s.port),
		"--no-host-key-verify",
		"--ssh-agent=false",
	}
}

// received returns every command executed so far.
func (s *sshServer) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func startSSHServer(t *testing.T, output string) *sshServer {
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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshServer{port: l.Addr().(*net.TCPAddr).Port, output: output}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.handleConn(conn, config)
		}
	}()
	return s
}

func (s *sshServer) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer func() { _ = sc.Close() }()
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

func (s *sshServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = channel.Close() }()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		s.mu.Lock()
		s.commands = append(s.commands, execCommand(req.Payload))
		s.mu.Unlock()
		_ = req.Reply(true, nil)
		_, _ = io.WriteString(channel, s.output)
		var status [4]byte // exit status 0
		_, _ = channel.SendRequest("exit-status", false, status[:])
		return
	}
}

// execCommand decodes an exec request's payload: a uint32 length, then the
// command.
func execCommand(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}
