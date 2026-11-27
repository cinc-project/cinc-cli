package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// assertExplains fails unless msg mentions every string in want and reads as
// a sentence: no em dash and no "cinc:"/"config:" package prefix.
func assertExplains(t *testing.T, msg string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("message should mention %q, got:\n%s", w, msg)
		}
	}
	if strings.Contains(msg, "—") {
		t.Errorf("message should not use an em dash, got:\n%s", msg)
	}
	for _, prefix := range []string{"cinc:", "config:"} {
		if strings.HasPrefix(msg, prefix) {
			t.Errorf("message should start as a sentence, not %q, got:\n%s", prefix, msg)
		}
	}
}

// With no credentials file and no terminal to prompt on, a server command
// should explain what's missing and show the non-interactive setup.
func TestMissingCredentialsExplainsHowToSetUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, false)
	swapConfigure(t, func(*cobra.Command, string) error {
		t.Error("runFirstRunConfigure should not run without a TTY")
		return nil
	})

	_, err := resolveProfile(fakeCmd("", "", "", new(bytes.Buffer)))
	if err == nil {
		t.Fatal("expected an error without a credentials file")
	}
	assertExplains(t, err.Error(),
		filepath.Join(home, ".cinc", "credentials"),
		"cinc config create",
		"--server-url", "--client-name", "--client-key")
}

func TestConfigCreateExplainsMissingClientName(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	_, _, err := runRoot(t, "config", "create", "--config", cfgPath,
		"--server-url", "https://cinc.example.com/organizations/acme",
		"--client-key", "/keys/tim.pem")
	if err == nil {
		t.Fatal("config create accepted a profile with no client name")
	}
	assertExplains(t, err.Error(), "--client-name", "cinc config create --client-name")
}

func TestConfigCreateExplainsMissingClientKey(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	_, _, err := runRoot(t, "config", "create", "--config", cfgPath,
		"--server-url", "https://cinc.example.com/organizations/acme",
		"--client-name", "tim")
	if err == nil {
		t.Fatal("config create accepted a profile with no client key")
	}
	assertExplains(t, err.Error(), "--client-key", ".pem", `"tim"`)
}

func TestClearCredentialsExplainsFailure(t *testing.T) {
	// A path under a regular file can't be truncated, and isn't "missing".
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "credentials")

	err := clearCredentials(path)
	if err == nil {
		t.Fatal("expected an error clearing a path under a file")
	}
	assertExplains(t, err.Error(), path, "replace")
}

func TestConfigValidateChecksExplainEachProblem(t *testing.T) {
	cfgPath := writeValidateConfig(t, `
[nothing]
ssl_verify_mode = "verify_peer"

[orgless]
client_name     = "tim"
client_key      = "/does/not/exist.pem"
cinc_server_url = "https://cinc.example.com"
`)
	out, _, _ := runRoot(t, "config", "validate", cfgPath)

	assertExplains(t, out,
		"✗ Client name is configured: client_name isn't set",
		"✗ Client key is configured: client_key isn't set",
		"✗ An endpoint is configured: this profile doesn't name a server. Set cinc_server_url",
		`✗ ssl_verify_mode is valid: ssl_verify_mode is "verify_peer"`, ":verify_peer",
		"✗ Server URL is valid: https://cinc.example.com doesn't say which organization",
		"✗ Client key file is readable: there's no file at /does/not/exist.pem",
	)
}

// A file that doesn't parse gets the full explanation, with every line of
// it indented under the check it belongs to.
func TestConfigValidateIndentsAMultilineDetail(t *testing.T) {
	cfgPath := writeValidateConfig(t, "[default]\nclient_name = tim\n")
	out, _, _ := runRoot(t, "config", "validate", cfgPath)

	assertExplains(t, out, "✗ Credentials file is valid TOML: we couldn't read", "line 2")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[2:] {
		if line != "" && !strings.HasPrefix(line, "  ") {
			t.Errorf("continuation line %q should be indented under its check, got:\n%s", line, out)
		}
	}
}
