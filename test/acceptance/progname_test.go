//go:build acceptance

package acceptance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// installAs copies the built binary under a different name, the way a
// distribution that has already spent the `cinc` name would.
func installAs(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(buildCinc(t))
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(renamed, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return renamed
}

// TestRenamedBinaryFollowsInvokedName asserts that a renamed install names
// itself consistently: the usage line, the examples printed directly below
// it, the version banner, the credentials guidance and the completion
// script all have to agree, or the binary tells users to run a command
// they do not have.
func TestRenamedBinaryFollowsInvokedName(t *testing.T) {
	renamed := installAs(t, "cinc-ng")

	help := runCinc(t, renamed, "--help")
	if !strings.Contains(help, "cinc-ng [command]") {
		t.Errorf("--help usage does not follow the invoked name\ngot:\n%s", help)
	}

	version := runCinc(t, renamed, "version")
	if !strings.HasPrefix(version, "cinc-ng ") {
		t.Errorf("version output does not lead with the invoked name\ngot:\n%s", version)
	}

	// Cobra prints Example verbatim, so this is the half a rename that
	// stops at the root command gets wrong.
	createHelp := runCinc(t, renamed, "config", "create", "--help")
	if !strings.Contains(createHelp, "cinc-ng config create") {
		t.Errorf("examples do not follow the invoked name\ngot:\n%s", createHelp)
	}
	bare := regexp.MustCompile(`(?m)(?:^|[^-\w])cinc (?:config|node|policy) `)
	if bare.MatchString(createHelp) {
		t.Errorf("help still advertises the canonical binary\ngot:\n%s", createHelp)
	}

	home := t.TempDir()
	_, stderr, err := runCincRawEnv(t, []string{"HOME=" + home}, renamed, "node", "list")
	if err == nil {
		t.Fatal("node list with no credentials should fail")
	}
	if !strings.Contains(stderr, "cinc-ng config create") {
		t.Errorf("missing-credentials guidance should name the invoked binary\ngot stderr:\n%s", stderr)
	}

	completion := runCinc(t, renamed, "completion", "bash")
	if !strings.Contains(completion, "__start_cinc-ng()") {
		t.Errorf("bash completion should register under the invoked name\ngot head:\n%.300s", completion)
	}
}

// TestRenamedBinaryRejectsUnsafeName covers the security half: argv[0] is
// caller-controlled and cobra interpolates the root name into completion
// scripts unquoted, so a name that is not a plain command name must never
// reach the output.
func TestRenamedBinaryRejectsUnsafeName(t *testing.T) {
	renamed := installAs(t, "cinc`id`")

	completion := runCinc(t, renamed, "completion", "bash")
	if strings.Contains(completion, "`id`") {
		t.Errorf("an unsafe argv[0] reached the completion script:\n%.400s", completion)
	}
	if !strings.Contains(completion, "__start_cinc()") {
		t.Errorf("expected a fallback to the canonical name\ngot head:\n%.300s", completion)
	}
}
