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
	"sync/atomic"
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
	// Skipped marks a host the command was never attempted on, because
	// --exit-on-error stopped the run first. It is not a failure: the host
	// was not asked to do anything, so nothing is known about it.
	Skipped bool `json:"skipped,omitempty"`
	// Reason classifies a failure that happened before the command could
	// run, such as an unknown host key or a refused connection; it is one of
	// the Reason constants. It is empty when the command itself ran.
	Reason string `json:"reason,omitempty"`
	// Hint explains Reason: why it happens and what to do about it. Error
	// holds the one-line summary.
	Hint string `json:"hint,omitempty"`
}

// Runner executes a command on a target.
type Runner interface {
	Run(ctx context.Context, target Target, command string, opts SSHOptions) CommandResult
}

// NativeRunner is a Runner backed by golang.org/x/crypto/ssh.
type NativeRunner struct{}

// Run executes command on target through SSH. A failure before the command
// could run comes back with Reason and Hint set, explaining it.
func (NativeRunner) Run(ctx context.Context, target Target, command string, opts SSHOptions) CommandResult {
	result := CommandResult{Host: target.Host}
	var err error
	opts, err = applyOpenSSHConfig(target.Host, opts)
	if err != nil {
		return failed(result, err)
	}
	config, closeAgent, err := clientConfig(opts)
	if err != nil {
		return failed(result, err)
	}
	// Held open for the duration of the session: the agent signs during the
	// handshake, and releasing it here keeps a fleet-wide run from exhausting
	// the process file descriptor limit one host at a time.
	defer closeAgent()
	// Remember the key the server offers, so a host key error can show its
	// fingerprint for the user to check.
	var presented ssh.PublicKey
	verify := config.HostKeyCallback
	config.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented = key
		return verify(hostname, remote, key)
	}
	host := net.JoinHostPort(target.Host, strconv.Itoa(config.port))
	dialer := net.Dialer{Timeout: config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return failed(result, connectError(target.Host, opts.withDefaults(), err))
	}
	defer func() { _ = conn.Close() }()
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, host, config.ClientConfig)
	if err != nil {
		return failed(result, handshakeError(target.Host, opts.withDefaults(), config.knownHosts, presented, config.offered, err))
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer func() { _ = client.Close() }()
	session, err := client.NewSession()
	if err != nil {
		return failed(result, &Error{
			Reason:  ReasonSession,
			Summary: fmt.Sprintf("logged in to %s, but couldn't start a session to run the command: %v", target.Host, err),
			Err:     err,
		})
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
	if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok {
		result.ExitCode = exitErr.ExitStatus()
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 255
	result.Error = err.Error()
	return result
}

// failed records err on result as a failure before the command ran.
func failed(result CommandResult, err error) CommandResult {
	result.ExitCode = 255
	if e, ok := errors.AsType[*Error](err); ok {
		result.Reason, result.Error, result.Hint = e.Reason, SanitizeText(e.Summary), SanitizeText(e.Hint)
		return result
	}
	result.Error = SanitizeText(err.Error())
	return result
}

// withDefaults fills in the port and timeout used when none is set.
func (o SSHOptions) withDefaults() SSHOptions {
	if o.Port == 0 {
		o.Port = 22
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	return o
}

// CheckOptions checks everything about opts that doesn't depend on which
// host is being reached: a user is set, the identity file is a usable
// private key, and known_hosts can be read when host keys are verified.
// Callers run it before doing anything with a side effect, so a typo in
// --ssh-identity-file doesn't surface only after work has started, and
// doesn't repeat once per host.
func CheckOptions(opts SSHOptions) error {
	if opts.User == "" {
		return missingUserError()
	}
	if opts.IdentityFile != "" {
		if _, err := loadSigner(opts.IdentityFile); err != nil {
			return err
		}
	}
	if _, _, err := hostKeyCallback(opts.VerifyHost); err != nil {
		return err
	}
	return nil
}

func applyOpenSSHConfig(host string, opts SSHOptions) (SSHOptions, error) {
	if !opts.UseAgent || opts.AgentSocket != "" {
		return opts, nil
	}
	identityAgent, err := sshConfigGet(host, "IdentityAgent")
	if err != nil {
		return opts, sshConfigError(host, err)
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
//
// With exitOnError, the first non-zero exit stops further launches. Commands
// already running are left to finish, which is what the flag promises, and
// every host that never ran comes back marked Skipped rather than failed.
func RunMany(ctx context.Context, runner Runner, targets []Target, command string, opts SSHOptions, concurrency int, exitOnError bool) []CommandResult {
	if concurrency < 1 {
		concurrency = 1
	}
	results := make([]CommandResult, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	// A plain flag rather than a cancelled context: cancelling would also
	// abort sessions that had already started, which is not what
	// "stop launching new SSH sessions" means.
	var stopLaunching atomic.Bool
	for i := 0; i < concurrency; i++ {
		wg.Go(func() {
			for idx := range jobs {
				if stopLaunching.Load() || ctx.Err() != nil {
					results[idx] = CommandResult{Host: targets[idx].Host, Skipped: true}
					continue
				}
				result := runner.Run(ctx, targets[idx], command, opts)
				results[idx] = result
				if exitOnError && result.ExitCode != 0 {
					stopLaunching.Store(true)
				}
			}
		})
	}
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// sshClientConfig is an ssh.ClientConfig plus what an error message needs
// to explain a failed connection.
type sshClientConfig struct {
	*ssh.ClientConfig
	port int
	// knownHosts is the known_hosts file host keys are checked against.
	knownHosts string
	// offered describes, in words, each login method we will try.
	offered []string
}

// clientConfig assembles the SSH client configuration. The returned cleanup
// releases the agent connection the auth methods hold; it is always non-nil
// and safe to defer.
func clientConfig(opts SSHOptions) (*sshClientConfig, func(), error) {
	noop := func() {}
	if opts.User == "" {
		return nil, noop, missingUserError()
	}
	opts = opts.withDefaults()
	auth, offered, cleanup, err := authMethods(opts)
	if err != nil {
		return nil, noop, err
	}
	if len(auth) == 0 {
		cleanup()
		return nil, noop, noAuthMethodError(opts)
	}
	hostKeyCallback, knownHosts, err := hostKeyCallback(opts.VerifyHost)
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	return &sshClientConfig{
		ClientConfig: &ssh.ClientConfig{
			User:            opts.User,
			Auth:            auth,
			HostKeyCallback: hostKeyCallback,
			Timeout:         opts.Timeout,
		},
		port:       opts.Port,
		knownHosts: knownHosts,
		offered:    offered,
	}, cleanup, nil
}

// loadSigner reads and parses a private key file.
func loadSigner(path string) (ssh.Signer, error) {
	path = expandHome(path)
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, identityReadError(path, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, identityParseError(path, key, err)
	}
	return signer, nil
}

// authMethods builds the authentication methods for one connection, and a
// description of each for error messages. The agent method holds an open
// unix socket for as long as authentication may need it, so the returned
// cleanup closes it; it is always non-nil and safe to defer.
func authMethods(opts SSHOptions) ([]ssh.AuthMethod, []string, func(), error) {
	var (
		methods []ssh.AuthMethod
		offered []string
		conns   []net.Conn
	)
	cleanup := func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}
	if opts.IdentityFile != "" {
		signer, err := loadSigner(opts.IdentityFile)
		if err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
		offered = append(offered, "the key in "+expandHome(opts.IdentityFile))
	}
	if opts.UseAgent {
		// Only the first agent that answers is used. Every key an agent holds
		// costs one authentication attempt, so offering several agents can
		// exhaust sshd's MaxAuthTries (6 by default) before the remaining
		// methods are ever tried.
		for _, sock := range agentSocketCandidates(opts.AgentSocket) {
			conn, err := net.Dial("unix", sock)
			if err != nil {
				continue
			}
			conns = append(conns, conn)
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
			offered = append(offered, "the keys in your ssh-agent ("+sock+")")
			break
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
		offered = append(offered, "the password from --ssh-password")
	}
	return methods, offered, cleanup, nil
}

// agentSocketCandidates lists agent sockets in the order they should be
// tried. Only the first one that answers is used, so the order decides which
// agent signs: the explicit --ssh-agent-socket first, then the agent the
// environment names, and finally the well-known 1Password paths as a fallback
// for when 1Password is running but has not exported SSH_AUTH_SOCK.
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
	add(os.Getenv("SSH_AUTH_SOCK"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".1password", "agent.sock"))
		add(filepath.Join(home, "Library", "Group Containers", "2BUA8C4S2C.com.1password", "t", "agent.sock"))
	}
	return paths
}

// hostKeyCallback checks host keys against ~/.ssh/known_hosts, or accepts
// any key when verify is off. It also returns the known_hosts path, for
// error messages.
func hostKeyCallback(verify bool) (ssh.HostKeyCallback, string, error) {
	if !verify {
		return ssh.InsecureIgnoreHostKey(), "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", homeDirError(err)
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, "", knownHostsLoadError(path, err)
	}
	return callback, path, nil
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
