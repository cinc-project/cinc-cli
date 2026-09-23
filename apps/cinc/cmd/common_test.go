package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// fakeCmd builds a cobra command with the same flags resolveProfile
// reads, plus a stdin/stderr wired to the provided buffers so tests
// can drive the interactive prompts.
func fakeCmd(configFlag, profileFlag, stdin string, stderr *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{Use: "fake"}
	c.Flags().String("config", configFlag, "")
	c.Flags().String("profile", profileFlag, "")
	c.Flags().String("format", "human", "")
	c.SetIn(strings.NewReader(stdin))
	c.SetOut(new(bytes.Buffer))
	c.SetErr(stderr)
	return c
}

// swapTTY sets stdinIsTTY for the duration of one test.
func swapTTY(t *testing.T, isTTY bool) {
	t.Helper()
	prev := stdinIsTTY
	stdinIsTTY = func() bool { return isTTY }
	t.Cleanup(func() { stdinIsTTY = prev })
}

// swapMigrate sets migrateChef for the duration of one test.
func swapMigrate(t *testing.T, fn func(chefPath, cincPath string) (int, error)) {
	t.Helper()
	prev := migrateChef
	migrateChef = fn
	t.Cleanup(func() { migrateChef = prev })
}

// swapConfigure sets runFirstRunConfigure for the duration of one
// test so the no-chef-file branch can be exercised without driving
// the full interactive prompt sequence.
func swapConfigure(t *testing.T, fn func(cmd *cobra.Command, cincPath string) error) {
	t.Helper()
	prev := runFirstRunConfigure
	runFirstRunConfigure = fn
	t.Cleanup(func() { runFirstRunConfigure = prev })
}

// fakeConfigure is a runFirstRunConfigure stand-in that writes a
// valid credentials file at cincPath so a subsequent config.Load
// succeeds.
func fakeConfigure(t *testing.T) func(*cobra.Command, string) error {
	t.Helper()
	return func(_ *cobra.Command, cincPath string) error {
		dir := filepath.Dir(cincPath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return os.WriteFile(cincPath, []byte(`[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/k/t.pem"
`), 0o600)
	}
}

// seedDefaultCreds writes a minimal valid credentials file at
// $HOME/.cinc/credentials and returns the home tempdir.
func seedDefaultCreds(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cinc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/k/t.pem"
`
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestResolveProfileLoadsDefaultPathWhenFlagEmpty(t *testing.T) {
	seedDefaultCreds(t)
	c := fakeCmd("", "", "", new(bytes.Buffer))

	p, err := resolveProfile(c)
	if err != nil {
		t.Fatalf("resolveProfile: %v", err)
	}
	if p.Org != "acme" || p.ClientName != "tim" {
		t.Errorf("unexpected profile: %+v", p)
	}
}

func TestResolveProfileSkipsMigrationWhenExplicitConfigMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	explicit := filepath.Join(t.TempDir(), "no-such.toml")

	called := false
	swapMigrate(t, func(_, _ string) (int, error) {
		called = true
		return 0, nil
	})

	c := fakeCmd(explicit, "", "", new(bytes.Buffer))
	if _, err := resolveProfile(c); err == nil {
		t.Error("expected an error loading the explicit missing config")
	}
	if called {
		t.Error("migrateChef must not run when --config is set explicitly")
	}
}

func TestResolveProfileWelcomesUserOnFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, true)
	swapConfigure(t, fakeConfigure(t))

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", "", stderr)
	if _, err := resolveProfile(c); !errors.Is(err, errFirstRunCompleted) {
		t.Fatalf("resolveProfile after first run = %v, want errFirstRunCompleted", err)
	}
	if !strings.Contains(stderr.String(), "Welcome to") {
		t.Errorf("expected a welcome line on stderr, got:\n%s", stderr.String())
	}
}

func TestResolveProfileRunsMigrationWhenDefaultMissingAndChefExists(t *testing.T) {
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
		// Write a usable cinc file so the retry succeeds.
		dir := filepath.Dir(cincPath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return 0, err
		}
		body := `[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/k/t.pem"
`
		return 1, os.WriteFile(cincPath, []byte(body), 0o600)
	})

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", "y\n", stderr)
	if _, err := resolveProfile(c); !errors.Is(err, errFirstRunCompleted) {
		t.Fatalf("resolveProfile after migration = %v, want errFirstRunCompleted", err)
	}
	if !called {
		t.Error("expected migrateChef to be invoked")
	}
	if !strings.Contains(stderr.String(), "migrate it") {
		t.Errorf("expected migration prompt on stderr, got:\n%s", stderr.String())
	}
}

func TestResolveProfileAcceptsBlankAnswerAsYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".chef"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("x"), 0o600)
	swapTTY(t, true)

	called := false
	swapMigrate(t, func(_, cincPath string) (int, error) {
		called = true
		_ = os.MkdirAll(filepath.Dir(cincPath), 0o700)
		return 1, os.WriteFile(cincPath, []byte(`[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/k/t.pem"
`), 0o600)
	})

	c := fakeCmd("", "", "\n", new(bytes.Buffer))
	if _, err := resolveProfile(c); !errors.Is(err, errFirstRunCompleted) {
		t.Fatalf("resolveProfile = %v, want errFirstRunCompleted", err)
	}
	if !called {
		t.Error("blank answer should default to yes; expected migrateChef call")
	}
}

func TestResolveProfileDeclinedMigrationPointsAtConfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".chef"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("x"), 0o600)
	swapTTY(t, true)

	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not be called when the user declines")
		return 0, nil
	})

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", "n\n", stderr)
	_, err := resolveProfile(c)
	if err == nil || !strings.Contains(err.Error(), "cinc config create") {
		t.Errorf("expected an error mentioning `cinc config create`, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "No problem") {
		t.Errorf("expected a friendly acknowledgement after decline, got:\n%s", stderr.String())
	}
}

func TestResolveProfileRunsConfigureWhenNoChefFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, true)
	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not be called when ~/.chef/credentials is absent")
		return 0, nil
	})

	called := false
	swapConfigure(t, func(cmd *cobra.Command, cincPath string) error {
		called = true
		return fakeConfigure(t)(cmd, cincPath)
	})

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", "", stderr)
	if _, err := resolveProfile(c); !errors.Is(err, errFirstRunCompleted) {
		t.Fatalf("resolveProfile after configure = %v, want errFirstRunCompleted", err)
	}
	if !called {
		t.Error("expected runFirstRunConfigure to be invoked when no chef file is present")
	}
	if !strings.Contains(stderr.String(), "Welcome to") {
		t.Errorf("expected welcome on stderr, got:\n%s", stderr.String())
	}
}

func TestResolveClientReportsMissingKeyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cincDir := filepath.Join(home, ".cinc")
	if err := os.MkdirAll(cincDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cincDir, "credentials")
	keyPath := filepath.Join(home, "missing.pem")
	body := fmt.Sprintf(`[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = %q
`, keyPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c := fakeCmd("", "", "", new(bytes.Buffer))
	_, err := resolveClient(c)
	if err == nil {
		t.Fatal("expected an error when the key file does not exist")
	}
	msg := err.Error()
	for _, want := range []string{keyPath, cfgPath, "client_key"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "no such file or directory") {
		t.Errorf("error should be conversational, not surface the raw os error: %s", msg)
	}
}

func TestResolveClientReportsUnreadableKeyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses file permissions")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cincDir := filepath.Join(home, ".cinc")
	if err := os.MkdirAll(cincDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cincDir, "credentials")
	keyPath := filepath.Join(home, "locked.pem")
	if err := os.WriteFile(keyPath, []byte("placeholder"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyPath, 0o600) })

	body := fmt.Sprintf(`[default]
chef_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = %q
`, keyPath)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c := fakeCmd("", "", "", new(bytes.Buffer))
	_, err := resolveClient(c)
	if err == nil {
		t.Fatal("expected an error when the key file is unreadable")
	}
	msg := err.Error()
	for _, want := range []string{keyPath, cfgPath, "permission"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must mention %q", msg, want)
		}
	}
}

func TestResolveProfilePointsAtConfigureWhenStdinNotTTY(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".chef"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".chef", "credentials"), []byte("x"), 0o600)
	swapTTY(t, false)
	swapMigrate(t, func(_, _ string) (int, error) {
		t.Error("migrateChef should not be called when stdin is not a TTY")
		return 0, nil
	})
	swapConfigure(t, func(*cobra.Command, string) error {
		t.Error("runFirstRunConfigure should not be called when stdin is not a TTY")
		return nil
	})

	c := fakeCmd("", "", "y\n", new(bytes.Buffer))
	_, err := resolveProfile(c)
	if err == nil || !strings.Contains(err.Error(), "cinc config create") {
		t.Errorf("expected an error mentioning `cinc config create`, got: %v", err)
	}
}

// TestResolveProfileExpandsTildeInConfigFlag covers --config=~/..., which no
// shell expands (the ~ does not start a word), and a quoted --config "~/...".
func TestResolveProfileExpandsTildeInConfigFlag(t *testing.T) {
	home := seedDefaultCreds(t)
	body, err := os.ReadFile(filepath.Join(home, ".cinc", "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "elsewhere"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	c := fakeCmd("~/elsewhere", "", "", new(bytes.Buffer))
	p, err := resolveProfile(c)
	if err != nil {
		t.Fatalf("resolveProfile with --config ~/elsewhere: %v", err)
	}
	if p.ClientName != "tim" {
		t.Errorf("unexpected profile: %+v", p)
	}
}

// TestResolveClientReportsMissingTildeKeyFile checks the missing-key message
// for a ~ path names the expanded path it actually tried.
func TestResolveClientReportsMissingTildeKeyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".cinc", "credentials")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `[default]
cinc_server_url = "https://x.example.com/organizations/acme"
client_name     = "tim"
client_key      = "~/missing.pem"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveClient(fakeCmd("", "", "", new(bytes.Buffer)))
	if err == nil {
		t.Fatal("expected an error when the key file does not exist")
	}
	if want := filepath.Join(home, "missing.pem"); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should name the expanded path %s", err, want)
	}
	if !strings.Contains(err.Error(), "can't find your client key") {
		t.Errorf("error %q should be the friendly missing-key message", err)
	}
}

// TestResolveSecretExpandsTildeInSecretFile checks a profile's secret_file
// written as ~/... is read from the home directory.
func TestResolveSecretExpandsTildeInSecretFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CINC_SECRET_FILE", "")
	t.Setenv("CHEF_SECRET_FILE", "")
	if err := os.WriteFile(filepath.Join(home, "secret"), []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSecret(fakeCmd("", "", "", new(bytes.Buffer)), config.Profile{SecretFile: "~/secret"})
	if err != nil {
		t.Fatalf("resolveSecret: %v", err)
	}
	if string(got) != "s3cret" {
		t.Errorf("secret = %q, want the contents of ~/secret", got)
	}
}
