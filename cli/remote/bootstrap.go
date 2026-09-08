package remote

import (
	"encoding/json"
	"fmt"
	"strings"
)

const DefaultBootstrapURL = "https://omnitruck.cinc.sh/install.sh"

// BootstrapOptions describes the remote Cinc Client bootstrap script.
type BootstrapOptions struct {
	NodeName         string
	ServerURL        string
	ClientKeyPEM     string
	RunList          []string
	Environment      string
	PolicyName       string
	PolicyGroup      string
	Sudo             bool
	BootstrapURL     string
	BootstrapVersion string
}

// BootstrapCommand builds the shell command executed on the target.
func BootstrapCommand(opts BootstrapOptions) (string, error) {
	if opts.NodeName == "" {
		return "", fmt.Errorf("node name is required")
	}
	if opts.ServerURL == "" {
		return "", fmt.Errorf("server URL is required")
	}
	if opts.ClientKeyPEM == "" {
		return "", fmt.Errorf("client key is required")
	}
	// rubyQuote keeps these out of Ruby's reach, but the client.rb it builds
	// is delivered inside a shell heredoc one layer down. A newline can close
	// that heredoc early and turn the remainder into commands run through
	// sudo on the target, so reject it here rather than try to escape it.
	for _, field := range []struct{ name, value string }{
		{"node name", opts.NodeName},
		{"server URL", opts.ServerURL},
	} {
		if strings.ContainsAny(field.value, "\n\r") {
			return "", fmt.Errorf("refusing a %s containing a newline: %q", field.name, field.value)
		}
	}
	if opts.BootstrapURL == "" {
		opts.BootstrapURL = DefaultBootstrapURL
	}
	if opts.Environment == "" {
		opts.Environment = "_default"
	}
	firstBoot, err := firstBootJSON(opts)
	if err != nil {
		return "", err
	}
	clientRB := clientRB(opts)
	prefix := ""
	if opts.Sudo {
		prefix = "sudo "
	}
	installArgs := ""
	if opts.BootstrapVersion != "" {
		installArgs = " -v " + shellQuote(opts.BootstrapVersion)
	}
	writeKey, err := writeFileCommand(prefix, "/etc/cinc/client.pem", opts.ClientKeyPEM)
	if err != nil {
		return "", err
	}
	writeClientRB, err := writeFileCommand(prefix, "/etc/cinc/client.rb", clientRB)
	if err != nil {
		return "", err
	}
	writeFirstBoot, err := writeFileCommand(prefix, "/etc/cinc/first-boot.json", string(firstBoot))
	if err != nil {
		return "", err
	}
	commands := []string{
		"set -e",
		// The installer is downloaded to a file and then run, rather than
		// piped into a shell. A pipeline reports only the exit status of its
		// last command, so `curl ... | bash` hides a failed download from
		// `set -e`: the script would carry on and run a cinc-client that was
		// never installed. curl -f turns an HTTP error into a failure instead
		// of saving the error page as the "installer".
		//
		// Trust assumption is unchanged: the target executes whatever
		// opts.BootstrapURL serves (HTTPS omnitruck by default), so it trusts
		// that host and its TLS chain, as with any Chef/Cinc bootstrap.
		`CINC_INSTALLER="$(mktemp)"`,
		`trap 'rm -f "$CINC_INSTALLER"' EXIT`,
		"curl -fsSL " + shellQuote(opts.BootstrapURL) + ` -o "$CINC_INSTALLER"`,
		prefix + `bash "$CINC_INSTALLER"` + installArgs,
		prefix + "mkdir -p /etc/cinc",
		// Create the private key 0600 *before* writing, so it is never
		// world-readable on the target (tee would otherwise create it with the
		// remote umask, typically 0644, until the chmod ran).
		prefix + "install -m 0600 /dev/null /etc/cinc/client.pem",
		writeKey,
		prefix + "chmod 0600 /etc/cinc/client.pem",
		writeClientRB,
		writeFirstBoot,
		prefix + "cinc-client -j /etc/cinc/first-boot.json",
	}
	return strings.Join(commands, "\n"), nil
}

func firstBootJSON(opts BootstrapOptions) ([]byte, error) {
	doc := map[string]any{}
	// A Policyfile-managed node and a run-list/environment-managed node are two
	// mutually exclusive management modes — cinc-client/chef-client aborts the
	// first run if policy_name/policy_group are set alongside chef_environment.
	// The --environment flag defaults to "_default", so emit chef_environment
	// only when the node isn't policy-managed.
	policyManaged := opts.PolicyName != "" || opts.PolicyGroup != ""
	if len(opts.RunList) > 0 {
		doc["run_list"] = opts.RunList
	}
	if opts.Environment != "" && !policyManaged {
		doc["chef_environment"] = opts.Environment
	}
	if opts.PolicyName != "" {
		doc["policy_name"] = opts.PolicyName
	}
	if opts.PolicyGroup != "" {
		doc["policy_group"] = opts.PolicyGroup
	}
	return json.MarshalIndent(doc, "", "  ")
}

func clientRB(opts BootstrapOptions) string {
	return fmt.Sprintf("chef_server_url %s\nnode_name %s\nclient_key %s\n",
		rubyQuote(opts.ServerURL), rubyQuote(opts.NodeName), rubyQuote("/etc/cinc/client.pem"))
}

// rubyQuote renders s as a single-quoted Ruby string literal. Ruby single
// quotes do not interpolate #{...} (unlike double quotes and unlike Go's %q),
// so this prevents a node name or server URL from injecting Ruby code into the
// client.rb that cinc-client evaluates on the target. Only backslash and the
// single quote itself are special inside a single-quoted Ruby literal.
func rubyQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// heredocDelimiter terminates every file the bootstrap script writes. A line
// in the body equal to it would close the heredoc early and leave the rest of
// the body to be executed as shell, so writeFileCommand refuses that content
// outright. Callers validate their own inputs; this is the backstop that
// makes the heredoc safe regardless.
const heredocDelimiter = "CINC_EOF"

func writeFileCommand(prefix, path, content string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimRight(line, "\r") == heredocDelimiter {
			return "", fmt.Errorf("refusing to write %s: its content contains a %s line, which would end the heredoc early", path, heredocDelimiter)
		}
	}
	return "cat <<'" + heredocDelimiter + "' | " + prefix + "tee " + shellQuote(path) + " >/dev/null\n" + content + "\n" + heredocDelimiter, nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
