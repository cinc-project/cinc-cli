package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// TestConfigureStopsWhenStdinIsExhausted covers `cinc config create` run
// without an interactive terminal (for example `cinc config create <
// /dev/null`, or from CI) against a credentials file that already holds
// profiles. The action prompt defaults to "Add a new profile", which then
// asks for a name it will not accept as empty. With no more input to read
// the command has to give up with a clear error rather than re-asking forever.
func TestConfigureStopsWhenStdinIsExhausted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL:  "https://old.example.test",
		Org:        "old",
		ClientName: "old",
		KeyPath:    "/keys/old.pem",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	done := make(chan error, 1)
	go func() { done <- root.Execute() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when stdin is exhausted, got nil")
		}
		if !strings.Contains(err.Error(), "ran out of input") {
			t.Fatalf("error = %v, want it to explain that input ran out", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("config create never returned; it asked for a profile name %d times",
			strings.Count(out.String(), "New profile name"))
	}
}

func TestConfigureCommandWritesTOMLCredentialsProfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	keyPath := filepath.Join(dir, "damacus.pem")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"config", "create",
		"--server-url", "https://api.chef.io/organizations/damacus",
		"--supermarket-site", "https://supermarket.chef.io",
		"--client-name", "damacus",
		"--client-key", keyPath,
		"--profile", "supermarket",
		"--config", cfgPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	p := cfg.Profiles["supermarket"]
	if p.ServerURL != "https://api.chef.io" || p.Org != "damacus" || p.SupermarketSite != "https://supermarket.chef.io" ||
		p.ClientName != "damacus" || p.KeyPath != keyPath {
		t.Fatalf("profile = %+v", p)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "client.rb") || !strings.Contains(string(data), "cinc_server_url") ||
		!strings.Contains(string(data), "supermarket_site") {
		t.Fatalf("credentials = %s, want TOML credentials with cinc_server_url, supermarket_site, and no client.rb", data)
	}
	if got := buf.String(); !strings.Contains(got, `Wrote credentials profile "supermarket"`) {
		t.Fatalf("stdout = %q", got)
	}
}

func TestConfigureCommandAcceptsSupermarketURLAsServerURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	keyPath := filepath.Join(dir, "damacus.pem")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{
		"config", "create",
		"--server-url", "https://supermarket.chef.io",
		"--client-name", "damacus",
		"--client-key", keyPath,
		"--config", cfgPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	p := cfg.Profiles["supermarket"]
	if p.ServerURL != "" || p.Org != "" || p.SupermarketSite != "https://supermarket.chef.io" {
		t.Fatalf("profile = %+v, want supermarket-only profile", p)
	}
}

func TestConfigureCommandSecondRunMutatesSameProfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	firstKeyPath := filepath.Join(dir, "first.pem")
	secondKeyPath := filepath.Join(dir, "second.pem")
	for _, path := range []string{firstKeyPath, secondKeyPath} {
		if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	first := newRootCmd()
	first.SetOut(&bytes.Buffer{})
	first.SetArgs([]string{
		"config", "create",
		"--server-url", "https://api.chef.io/organizations/old-org",
		"--client-name", "old-client",
		"--client-key", firstKeyPath,
		"--profile", "supermarket",
		"--config", cfgPath,
	})
	if err := first.Execute(); err != nil {
		t.Fatalf("first cinc config create: %v", err)
	}

	second := newRootCmd()
	second.SetOut(&bytes.Buffer{})
	second.SetArgs([]string{
		"config", "create",
		"--server-url", "https://supermarket.chef.io",
		"--client-name", "damacus",
		"--client-key", secondKeyPath,
		"--profile", "supermarket",
		"--config", cfgPath,
	})
	if err := second.Execute(); err != nil {
		t.Fatalf("second cinc config create: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want only the mutated supermarket profile", cfg.Profiles)
	}
	p := cfg.Profiles["supermarket"]
	if p.ServerURL != "" || p.Org != "" || p.SupermarketSite != "https://supermarket.chef.io" ||
		p.ClientName != "damacus" || p.KeyPath != secondKeyPath {
		t.Fatalf("profile = %+v, want second run to replace first run values", p)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old-org") || strings.Contains(string(data), firstKeyPath) ||
		strings.Contains(string(data), "chef_server_url") {
		t.Fatalf("credentials = %s, want stale first-run values removed", data)
	}
}

func TestConfigureCommandOnboardsWithDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "damacus")

	keyPath := filepath.Join(home, ".cinc", "damacus.pem")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".cinc", "credentials")

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetIn(strings.NewReader("\n\n\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	p := cfg.Profiles["supermarket"]
	if p.SupermarketSite != "https://supermarket.chef.io" || p.ClientName != "damacus" || p.KeyPath != keyPath {
		t.Fatalf("profile = %+v", p)
	}
	stdout := out.String()
	for _, want := range []string{
		"Credentials file location [" + cfgPath + "]",
		"Profile name [default]",
		"Supermarket site [https://supermarket.chef.io]",
		"Client key path [" + keyPath + "]",
		"Chef server host (optional, e.g. chef.example.com) []",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want prompt %q", stdout, want)
		}
	}
}

func TestConfigureCommandPreservesExistingProfiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	keyPath := filepath.Join(dir, "new.pem")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL:  "https://old.example.test",
		Org:        "old",
		ClientName: "old",
		KeyPath:    "/keys/old.pem",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{
		"config", "create",
		"--chef-server-url", "https://api.chef.io/organizations/damacus",
		"--client-name", "damacus",
		"--client-key", keyPath,
		"--profile", "damacus",
		"--config", cfgPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Fatal("default profile was not preserved")
	}
	if cfg.Profiles["damacus"].ClientName != "damacus" {
		t.Fatalf("damacus profile = %+v", cfg.Profiles["damacus"])
	}
}

func TestConfigureInteractiveAddsNewProfileWhenFileExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "damacus")

	cfgPath := filepath.Join(home, ".cinc", "credentials")
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL:  "https://old.example.test",
		Org:        "old",
		ClientName: "old",
		KeyPath:    "/keys/old.pem",
	}); err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(home, ".cinc", "damacus.pem")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	// Stdin answers, in order:
	//   credentials file location (default)
	//   action prompt           (default = 1 = Add new profile)
	//   new profile name        ("staging")
	//   supermarket site        (default)
	//   client name             (default)
	//   client key path         (default)
	//   chef server host        (empty)
	//   ssl verify mode         (default)
	root.SetIn(strings.NewReader("\n\nstaging\n\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	stdout := out.String()
	for _, want := range []string{
		"You already have credentials at " + cfgPath + " with profiles:",
		"  - default",
		"1) Add a new profile",
		"2) Update an existing profile",
		"3) Replace the credentials file",
		"New profile name:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Fatal("default profile must be preserved when adding a new profile")
	}
	added, ok := cfg.Profiles["staging"]
	if !ok {
		t.Fatalf("staging profile was not added; profiles = %v", cfg.Profiles)
	}
	if added.ClientName != "damacus" || added.KeyPath != keyPath {
		t.Fatalf("staging profile = %+v, want client_name=damacus key=%s", added, keyPath)
	}
}

func TestConfigureInteractiveUpdatesExistingProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "damacus")

	cfgPath := filepath.Join(home, ".cinc", "credentials")
	stagingKey := filepath.Join(home, ".cinc", "staging.pem")
	defaultKey := filepath.Join(home, ".cinc", "default.pem")
	if err := os.MkdirAll(filepath.Dir(stagingKey), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{stagingKey, defaultKey} {
		if err := os.WriteFile(p, []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL: "https://default.example.test", Org: "default-org",
		ClientName: "default-client", KeyPath: defaultKey, SSLVerifyMode: ":verify_peer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "staging", config.Profile{
		ServerURL: "https://staging.example.test", Org: "staging-org",
		ClientName: "staging-client", KeyPath: stagingKey, SSLVerifyMode: ":verify_none",
		SupermarketSite: "https://supermarket.example.test",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	// Stdin answers, in order:
	//   credentials file location (default)
	//   action prompt           (2 = Update existing)
	//   profile picker          (2 = staging; sorted: default, staging)
	//   supermarket site        (Enter -> keep existing)
	//   client name             (Enter -> keep existing)
	//   client key path         (Enter -> keep existing)
	//   chef server host        (Enter -> keep existing)
	//   chef server org         (Enter -> keep existing)
	//   ssl verify mode         (Enter -> keep existing)
	root.SetIn(strings.NewReader("\n2\n2\n\n\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	stdout := out.String()
	for _, want := range []string{
		"Which profile would you like to update?",
		"1) default",
		"2) staging",
		"Supermarket site [https://supermarket.example.test]",
		"Client name [staging-client]",
		"Client key path [" + stagingKey + "]",
		"Chef server host (optional, e.g. chef.example.com) [staging.example.test]",
		"Chef server organization [staging-org]",
		"SSL verify mode [:verify_none]",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\nfull stdout: %s", want, stdout)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %v", cfg.Profiles)
	}
	def := cfg.Profiles["default"]
	if def.ClientName != "default-client" || def.KeyPath != defaultKey || def.Org != "default-org" {
		t.Fatalf("default profile got mutated: %+v", def)
	}
	staging := cfg.Profiles["staging"]
	if staging.ClientName != "staging-client" || staging.KeyPath != stagingKey ||
		staging.ServerURL != "https://staging.example.test" || staging.Org != "staging-org" ||
		staging.SSLVerifyMode != ":verify_none" || staging.SupermarketSite != "https://supermarket.example.test" {
		t.Fatalf("staging profile values changed: %+v", staging)
	}
}

func TestConfigureInteractiveReplacesCredentialsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "damacus")

	cfgPath := filepath.Join(home, ".cinc", "credentials")
	keyPath := filepath.Join(home, ".cinc", "damacus.pem")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL: "https://old.example.test", Org: "old-org",
		ClientName: "old", KeyPath: "/keys/old.pem",
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "staging", config.Profile{
		ServerURL: "https://staging.example.test", Org: "staging-org",
		ClientName: "staging", KeyPath: "/keys/staging.pem",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	// Stdin answers, in order:
	//   credentials file location (default)
	//   action prompt           (3 = Replace)
	//   confirm replace         (y)
	//   profile name            ("fresh")
	//   supermarket site        (custom, to avoid the supermarket auto-rename)
	//   client name             (default)
	//   client key path         (default)
	//   chef server host        (empty)
	//   ssl verify mode         (default)
	root.SetIn(strings.NewReader("\n3\ny\nfresh\nhttps://supermarket.example.test\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	stdout := out.String()
	for _, want := range []string{
		"This will delete profiles: default, staging.",
		"Replace the file? [y/N]",
		"Profile name [default]",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\nfull stdout: %s", want, stdout)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("expected 1 profile after replace, got %v", cfg.Profiles)
	}
	fresh, ok := cfg.Profiles["fresh"]
	if !ok {
		t.Fatalf("fresh profile missing; profiles = %v", cfg.Profiles)
	}
	if fresh.ClientName != "damacus" || fresh.KeyPath != keyPath {
		t.Fatalf("fresh profile = %+v", fresh)
	}
}

func TestConfigureInteractiveAddNewWithCollisionOffersUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "damacus")

	cfgPath := filepath.Join(home, ".cinc", "credentials")
	defaultKey := filepath.Join(home, ".cinc", "default.pem")
	if err := os.MkdirAll(filepath.Dir(defaultKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultKey, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL: "https://default.example.test", Org: "default-org",
		ClientName: "old-client", KeyPath: defaultKey, SSLVerifyMode: ":verify_peer",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	// Stdin answers, in order:
	//   credentials file location (default)
	//   action prompt           (default = 1 = Add new)
	//   new profile name        ("default" -> collides)
	//   "Update it instead?"    (Enter -> Y default)
	//   supermarket site        (Enter -> existing)
	//   client name             ("new-client" -> change it)
	//   client key path         (Enter -> existing)
	//   chef server host        (Enter -> existing)
	//   chef server org         (Enter -> existing)
	//   ssl verify mode         (Enter -> existing)
	root.SetIn(strings.NewReader("\n\ndefault\n\n\nnew-client\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	stdout := out.String()
	for _, want := range []string{
		`A profile named "default" already exists.`,
		"Update it instead? [Y/n]",
		"Client name [old-client]",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\nfull stdout: %s", want, stdout)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %v", cfg.Profiles)
	}
	got := cfg.Profiles["default"]
	if got.ClientName != "new-client" {
		t.Fatalf("default.ClientName = %q, want new-client", got.ClientName)
	}
	if got.KeyPath != defaultKey || got.ServerURL != "https://default.example.test" || got.Org != "default-org" {
		t.Fatalf("default profile = %+v, want other fields preserved", got)
	}
}

func TestConfigureNonInteractiveSkipsActionPrompt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "credentials")
	keyPath := filepath.Join(dir, "new.pem")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL:  "https://old.example.test",
		Org:        "old",
		ClientName: "old",
		KeyPath:    "/keys/old.pem",
	}); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{
		"config", "create",
		"--chef-server-url", "https://api.chef.io/organizations/damacus",
		"--client-name", "damacus",
		"--client-key", keyPath,
		"--profile", "damacus",
		"--config", cfgPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	stdout := out.String()
	for _, forbidden := range []string{
		"You already have credentials",
		"What would you like to do?",
		"Add a new profile",
		"Replace the credentials file",
	} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("non-interactive run unexpectedly emitted %q\nfull stdout: %s", forbidden, stdout)
		}
	}
}

// seedSharedProfile writes a credentials file holding the three keys
// `config create` never prompts for, plus one key cinc does not model.
func seedSharedProfile(t *testing.T, dir string) (cfgPath, keyPath string) {
	t.Helper()
	cfgPath = filepath.Join(dir, "credentials")
	keyPath = filepath.Join(dir, "tim.pem")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := "[default]\n" +
		"cinc_server_url = \"https://cinc.example.com/organizations/acme\"\n" +
		"client_name = \"tim\"\n" +
		"client_key = \"" + keyPath + "\"\n" +
		"secret_file = \"/keys/databag_secret\"\n" +
		"supermarket_client_name = \"tim-public\"\n" +
		"supermarket_key = \"/keys/supermarket.pem\"\n" +
		"node_name = \"tim\"\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, keyPath
}

func assertUnpromptedKeysSurvive(t *testing.T, cfgPath string) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Profiles["default"]
	for name, val := range map[string]string{
		"secret_file":             p.SecretFile,
		"supermarket_client_name": p.SupermarketClientName,
		"supermarket_key":         p.SupermarketKey,
	} {
		if val == "" {
			t.Errorf("%s was wiped by the update", name)
		}
	}
}

// The flag-driven path never runs the prompts, so a fix that only
// populates values inside the interactive branch does not reach it.
func TestConfigureNonInteractiveUpdateKeepsUnpromptedKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath, keyPath := seedSharedProfile(t, dir)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{
		"config", "create",
		"--config", cfgPath,
		"--server-url", "https://new.example.com/organizations/acme",
		"--client-name", "tim",
		"--client-key", keyPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("config create: %v", err)
	}

	assertUnpromptedKeysSurvive(t, cfgPath)
	cfg, _ := config.Load(cfgPath)
	if got := cfg.Profiles["default"].ServerURL; got != "https://new.example.com" {
		t.Errorf("server URL not updated: %q", got)
	}
}

func TestConfigureInteractiveUpdateKeepsUnpromptedKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath, _ := seedSharedProfile(t, dir)

	// Accept the config path, choose "2) Update an existing profile",
	// pick profile 1, then accept every prompted default.
	root := newRootCmd()
	root.SetIn(strings.NewReader(cfgPath + "\n2\n1\n\n\n\n\n\n\n\n"))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"config", "create", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("config create: %v", err)
	}

	assertUnpromptedKeysSurvive(t, cfgPath)
}

// The first-run flow shares promptConfigure with `config create` and
// writes through the same machinery, so it needs the same guarantee.
func TestFirstRunConfigureKeepsUnpromptedKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath, _ := seedSharedProfile(t, dir)
	swapTTY(t, true)

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", cfgPath+"\n2\n1\n\n\n\n\n\n\n\n", stderr)
	if err := realRunFirstRunConfigure(c, cfgPath); err != nil {
		t.Fatalf("first-run configure: %v", err)
	}

	assertUnpromptedKeysSurvive(t, cfgPath)
}

// TestConfigureInteractiveAcceptsServerURLAtHostPrompt pastes the server's
// URL, not just its host, at the host prompt. That is the natural answer,
// and the only one that can describe a plain-HTTP server or one on another
// port: the prompt used to glue "https://" in front of whatever it got,
// producing "https://http://...".
func TestConfigureInteractiveAcceptsServerURLAtHostPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, host, org     string
		wantServer, wantOrg string
	}{
		{"bare host", "cinc.example.test", "acme", "https://cinc.example.test", "acme"},
		{"host and port", "cinc.example.test:8443", "acme", "https://cinc.example.test:8443", "acme"},
		{"http URL with port", "http://127.0.0.1:8889", "acme", "http://127.0.0.1:8889", "acme"},
		{"https URL with trailing slash", "https://cinc.example.test/", "acme", "https://cinc.example.test", "acme"},
		// A full org URL offers its org as the default, so Enter keeps it.
		{"full org URL", "https://cinc.example.test/organizations/from-url", "", "https://cinc.example.test", "from-url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			cfgPath := filepath.Join(home, "credentials")
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			// location, profile, supermarket, client name, key, host, org,
			// ssl. A private Supermarket keeps the profile out of the
			// public-Supermarket rename, which is not what this tests.
			root.SetIn(strings.NewReader(strings.Join([]string{
				cfgPath, "lab", "https://supermarket.example.test", "tim", "/keys/tim.pem", tc.host, tc.org, "",
			}, "\n") + "\n"))
			root.SetArgs([]string{"config", "create"})
			if err := root.Execute(); err != nil {
				t.Fatalf("cinc config create: %v\n%s", err, out.String())
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			p := cfg.Profiles["lab"]
			if p.ServerURL != tc.wantServer || p.Org != tc.wantOrg {
				t.Errorf("profile server = %q org = %q, want %q and %q", p.ServerURL, p.Org, tc.wantServer, tc.wantOrg)
			}
		})
	}
}

// TestConfigureInteractiveUpdateKeepsServerScheme updates a profile on a
// plain-HTTP server with a port and accepts every default. The host prompt
// used to offer only the host, then rebuild the URL as https, so pressing
// Enter broke a working profile.
func TestConfigureInteractiveUpdateKeepsServerScheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, "credentials")
	if err := config.WriteProfile(cfgPath, "default", config.Profile{
		ServerURL: "http://lab.example.test:8889", Org: "acme",
		ClientName: "tim", KeyPath: "/keys/tim.pem",
	}); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	// location, action (2 = update), picker (1 = default), then Enter for
	// supermarket, client name, key, host, org, ssl.
	root.SetIn(strings.NewReader("\n2\n1\n\n\n\n\n\n\n"))
	root.SetArgs([]string{"config", "create", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config create: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "[http://lab.example.test:8889]") {
		t.Errorf("the host prompt should offer the scheme and port it will keep:\n%s", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Profiles["default"]; p.ServerURL != "http://lab.example.test:8889" || p.Org != "acme" {
		t.Errorf("accepting every default changed the server: %+v", p)
	}
}

// TestConfigureInteractiveKeepsProfileNameForAServerProfile answers the
// prompts for a server profile, leaving the Supermarket prompt at its public
// default as nearly everyone will. The profile has a Cinc Server, so it is
// not a Supermarket-only profile and must keep the name typed for it; it
// used to be written as [supermarket], leaving the named profile missing.
func TestConfigureInteractiveKeepsProfileNameForAServerProfile(t *testing.T) {
	for _, name := range []string{"lab", "default"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			cfgPath := filepath.Join(home, "credentials")
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			// location, profile, supermarket (Enter: public default),
			// client name, key, host, org, ssl
			root.SetIn(strings.NewReader(strings.Join([]string{
				cfgPath, name, "", "tim", "/keys/tim.pem", "cinc.example.test", "acme", "",
			}, "\n") + "\n"))
			root.SetArgs([]string{"config", "create"})
			if err := root.Execute(); err != nil {
				t.Fatalf("cinc config create: %v\n%s", err, out.String())
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				t.Fatalf("no [%s] profile written; got %v", name, sortedProfileNames(cfg))
			}
			if p.ServerURL != "https://cinc.example.test" || p.Org != "acme" {
				t.Errorf("[%s] = %+v, want the server profile", name, p)
			}
			if !strings.Contains(out.String(), fmt.Sprintf("Wrote credentials profile %q", name)) {
				t.Errorf("output should name the profile it wrote:\n%s", out.String())
			}
		})
	}
}

// TestConfigureCommandKeepsProfileNameWithPublicSupermarketAndServer passes
// both a server URL and the public Supermarket as flags: still a server
// profile, still the default name.
func TestConfigureCommandKeepsProfileNameWithPublicSupermarketAndServer(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	_, _, err := runRoot(t, "config", "create", "--config", cfgPath,
		"--server-url", "https://cinc.example.test/organizations/acme",
		"--supermarket-site", "https://supermarket.chef.io",
		"--client-name", "tim", "--client-key", "/keys/tim.pem")
	if err != nil {
		t.Fatalf("cinc config create: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Profiles["default"]; p.Org != "acme" || p.SupermarketSite != "https://supermarket.chef.io" {
		t.Errorf("profiles = %v, want [default] with both endpoints", cfg.Profiles)
	}
}

// TestConfigureCommandRejectsServerURLWithoutOrg leaves /organizations/<org>
// off --server-url, the likeliest typo there is. It used to be filed away
// as supermarket_site, writing a profile no server command could use.
func TestConfigureCommandRejectsServerURLWithoutOrg(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	_, _, err := runRoot(t, "config", "create", "--config", cfgPath,
		"--server-url", "https://cinc.example.test",
		"--client-name", "tim", "--client-key", "/keys/tim.pem")
	if err == nil {
		b, _ := os.ReadFile(cfgPath)
		t.Fatalf("config create accepted a server URL without an org and wrote:\n%s", b)
	}
	for _, want := range []string{"https://cinc.example.test", "/organizations/", "--supermarket-site"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err, want)
		}
	}
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		t.Errorf("a rejected config create wrote %s", cfgPath)
	}
}

// TestFirstRunConfigureExpandsTildePaths answers the location and key
// prompts with ~ paths. They mean the home directory, as they do for
// `config create`, not a directory named ~ under the working directory.
func TestFirstRunConfigureExpandsTildePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	out := new(bytes.Buffer)
	c := fakeCmd("", "", strings.Join([]string{
		"~/work/credentials", "", "", "tim", "~/keys/tim.pem", "https://cinc.example.test", "acme", "",
	}, "\n")+"\n", new(bytes.Buffer))
	c.SetOut(out)
	if err := realRunFirstRunConfigure(c, filepath.Join(home, ".cinc", "credentials")); err != nil {
		t.Fatalf("first-run configure: %v", err)
	}

	cfgPath := filepath.Join(home, "work", "credentials")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load %s: %v", cfgPath, err)
	}
	if got, want := cfg.Profiles["default"].KeyPath, filepath.Join(home, "keys", "tim.pem"); got != want {
		t.Errorf("client_key = %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), "to "+cfgPath) {
		t.Errorf("expected the expanded path in the success message, got:\n%s", out.String())
	}
	if _, err := os.Stat("~"); err == nil {
		t.Error("first-run configure created a directory named ~")
	}
}
