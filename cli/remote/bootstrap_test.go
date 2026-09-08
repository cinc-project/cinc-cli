package remote

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBootstrapCommandBuildsCincClientScript(t *testing.T) {
	cmd, err := BootstrapCommand(BootstrapOptions{
		NodeName:         "web01",
		ServerURL:        "https://cinc.example.test/organizations/acme",
		ClientKeyPEM:     "PRIVATE KEY",
		RunList:          []string{"recipe[apt]"},
		Environment:      "prod",
		Sudo:             true,
		BootstrapURL:     DefaultBootstrapURL,
		BootstrapVersion: "18",
	})
	if err != nil {
		t.Fatalf("BootstrapCommand: %v", err)
	}
	for _, want := range []string{
		"curl -fsSL 'https://omnitruck.cinc.sh/install.sh' -o",
		"sudo bash \"$CINC_INSTALLER\" -v '18'",
		"sudo mkdir -p /etc/cinc",
		"sudo install -m 0600 /dev/null /etc/cinc/client.pem",
		"chef_server_url 'https://cinc.example.test/organizations/acme'",
		"node_name 'web01'",
		"PRIVATE KEY",
		"\"chef_environment\": \"prod\"",
		"\"recipe[apt]\"",
		"sudo cinc-client -j /etc/cinc/first-boot.json",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
}

// TestBootstrapCommandStopsWhenTheInstallerCannotBeFetched pins that a failed
// download aborts the bootstrap. `curl ... | bash` reports only bash's status,
// so under plain `set -e` a 404 or a TLS failure was swallowed and the script
// carried on to run a cinc-client that had never been installed.
func TestBootstrapCommandStopsWhenTheInstallerCannotBeFetched(t *testing.T) {
	cmd, err := BootstrapCommand(BootstrapOptions{
		NodeName:     "web01",
		ServerURL:    "https://cinc.example.test/organizations/acme",
		ClientKeyPEM: "PRIVATE KEY",
		Sudo:         true,
	})
	if err != nil {
		t.Fatalf("BootstrapCommand: %v", err)
	}
	if strings.Contains(cmd, "| sudo bash") || strings.Contains(cmd, "| bash") {
		t.Errorf("installer is still piped into a shell, so its exit status is lost:\n%s", cmd)
	}
	// -f makes curl fail on an HTTP error rather than saving the error page.
	if !strings.Contains(cmd, "curl -fsSL") {
		t.Errorf("curl should use -f so HTTP errors are failures:\n%s", cmd)
	}
	if !strings.Contains(cmd, "set -e") {
		t.Errorf("script should still abort on the first failing command:\n%s", cmd)
	}
}

// The downloaded installer is removed even when a later step fails.
func TestBootstrapCommandCleansUpTheInstaller(t *testing.T) {
	cmd, err := BootstrapCommand(BootstrapOptions{
		NodeName:     "web01",
		ServerURL:    "https://cinc.example.test/organizations/acme",
		ClientKeyPEM: "PRIVATE KEY",
	})
	if err != nil {
		t.Fatalf("BootstrapCommand: %v", err)
	}
	if !strings.Contains(cmd, "trap") || !strings.Contains(cmd, "rm -f") {
		t.Errorf("script should remove the downloaded installer on exit:\n%s", cmd)
	}
}

// firstBoot extracts and decodes the first-boot.json payload embedded in the
// generated bootstrap script so tests can assert on the actual JSON keys.
func firstBoot(t *testing.T, opts BootstrapOptions) map[string]any {
	t.Helper()
	data, err := firstBootJSON(opts)
	if err != nil {
		t.Fatalf("firstBootJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode first-boot.json: %v", err)
	}
	return doc
}

// A Policyfile-managed node is mutually exclusive with chef_environment;
// cinc-client/chef-client treats setting both as a conflict. The bootstrap
// --environment flag defaults to "_default", so a policy bootstrap must drop
// chef_environment from first-boot.json entirely.
func TestFirstBootJSONOmitsEnvironmentForPolicyBootstrap(t *testing.T) {
	doc := firstBoot(t, BootstrapOptions{
		NodeName:    "web01",
		ServerURL:   "https://cinc.example.test/organizations/acme",
		PolicyName:  "base",
		PolicyGroup: "prod",
		Environment: "_default", // the flag default that leaks the conflict
	})
	if _, ok := doc["chef_environment"]; ok {
		t.Fatalf("policy bootstrap first-boot.json must not set chef_environment: %v", doc)
	}
	if doc["policy_name"] != "base" || doc["policy_group"] != "prod" {
		t.Fatalf("policy fields missing or wrong: %v", doc)
	}
}

// client.rb is evaluated as Ruby on the target. Ruby double-quoted strings
// interpolate #{...}, so values must be emitted as single-quoted literals to
// avoid arbitrary code execution from a node name / server URL.
func TestClientRBDoesNotInterpolateRuby(t *testing.T) {
	rb := clientRB(BootstrapOptions{
		NodeName:  `web#{exec("id")}01`,
		ServerURL: "https://cinc.example.test/organizations/acme",
	})
	if !strings.Contains(rb, `node_name 'web#{exec("id")}01'`) {
		t.Fatalf("node_name should be a single-quoted literal, got:\n%s", rb)
	}
	if strings.Contains(rb, `node_name "`) {
		t.Fatalf("node_name must not be an interpolating double-quoted string:\n%s", rb)
	}
}

// Single-quoted Ruby literals still need backslash and single-quote escaping.
func TestClientRBEscapesQuotesAndBackslashes(t *testing.T) {
	rb := clientRB(BootstrapOptions{
		NodeName:  `it's\ok`,
		ServerURL: "https://x/organizations/o",
	})
	if !strings.Contains(rb, `node_name 'it\'s\\ok'`) {
		t.Fatalf("node_name escaping wrong, got:\n%s", rb)
	}
}

// rubyQuote stops a node name interpolating Ruby, but the client.rb it
// produces is then delivered inside a shell heredoc. A newline in the node
// name (or the profile's server URL) can close that heredoc early and turn the
// rest of the value into commands, which run through sudo on the target. The
// quoting above operates one layer up and cannot see this.
func TestBootstrapCommandRejectsNewlinesInValues(t *testing.T) {
	breakout := "web01\nCINC_EOF\nid > /tmp/pwned\ncat <<'CINC_EOF' | sudo tee /dev/null\nx"
	cases := []struct {
		name string
		opts BootstrapOptions
	}{
		{"node name", BootstrapOptions{
			NodeName:     breakout,
			ServerURL:    "https://cinc.example.test/organizations/acme",
			ClientKeyPEM: "key",
		}},
		{"server URL", BootstrapOptions{
			NodeName:     "web01",
			ServerURL:    "https://cinc.example.test/organizations/acme\nCINC_EOF\nid > /tmp/pwned",
			ClientKeyPEM: "key",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := BootstrapCommand(tc.opts)
			if err == nil {
				t.Fatalf("BootstrapCommand accepted a newline in the %s; generated script:\n%s", tc.name, cmd)
			}
			if !strings.Contains(err.Error(), "newline") {
				t.Errorf("error = %v, want it to name the newline as the problem", err)
			}
		})
	}
}

// Belt and braces for the heredoc itself: whatever the callers validate, a
// body that contains the terminator must never be emitted as a heredoc.
func TestWriteFileCommandRejectsDelimiterInContent(t *testing.T) {
	if _, err := writeFileCommand("sudo ", "/etc/cinc/client.rb", "harmless\nCINC_EOF\nid\n"); err == nil {
		t.Error("writeFileCommand accepted content containing its own heredoc terminator")
	}
	if _, err := writeFileCommand("sudo ", "/etc/cinc/client.rb", "harmless\n"); err != nil {
		t.Errorf("ordinary content should still be accepted: %v", err)
	}
}

// A run-list bootstrap (no policy) keeps chef_environment as before.
func TestFirstBootJSONKeepsEnvironmentForRunListBootstrap(t *testing.T) {
	doc := firstBoot(t, BootstrapOptions{
		NodeName:    "web01",
		ServerURL:   "https://cinc.example.test/organizations/acme",
		RunList:     []string{"recipe[apt]"},
		Environment: "prod",
	})
	if doc["chef_environment"] != "prod" {
		t.Fatalf("run-list bootstrap should keep chef_environment: %v", doc)
	}
}
