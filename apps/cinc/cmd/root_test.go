package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFirstRunExitsCleanlyBeforeServerCommandRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, true)
	swapConfigure(t, fakeConfigure(t))

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader("\n"))
	root.SetArgs([]string{"node", "list"})

	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "first-run setup completed") {
		t.Fatalf("expected first-run sentinel from cinc node list, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "Welcome to") {
		t.Errorf("expected welcome on stderr, got:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "name") || strings.Contains(stdout.String(), "node") {
		t.Errorf("node list must not have run after first-run setup; stdout:\n%s", stdout.String())
	}
}

func TestRootCommandRunsVersionSubcommand(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc version failed: %v", err)
	}

	if !strings.HasPrefix(buf.String(), "cinc ") {
		t.Errorf("expected version output from `cinc version`, got:\n%s", buf.String())
	}
}

// runBareCinc executes `cinc` with no subcommand, returning combined
// stdout and stderr output.
func runBareCinc(t *testing.T, stdin string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(nil)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestBareCincOffersMigrationWhenChefCredentialsPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".chef"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapTTY(t, true)

	called := false
	swapMigrate(t, func(_, cincPath string) (int, error) {
		called = true
		dir := filepath.Dir(cincPath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return 0, err
		}
		return 1, os.WriteFile(cincPath, []byte(`[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/k/t.pem"
`), 0o600)
	})

	// First line answers the "run interactive setup?" gate, second the migration prompt.
	stdout, stderr, err := runBareCinc(t, "y\ny\n")
	if err != nil {
		t.Fatalf("bare cinc returned error: %v", err)
	}
	if !called {
		t.Error("expected migrateChef to be invoked on bare cinc")
	}
	if !strings.Contains(stderr, "Welcome to") {
		t.Errorf("expected welcome on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "first time using Cinc") {
		t.Errorf("expected the interactive-setup prompt on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "migrate it") {
		t.Errorf("expected migration prompt on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Cinc is a unified") {
		t.Errorf("expected help text on stdout after migration, got:\n%s", stdout)
	}
}

func TestBareCincRunsConfigureWhenChefAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	swapTTY(t, true)
	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not run when no chef file exists")
		return 0, nil
	})

	called := false
	swapConfigure(t, func(cmd *cobra.Command, cincPath string) error {
		called = true
		return fakeConfigure(t)(cmd, cincPath)
	})

	stdout, stderr, err := runBareCinc(t, "\n")
	if err != nil {
		t.Fatalf("bare cinc returned error: %v", err)
	}
	if !called {
		t.Error("expected runFirstRunConfigure to be invoked when no chef file exists")
	}
	if strings.Contains(stderr, "migrate it") {
		t.Errorf("did not expect a migration prompt, got stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Welcome to") {
		t.Errorf("expected a welcome line on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Cinc is a unified") {
		t.Errorf("expected help text on stdout, got:\n%s", stdout)
	}
}

func TestBareCincSkipsMigrationWhenCincCredentialsExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".cinc"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".cinc", "credentials"), []byte("[default]\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".chef"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("placeholder"), 0o600)
	swapTTY(t, true)
	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not run when cinc credentials already exist")
		return 0, nil
	})

	stdout, stderr, err := runBareCinc(t, "")
	if err != nil {
		t.Fatalf("bare cinc returned error: %v", err)
	}
	if strings.Contains(stderr, "migrate it") {
		t.Errorf("did not expect a migration prompt, got stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "Welcome to") {
		t.Errorf("did not expect a welcome line when cinc creds already exist, got:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Cinc is a unified") {
		t.Errorf("expected help text on stdout, got:\n%s", stdout)
	}
}

func TestBareCincExitsWithoutHelpWhenSetupDeclined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".chef"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("placeholder"), 0o600)
	swapTTY(t, true)
	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not run when the user declines")
		return 0, nil
	})

	stdout, stderr, err := runBareCinc(t, "n\n")
	if err != nil {
		t.Fatalf("bare cinc returned error: %v", err)
	}
	if !strings.Contains(stderr, "first time using Cinc") {
		t.Errorf("expected the interactive-setup prompt on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "No problem") {
		t.Errorf("expected a friendly decline message on stderr, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "Cinc is a unified") {
		t.Errorf("expected no help text on stdout after declining setup; the app should just exit, got:\n%s", stdout)
	}
}

func TestBareCincSetupPromptIsOnItsOwnLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, true)

	_, stderr, err := runBareCinc(t, "n\n")
	if err != nil {
		t.Fatalf("bare cinc returned error: %v", err)
	}
	if !strings.Contains(stderr, "\nWould you like to run the interactive setup? (Y/n) ") {
		t.Errorf("expected the setup question on its own line, got:\n%s", stderr)
	}
}

// TestUnknownFormatIsRejectedBeforeAnyCommandActs checks --format is
// validated for every command before it runs, not only by the commands that
// print structured output. A create that ignores --format yaml has already
// changed the server by the time anything could complain.
func TestUnknownFormatIsRejectedBeforeAnyCommandActs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	cfg := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name = "tim"
client_key = %q
`, srv.URL, writeTestKey(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(t.TempDir(), "written")

	for _, args := range [][]string{
		{"node", "create", "web01", "--config", cfg},
		{"environment", "create", "prod", "--config", cfg},
		{"config", "create", "--config", written, "--server-url", srv.URL + "/organizations/acme", "--client-name", "tim", "--client-key", "/k.pem"},
		{"version"},
	} {
		_, _, err := runRoot(t, append(args, "--format", "yaml")...)
		if err == nil {
			t.Errorf("%v --format yaml succeeded", args)
			continue
		}
		for _, want := range []string{`"yaml"`, "human", "json"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: error %q should mention %s", args, err, want)
			}
		}
	}
	if len(requests) != 0 {
		t.Errorf("a rejected --format still reached the server: %v", requests)
	}
	if _, err := os.Stat(written); err == nil {
		t.Errorf("config create --format yaml wrote %s", written)
	}

	// The accepted formats still pass the check.
	for _, format := range []string{"human", "json"} {
		if _, _, err := runRoot(t, "version", "--format", format); err != nil {
			t.Errorf("version --format %s: %v", format, err)
		}
	}
}
