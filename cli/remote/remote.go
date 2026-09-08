// Package remote implements SSH-based remote command execution for node
// workflows.
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sshconfig "github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

var sshConfigGet = sshconfig.GetStrict

// Target identifies one remote host.
type Target struct {
	Host string `json:"host"`
}

// SSHOptions configures native SSH connections.
type SSHOptions struct {
	User         string
	Password     string
	IdentityFile string
	AgentSocket  string
	Port         int
	Timeout      time.Duration
	UseAgent     bool
	VerifyHost   bool
}

// CommandResult is the result of one command on one host.
type CommandResult struct {
	Host     string `json:"host"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// Runner executes a command on a target.
type Runner interface {
	Run(ctx context.Context, target Target, command string, opts SSHOptions) CommandResult
}

// NativeRunner is a Runner backed by golang.org/x/crypto/ssh.
type NativeRunner struct{}

// Run executes command on target through SSH.
func (NativeRunner) Run(ctx context.Context, target Target, command string, opts SSHOptions) CommandResult {
	result := CommandResult{Host: target.Host}
	var err error
	opts, err = applyOpenSSHConfig(target.Host, opts)
	if err != nil {
		result.ExitCode = 255
		result.Error = err.Error()
		return result
	}
	config, err := clientConfig(opts)
	if err != nil {
		result.ExitCode = 255
		result.Error = err.Error()
		return result
	}
	host := net.JoinHostPort(target.Host, strconv.Itoa(opts.Port))
	dialer := net.Dialer{Timeout: opts.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		result.ExitCode = 255
		result.Error = fmt.Sprintf("dial ssh: %v", err)
		return result
	}
	defer func() { _ = conn.Close() }()
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, host, config)
	if err != nil {
		result.ExitCode = 255
		result.Error = fmt.Sprintf("ssh handshake: %v", err)
		return result
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer func() { _ = client.Close() }()
	session, err := client.NewSession()
	if err != nil {
		result.ExitCode = 255
		result.Error = fmt.Sprintf("new ssh session: %v", err)
		return result
	}
	defer func() { _ = session.Close() }()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	err = session.Run(command)
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if err == nil {
		return result
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 255
	result.Error = err.Error()
	return result
}

func applyOpenSSHConfig(host string, opts SSHOptions) (SSHOptions, error) {
	if !opts.UseAgent || opts.AgentSocket != "" {
		return opts, nil
	}
	identityAgent, err := sshConfigGet(host, "IdentityAgent")
	if err != nil {
		return opts, fmt.Errorf("read ssh config IdentityAgent: %w", err)
	}
	if strings.EqualFold(identityAgent, "none") {
		opts.UseAgent = false
		return opts, nil
	}
	if identityAgent != "" {
		opts.AgentSocket = identityAgent
	}
	return opts, nil
}

// RunMany executes command across targets, limiting concurrency and preserving
// the input order in the returned results.
func RunMany(ctx context.Context, runner Runner, targets []Target, command string, opts SSHOptions, concurrency int, exitOnError bool) []CommandResult {
	if concurrency < 1 {
		concurrency = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]CommandResult, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					results[idx] = CommandResult{Host: targets[idx].Host, ExitCode: 255, Error: ctx.Err().Error()}
					continue
				}
				result := runner.Run(ctx, targets[idx], command, opts)
				results[idx] = result
				if exitOnError && result.ExitCode != 0 {
					cancel()
				}
			}
		}()
	}
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func clientConfig(opts SSHOptions) (*ssh.ClientConfig, error) {
	if opts.User == "" {
		return nil, fmt.Errorf("ssh user is required")
	}
	if opts.Port == 0 {
		opts.Port = 22
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	auth, err := authMethods(opts)
	if err != nil {
		return nil, err
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("no SSH authentication method configured")
	}
	hostKeyCallback, err := hostKeyCallback(opts.VerifyHost)
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            opts.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         opts.Timeout,
	}, nil
}

func authMethods(opts SSHOptions) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if opts.IdentityFile != "" {
		key, err := os.ReadFile(expandHome(opts.IdentityFile))
		if err != nil {
			return nil, fmt.Errorf("read ssh identity file: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse ssh identity file: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if opts.UseAgent {
		for _, sock := range agentSocketCandidates(opts.AgentSocket) {
			conn, err := net.Dial("unix", sock)
			if err == nil {
				methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
			}
		}
	}
	if opts.Password != "" {
		methods = append(methods, ssh.Password(opts.Password), ssh.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = opts.Password
			}
			return answers, nil
		}))
	}
	return methods, nil
}

func agentSocketCandidates(explicit string) []string {
	var paths []string
	seen := map[string]struct{}{}
	add := func(path string) {
		path = expandHome(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	add(explicit)
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".1password", "agent.sock"))
		add(filepath.Join(home, "Library", "Group Containers", "2BUA8C4S2C.com.1password", "t", "agent.sock"))
	}
	add(os.Getenv("SSH_AUTH_SOCK"))
	return paths
}

func hostKeyCallback(verify bool) (ssh.HostKeyCallback, error) {
	if !verify {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	callback, err := knownhosts.New(filepath.Join(home, ".ssh", "known_hosts"))
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return callback, nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
