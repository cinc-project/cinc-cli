package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// Reasons a host failed before its command could run. A caller running one
// command across many hosts uses them to group hosts that failed the same
// way and explain each problem once instead of once per host.
const (
	ReasonConfig          = "config"
	ReasonDNS             = "dns"
	ReasonRefused         = "connection_refused"
	ReasonTimeout         = "timeout"
	ReasonConnect         = "connect"
	ReasonUnknownHostKey  = "unknown_host_key"
	ReasonHostKeyMismatch = "host_key_mismatch"
	ReasonHostKeyRevoked  = "host_key_revoked"
	ReasonAuth            = "auth"
	ReasonHandshake       = "handshake"
	ReasonSession         = "session"
)

// Error is an SSH failure explained for someone who may never have used SSH
// much. Summary is one line naming what went wrong; Hint says why it happens
// and what to do about it. Error() joins the two. Err is the underlying
// error, so errors.Is and errors.As still see through it.
type Error struct {
	Reason  string
	Summary string
	Hint    string
	Err     error
}

func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Summary
	}
	return e.Summary + "\n\n" + e.Hint
}

func (e *Error) Unwrap() error { return e.Err }

// sshCommand is the plain ssh invocation that reaches host as user, for a
// user to try by hand. The port is left out when it is the default.
func sshCommand(user, host string, port int) string {
	if port == 0 || port == 22 {
		return "ssh " + QuoteArg(user+"@"+host)
	}
	return fmt.Sprintf("ssh -p %d %s", port, QuoteArg(user+"@"+host))
}

func portFlag(port int) string {
	if port == 0 || port == 22 {
		return ""
	}
	return fmt.Sprintf("-p %d ", port)
}

// knownHostsName is how known_hosts and ssh-keygen -R spell host:port.
func knownHostsName(host string, port int) string {
	if port == 0 || port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

func missingUserError() *Error {
	return &Error{
		Reason:  ReasonConfig,
		Summary: "we need a user name to log in with over SSH",
		Hint:    "Pass the account to log in as with --ssh-user, such as --ssh-user ubuntu.",
	}
}

// noAuthMethodError explains that none of the three ways to log in is
// available, and what state the agent, the default, was in.
func noAuthMethodError(opts SSHOptions) *Error {
	var agent string
	switch {
	case !opts.UseAgent:
		agent = "The agent is turned off for this run, either by --ssh-agent=false or by IdentityAgent none in your SSH config."
	case opts.AgentSocket == "" && os.Getenv("SSH_AUTH_SOCK") == "":
		agent = "We couldn't find one running: SSH_AUTH_SOCK isn't set. Start one with `eval \"$(ssh-agent)\"` and add your key with `ssh-add`."
	default:
		sock := opts.AgentSocket
		if sock == "" {
			sock = os.Getenv("SSH_AUTH_SOCK")
		}
		agent = fmt.Sprintf("We couldn't reach one at %s. Check that it's running and holds your key (`ssh-add -l` lists them).", expandHome(sock))
	}
	return &Error{
		Reason:  ReasonConfig,
		Summary: fmt.Sprintf("we have no way to log in as %q over SSH", opts.User),
		Hint: fmt.Sprintf(`%s can log in with any of these:
  - an SSH agent holding your key. %s
  - a private key file: --ssh-identity-file ~/.ssh/id_ed25519
  - a password: --ssh-password`, progname.Get(), agent),
	}
}

// identityReadError explains an --ssh-identity-file that can't be opened.
func identityReadError(path string, err error) *Error {
	e := &Error{Reason: ReasonConfig, Err: err}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		e.Summary = fmt.Sprintf("can't find the SSH key file %s", path)
		e.Hint = "That's the path given to --ssh-identity-file. Check it for typos. SSH keys usually live in ~/.ssh, with names like id_ed25519 or id_rsa."
	case errors.Is(err, fs.ErrPermission):
		e.Summary = fmt.Sprintf("can't read the SSH key file %s: permission denied", path)
		e.Hint = fmt.Sprintf("Your account isn't allowed to open it. Make it readable by you and nobody else:\n  chmod 600 %s", path)
	default:
		e.Summary = fmt.Sprintf("can't read the SSH key file %s: %v", path, err)
	}
	return e
}

// identityParseError explains an --ssh-identity-file that was read but isn't
// a private key we can use as is.
func identityParseError(path string, data []byte, err error) *Error {
	e := &Error{Reason: ReasonConfig, Err: err}
	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); ok {
		e.Summary = fmt.Sprintf("the SSH key %s is protected by a passphrase, and we can't ask for it here", path)
		e.Hint = fmt.Sprintf(`Load the key into your ssh-agent instead. The agent asks for the passphrase once, then signs logins for %s:
  ssh-add %s
Then run the command again without --ssh-identity-file; the agent is used by default.`, progname.Get(), path)
		return e
	}
	if _, _, _, _, perr := ssh.ParseAuthorizedKey(data); perr == nil {
		private := strings.TrimSuffix(path, ".pub")
		e.Summary = fmt.Sprintf("%s is a public key, but --ssh-identity-file needs the private key", path)
		e.Hint = "SSH keys come in pairs: the public half goes on the servers you log in to, and the private half stays with you."
		if private != path {
			e.Hint += fmt.Sprintf(" The private half is usually the same file without .pub:\n  --ssh-identity-file %s", private)
		}
		return e
	}
	e.Summary = fmt.Sprintf("%s isn't an SSH private key we can read: %v", path, err)
	e.Hint = "--ssh-identity-file needs a private key in OpenSSH or PEM format, a file that starts with a line like -----BEGIN OPENSSH PRIVATE KEY-----. A PuTTY .ppk key needs converting first:\n  puttygen key.ppk -O private-openssh -o key"
	return e
}

const noVerifyHint = "On a network you trust completely, such as a lab or a throwaway VM, --no-host-key-verify skips the check."

func homeDirError(err error) *Error {
	return &Error{
		Reason:  ReasonConfig,
		Summary: "can't find your home directory, which is where your known_hosts file lives",
		Hint:    "Set HOME and try again. " + noVerifyHint,
		Err:     err,
	}
}

// knownHostsLoadError explains a known_hosts file that is missing or that
// knownhosts can't parse.
func knownHostsLoadError(path string, err error) *Error {
	if errors.Is(err, fs.ErrNotExist) {
		return &Error{
			Reason:  ReasonConfig,
			Summary: fmt.Sprintf("can't check host keys: there's no known_hosts file at %s", path),
			Hint: fmt.Sprintf(`Every SSH server has a host key that proves which machine it is. Before logging in, %s compares that key with the one in your known_hosts file, the list of servers you've connected to before, so a machine pretending to be one of yours can't collect your login. That file doesn't exist yet.

It's created the first time you connect to a host with ssh and accept its key. Or add a host's key without logging in:
  ssh-keyscan <host> >> %s
%s`, progname.Get(), path, noVerifyHint),
			Err: err,
		}
	}
	return &Error{
		Reason:  ReasonConfig,
		Summary: fmt.Sprintf("can't read your known_hosts file: %v", err),
		Hint:    fmt.Sprintf("Fix or delete the line it names in %s. %s", path, noVerifyHint),
		Err:     err,
	}
}

func sshConfigError(host string, err error) *Error {
	return &Error{
		Reason:  ReasonConfig,
		Summary: fmt.Sprintf("can't read your SSH config: %v", err),
		Hint: fmt.Sprintf("We read it (~/.ssh/config, then /etc/ssh/ssh_config) to find the IdentityAgent setting for %s, which says which ssh-agent to use. Fix the line it complains about, or skip the lookup by naming the agent yourself with --ssh-agent-socket.",
			host),
		Err: err,
	}
}

// connectError explains a failed TCP connection to host:port.
func connectError(host string, opts SSHOptions, err error) *Error {
	port, timeout := opts.Port, opts.Timeout
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
		return &Error{
			Reason:  ReasonDNS,
			Summary: fmt.Sprintf("can't find a host named %s", host),
			Hint: fmt.Sprintf("Your computer couldn't look up an IP address for %s, so there was nothing to connect to. Check the spelling. If the name came from a node search, it's the node's fqdn (or the attribute --attribute names), which may only resolve inside the node's own network; --attribute ipaddress connects by IP address instead.",
				host),
			Err: err,
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return &Error{
			Reason:  ReasonRefused,
			Summary: fmt.Sprintf("%s refused the SSH connection on port %d", host, port),
			Hint: fmt.Sprintf("The machine is up, but nothing is accepting SSH connections on port %d. Check that the SSH server (sshd) is running there, and that it listens on port %d; if it uses another port, pass it with --ssh-port.",
				port, port),
			Err: err,
		}
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return &Error{
			Reason:  ReasonTimeout,
			Summary: fmt.Sprintf("timed out after %s connecting to %s on port %d", timeout, host, port),
			Hint: fmt.Sprintf(`%s didn't answer at all. It may be powered off, a firewall may be dropping SSH traffic, or the address may not be reachable from this machine (a private IP on another network, for example). Check that you can reach it by hand:
  %s
If the network is just slow, allow more time with --ssh-timeout 2m.`, host, sshCommand(opts.User, host, port)),
			Err: err,
		}
	}
	return &Error{
		Reason:  ReasonConnect,
		Summary: fmt.Sprintf("can't connect to %s on port %d: %v", host, port, err),
		Err:     err,
	}
}

var attemptedMethods = regexp.MustCompile(`attempted methods \[([^\]]*)\]`)

// handshakeError explains a failed SSH handshake: an unknown or changed host
// key, a rejected login, or a port that doesn't speak SSH. presented is the
// host key the server offered, if it got that far; offered describes the
// login methods we tried, for a rejected login.
func handshakeError(host string, opts SSHOptions, knownHostsPath string, presented ssh.PublicKey, offered []string, err error) *Error {
	fingerprint := "(unknown)"
	keyType := ""
	if presented != nil {
		fingerprint = ssh.FingerprintSHA256(presented)
		keyType = presented.Type()
	}
	if keyErr, ok := errors.AsType[*knownhosts.KeyError](err); ok {
		if len(keyErr.Want) == 0 {
			return &Error{
				Reason:  ReasonUnknownHostKey,
				Summary: fmt.Sprintf("%s isn't in your known_hosts file, so we can't confirm it's the right machine", host),
				Hint: fmt.Sprintf(`Every SSH server has a host key that proves which machine it is. Before logging in, %s checks that key against %s, the list of servers you've connected to before, so a machine pretending to be %s can't collect your login. This host isn't listed yet. It offered the %s key %s.

To trust it, connect once with ssh, check that the fingerprint it shows matches the one above, and answer yes:
  %s
Or add its key without logging in:
  ssh-keyscan %s%s >> %s
%s`, progname.Get(), knownHostsPath, host, keyType, fingerprint,
					sshCommand(opts.User, host, opts.Port),
					portFlag(opts.Port), host, knownHostsPath,
					noVerifyHint),
				Err: err,
			}
		}
		return hostKeyMismatchError(host, opts, presented, fingerprint, keyErr, err)
	}
	if revoked, ok := errors.AsType[*knownhosts.RevokedError](err); ok {
		return &Error{
			Reason:  ReasonHostKeyRevoked,
			Summary: fmt.Sprintf("%s offered a host key that's marked as revoked in %s line %d", host, revoked.Revoked.Filename, revoked.Revoked.Line),
			Hint: fmt.Sprintf("A revoked key must not be trusted any more, usually because it was stolen or retired. We stopped before sending your login. Ask whoever runs %s for its current host key.",
				host),
			Err: err,
		}
	}
	if m := attemptedMethods.FindStringSubmatch(err.Error()); strings.Contains(err.Error(), "unable to authenticate") {
		tried, rejected := "nothing we could offer", "turned that down"
		switch {
		case len(offered) == 1:
			tried, rejected = offered[0], "turned it down"
		case len(offered) > 1:
			tried, rejected = JoinWords(offered), "turned them all down"
		case m != nil:
			tried, rejected = "the methods "+m[1], "turned them all down"
		}
		return &Error{
			Reason:  ReasonAuth,
			Summary: fmt.Sprintf("%s didn't accept the login for user %q", host, opts.User),
			Hint: fmt.Sprintf(`We tried %s, and the server %s. Check that:
  - %q is the right account on that machine. Cloud images often use ubuntu, ec2-user, or admin; pass another with --ssh-user.
  - the account's ~/.ssh/authorized_keys on the host holds the public half of your key, or that --ssh-password is right.
  - you can log in by hand:
      %s`, tried, rejected, opts.User, sshCommand(opts.User, host, opts.Port)),
			Err: err,
		}
	}
	return &Error{
		Reason:  ReasonHandshake,
		Summary: fmt.Sprintf("connected to %s on port %d, but the SSH handshake failed: %v", host, opts.Port, err),
		Hint: fmt.Sprintf("Check that port %d on %s is an SSH server and not some other service. A server that closes connections straight away may also be refusing new ones because too many arrived at once; connecting to fewer hosts at a time helps.",
			opts.Port, host),
		Err: err,
	}
}

func hostKeyMismatchError(host string, opts SSHOptions, presented ssh.PublicKey, fingerprint string, keyErr *knownhosts.KeyError, err error) *Error {
	want := keyErr.Want[0]
	sameType := presented == nil
	for _, k := range keyErr.Want {
		if presented != nil && k.Key.Type() == presented.Type() {
			want, sameType = k, true
			break
		}
	}
	name := knownHostsName(host, opts.Port)
	if !sameType {
		return &Error{
			Reason:  ReasonHostKeyMismatch,
			Summary: fmt.Sprintf("%s's SSH host key doesn't match the one in your known_hosts file", host),
			Hint: fmt.Sprintf(`%s line %d has a %s key for %s, but the server offered a %s key (%s). That's usually harmless: servers have several keys of different types, and ssh recorded a different one than we were offered. To record this one too, check that the fingerprint above is right, then run:
  ssh-keyscan -t %s %s%s >> %s`, want.Filename, want.Line, want.Key.Type(), host, presented.Type(), fingerprint,
				presented.Type(), portFlag(opts.Port), host, want.Filename),
			Err: err,
		}
	}
	return &Error{
		Reason:  ReasonHostKeyMismatch,
		Summary: fmt.Sprintf("%s's SSH host key doesn't match the one in your known_hosts file", host),
		Hint: fmt.Sprintf(`%s offered the host key %s, but %s line %d says it should be %s. Either the machine was rebuilt or reinstalled and made a new key, or something between you and it is pretending to be it to collect your login (a man-in-the-middle attack). We stopped before sending your login.

If you know %s was rebuilt, remove the old key and accept the new one:
  ssh-keygen -R %s
  %s
If you don't know why the key changed, don't connect; check with whoever runs the machine first.`,
			host, fingerprint, want.Filename, want.Line, ssh.FingerprintSHA256(want.Key),
			host, QuoteArg(name), sshCommand(opts.User, host, opts.Port)),
		Err: err,
	}
}

// JoinWords joins items as English prose: "a", "a and b", "a, b, and c".
func JoinWords(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
}

// QuoteArg quotes s for a POSIX shell, leaving it bare when it is made only
// of characters a shell treats literally, so suggested commands stay
// readable: web01.example.com and role:web need no quotes.
func QuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@%+=,", r)) {
			return shellQuote(s)
		}
	}
	return s
}
