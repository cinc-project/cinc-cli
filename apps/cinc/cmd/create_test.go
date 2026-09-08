package cmd

import (
	"bytes"
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
