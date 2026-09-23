package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const sampleConfig = `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
cinc_server_url = "https://cinc.example.com/organizations/acme"

[supermarket]
client_name      = "tim"
client_key       = "/keys/supermarket.pem"
supermarket_site = "https://supermarket.chef.io"

[staging]
client_name     = "tim"
client_key      = "/keys/staging.pem"
cinc_server_url = "https://staging.example.com/organizations/acme-staging"
ssl_verify_mode = ":verify_none"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadParsesProfiles(t *testing.T) {
	cfg, err := Load(writeConfig(t, sampleConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Profiles) != 3 {
		t.Fatalf("got %d profiles, want 3", len(cfg.Profiles))
	}
	prod := cfg.Profiles["default"]
	if prod.ServerURL != "https://cinc.example.com" || prod.Org != "acme" ||
		prod.ClientName != "tim" || prod.KeyPath != "/keys/tim.pem" {
		t.Errorf("default profile parsed incorrectly: %+v", prod)
	}
	staging := cfg.Profiles["staging"]
	if staging.SSLVerifyMode != ":verify_none" {
		t.Errorf("staging ssl_verify_mode = %q, want %q", staging.SSLVerifyMode, ":verify_none")
	}
	supermarket := cfg.Profiles["supermarket"]
	if supermarket.SupermarketSite != "https://supermarket.chef.io" || supermarket.KeyPath != "/keys/supermarket.pem" {
		t.Errorf("supermarket profile parsed incorrectly: %+v", supermarket)
	}
}

func TestLoadParsesSecretFile(t *testing.T) {
	const cfg = `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
cinc_server_url = "https://cinc.example.com/organizations/acme"
secret_file     = "/keys/encrypted_data_bag_secret"
`
	c, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.Profiles["default"].SecretFile; got != "/keys/encrypted_data_bag_secret" {
		t.Errorf("default secret_file = %q, want %q", got, "/keys/encrypted_data_bag_secret")
	}
}

func TestWriteProfileRoundTripsSecretFile(t *testing.T) {
	path := writeConfig(t, "")
	if err := WriteProfile(path, "worker", Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "worker",
		KeyPath:    "/keys/worker.pem",
		SecretFile: "/keys/secret",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.Profiles["worker"].SecretFile; got != "/keys/secret" {
		t.Errorf("round-tripped secret_file = %q, want %q", got, "/keys/secret")
	}
}

func TestLoadParsesSupermarketCredentials(t *testing.T) {
	const cfg = `
[default]
client_name             = "tim"
client_key              = "/keys/tim.pem"
cinc_server_url         = "https://cinc.example.com/organizations/acme"
supermarket_client_name = "tim-public"
supermarket_key         = "/keys/supermarket.pem"
`
	c, err := Load(writeConfig(t, cfg))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := c.Profiles["default"]
	if p.SupermarketClientName != "tim-public" || p.SupermarketKey != "/keys/supermarket.pem" {
		t.Fatalf("supermarket creds = %q/%q, want tim-public//keys/supermarket.pem", p.SupermarketClientName, p.SupermarketKey)
	}
}

func TestWriteProfileRoundTripsSupermarketCredentials(t *testing.T) {
	path := writeConfig(t, "")
	if err := WriteProfile(path, "worker", Profile{
		ServerURL:             "https://cinc.example.com",
		Org:                   "acme",
		ClientName:            "worker",
		KeyPath:               "/keys/worker.pem",
		SupermarketClientName: "worker-public",
		SupermarketKey:        "/keys/supermarket.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := c.Profiles["worker"]
	if p.SupermarketClientName != "worker-public" || p.SupermarketKey != "/keys/supermarket.pem" {
		t.Fatalf("round-tripped supermarket creds = %q/%q, want worker-public//keys/supermarket.pem", p.SupermarketClientName, p.SupermarketKey)
	}
}

func TestSupermarketIdentityFallsBackPerField(t *testing.T) {
	cases := []struct {
		name        string
		profile     Profile
		wantName    string
		wantKey     string
		wantInvalid bool
	}{
		{
			name:     "no override falls back to client identity",
			profile:  Profile{ClientName: "tim", KeyPath: "/keys/tim.pem"},
			wantName: "tim", wantKey: "/keys/tim.pem",
		},
		{
			name: "both overrides win",
			profile: Profile{
				ClientName: "tim", KeyPath: "/keys/tim.pem",
				SupermarketClientName: "tim-public", SupermarketKey: "/keys/super.pem",
			},
			wantName: "tim-public", wantKey: "/keys/super.pem",
		},
		{
			name: "only username override, key falls back",
			profile: Profile{
				ClientName: "tim", KeyPath: "/keys/tim.pem",
				SupermarketClientName: "tim-public",
			},
			wantName: "tim-public", wantKey: "/keys/tim.pem",
		},
		{
			name: "only key override, username falls back",
			profile: Profile{
				ClientName: "tim", KeyPath: "/keys/tim.pem",
				SupermarketKey: "/keys/super.pem",
			},
			wantName: "tim", wantKey: "/keys/super.pem",
		},
		{
			name:        "neither base nor override identity is invalid",
			profile:     Profile{},
			wantInvalid: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotKey := tc.profile.SupermarketIdentity()
			if !tc.wantInvalid && (gotName != tc.wantName || gotKey != tc.wantKey) {
				t.Fatalf("SupermarketIdentity() = %q/%q, want %q/%q", gotName, gotKey, tc.wantName, tc.wantKey)
			}
			err := tc.profile.ValidateSupermarketIdentity()
			if tc.wantInvalid && err == nil {
				t.Fatal("ValidateSupermarketIdentity() = nil, want error")
			}
			if !tc.wantInvalid && err != nil {
				t.Fatalf("ValidateSupermarketIdentity() = %v, want nil", err)
			}
		})
	}
}

func TestConfigProfileResolvesDefaultWhenNameEmpty(t *testing.T) {
	t.Setenv("CHEF_PROFILE", "")
	cfg, _ := Load(writeConfig(t, sampleConfig))

	p, err := cfg.Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if p.Org != "acme" {
		t.Errorf("default profile Org = %q, want %q", p.Org, "acme")
	}
}

func TestConfigProfileHonorsChefProfileEnv(t *testing.T) {
	t.Setenv("CINC_PROFILE", "")
	t.Setenv("CHEF_PROFILE", "staging")
	cfg, _ := Load(writeConfig(t, sampleConfig))

	p, err := cfg.Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if p.Org != "acme-staging" {
		t.Errorf("env-selected profile Org = %q, want %q", p.Org, "acme-staging")
	}
}

func TestConfigProfileHonorsCincProfileEnv(t *testing.T) {
	t.Setenv("CINC_PROFILE", "staging")
	t.Setenv("CHEF_PROFILE", "")
	cfg, _ := Load(writeConfig(t, sampleConfig))

	p, err := cfg.Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if p.Org != "acme-staging" {
		t.Errorf("CINC_PROFILE-selected profile Org = %q, want %q", p.Org, "acme-staging")
	}
}

func TestConfigProfileCincEnvWinsOverChefEnv(t *testing.T) {
	t.Setenv("CINC_PROFILE", "staging")
	t.Setenv("CHEF_PROFILE", "default")
	cfg, _ := Load(writeConfig(t, sampleConfig))

	p, err := cfg.Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if p.Org != "acme-staging" {
		t.Errorf("with both env vars set, expected CINC_PROFILE to win; got Org = %q", p.Org)
	}
}

func TestConfigProfileResolvesByName(t *testing.T) {
	cfg, _ := Load(writeConfig(t, sampleConfig))

	p, err := cfg.Profile("staging")
	if err != nil {
		t.Fatalf("Profile(staging): %v", err)
	}
	if p.Org != "acme-staging" {
		t.Errorf("staging profile Org = %q, want %q", p.Org, "acme-staging")
	}
}

func TestConfigProfileUnknownNameErrors(t *testing.T) {
	cfg, _ := Load(writeConfig(t, sampleConfig))

	if _, err := cfg.Profile("does-not-exist"); err == nil {
		t.Error("expected an error resolving an unknown profile")
	}
}

func TestLoadAcceptsChefServerURLKey(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
chef_server_url = "https://chef.example.com/organizations/acme"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Profiles["default"]
	if p.ServerURL != "https://chef.example.com" || p.Org != "acme" {
		t.Errorf("chef_server_url (compat form) parsed incorrectly: %+v", p)
	}
}

func TestLoadCincServerURLWinsOverChefServerURL(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
chef_server_url = "https://chef.example.com/organizations/chef-org"
cinc_server_url = "https://cinc.example.com/organizations/cinc-org"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Profiles["default"]
	if p.ServerURL != "https://cinc.example.com" || p.Org != "cinc-org" {
		t.Errorf("with both keys set, expected cinc_server_url to win; got %+v", p)
	}
}

func TestLoadKeepsServerURLWithoutOrganizationSegmentForValidation(t *testing.T) {
	// A malformed server URL no longer fails the whole load: the raw value is
	// preserved (with ServerURL/Org left empty) so `cinc config validate` can
	// report the precise problem per profile. Validate still surfaces it for
	// callers that need a usable connection.
	bad := `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
cinc_server_url = "https://cinc.example.com"
`
	cfg, err := Load(writeConfig(t, bad))
	if err != nil {
		t.Fatalf("Load should tolerate a malformed server URL, got %v", err)
	}
	p := cfg.Profiles["default"]
	if p.RawServerURL != "https://cinc.example.com" {
		t.Errorf("RawServerURL = %q, want the raw URL preserved", p.RawServerURL)
	}
	if p.ServerURL != "" || p.Org != "" {
		t.Errorf("ServerURL/Org = %q/%q, want both empty for a malformed URL", p.ServerURL, p.Org)
	}
	if err := p.Validate(); err == nil ||
		!strings.Contains(err.Error(), "organizations") {
		t.Errorf("Validate() = %v, want an /organizations/<org> error", err)
	}
}

func TestNewProfileParsesConfigureValues(t *testing.T) {
	p, err := NewProfile("https://cinc.example.com/organizations/acme", "worker", "/keys/worker.pem", ":verify_peer", "https://supermarket.example.test")
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if p.ServerURL != "https://cinc.example.com" || p.Org != "acme" || p.ClientName != "worker" ||
		p.KeyPath != "/keys/worker.pem" || p.SSLVerifyMode != ":verify_peer" || p.SupermarketSite != "https://supermarket.example.test" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestNewProfileTreatsServerURLWithoutOrganizationAsSupermarketSite(t *testing.T) {
	p, err := NewProfile("https://supermarket.chef.io", "worker", "/keys/worker.pem", "", "")
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if p.ServerURL != "" || p.Org != "" || p.SupermarketSite != "https://supermarket.chef.io" {
		t.Fatalf("profile = %+v, want supermarket-only profile", p)
	}
}

func TestWriteProfileCreatesCredentialsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cinc", "credentials")

	err := WriteProfile(path, "worker", Profile{
		ServerURL:       "https://cinc.example.com",
		Org:             "acme",
		SupermarketSite: "https://supermarket.example.test",
		ClientName:      "worker",
		KeyPath:         "/keys/worker.pem",
	})
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials mode = %o, want 0600", perm)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load written credentials: %v", err)
	}
	p := cfg.Profiles["worker"]
	if p.ServerURL != "https://cinc.example.com" || p.Org != "acme" || p.SupermarketSite != "https://supermarket.example.test" || p.ClientName != "worker" || p.KeyPath != "/keys/worker.pem" {
		t.Fatalf("written profile = %+v", p)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "cinc_server_url") || !strings.Contains(string(data), "supermarket_site") {
		t.Fatalf("credentials = %s, want cinc_server_url and supermarket_site", data)
	}
	if strings.Contains(string(data), "chef_server_url") {
		t.Fatalf("credentials = %s, want the cinc-canonical key, not chef_server_url", data)
	}
}

func TestWriteProfileRoundTripsAllKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cinc", "credentials")

	want := Profile{
		ServerURL:             "https://cinc.example.com",
		Org:                   "acme",
		RawServerURL:          "https://cinc.example.com/organizations/acme",
		SupermarketSite:       "https://supermarket.example.test",
		ClientName:            "worker",
		KeyPath:               "/keys/worker.pem",
		SSLVerifyMode:         ":verify_none",
		SecretFile:            "/keys/encrypted_data_bag_secret",
		SupermarketClientName: "worker-public",
		SupermarketKey:        "/keys/supermarket.pem",
	}
	if err := WriteProfile(path, "worker", want); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load written credentials: %v", err)
	}
	got := cfg.Profiles["worker"]
	if got != want {
		t.Fatalf("round-tripped profile = %+v, want %+v", got, want)
	}
}

func TestWriteProfileAllowsSupermarketOnlyProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cinc", "credentials")

	err := WriteProfile(path, "supermarket", Profile{
		SupermarketSite: "https://supermarket.chef.io",
		ClientName:      "worker",
		KeyPath:         "/keys/worker.pem",
	})
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load written credentials: %v", err)
	}
	p := cfg.Profiles["supermarket"]
	if p.ServerURL != "" || p.Org != "" || p.SupermarketSite != "https://supermarket.chef.io" {
		t.Fatalf("written profile = %+v", p)
	}
}

func TestWriteProfilePreservesExistingProfiles(t *testing.T) {
	path := writeConfig(t, sampleConfig)

	err := WriteProfile(path, "worker", Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "worker",
		KeyPath:    "/keys/worker.pem",
	})
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load written credentials: %v", err)
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Fatal("default profile was not preserved")
	}
	if _, ok := cfg.Profiles["staging"]; !ok {
		t.Fatal("staging profile was not preserved")
	}
	if cfg.Profiles["worker"].ClientName != "worker" {
		t.Fatalf("worker profile = %+v", cfg.Profiles["worker"])
	}
}

// TestWriteProfilePreservesUnknownKeys guards the compatibility promise that
// cinc can share a credentials file with knife. knife stores keys cinc has no
// model for (validation_client_name, validation_key, node_name, ...), and
// rewriting the file to add or update one profile must not quietly drop them
// from that profile or from any other.
func TestWriteProfilePreservesUnknownKeys(t *testing.T) {
	path := writeConfig(t, `
[default]
chef_server_url        = "https://chef.example.com/organizations/acme"
client_name            = "tim"
client_key             = "/keys/tim.pem"
validation_client_name = "acme-validator"
validation_key         = "/keys/acme-validator.pem"
node_name              = "tim"
cookbook_path          = "/repo/cookbooks"

[staging]
chef_server_url        = "https://staging.example.com/organizations/acme"
client_name            = "tim"
client_key             = "/keys/tim.pem"
validation_client_name = "staging-validator"
`)

	// Add a brand new profile, the way `cinc config create` does.
	if err := WriteProfile(path, "prod", Profile{
		ServerURL:  "https://prod.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	// And update an existing one, which is the more dangerous case: its own
	// unmodelled keys have to survive being rewritten.
	if err := WriteProfile(path, "default", Profile{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/new.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		`validation_client_name = "acme-validator"`,
		`validation_key = "/keys/acme-validator.pem"`,
		`node_name = "tim"`,
		`cookbook_path = "/repo/cookbooks"`,
		`validation_client_name = "staging-validator"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("WriteProfile dropped %s\nfile:\n%s", want, body)
		}
	}
	// The managed keys still round-trip.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load written credentials: %v", err)
	}
	if got := cfg.Profiles["default"].KeyPath; got != "/keys/new.pem" {
		t.Errorf("default client_key = %q, want the updated /keys/new.pem", got)
	}
	if got := cfg.Profiles["prod"].Org; got != "acme" {
		t.Errorf("prod org = %q, want acme", got)
	}
}

func TestWriteProfileRequiresName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")

	err := WriteProfile(path, "", Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	})
	if err == nil {
		t.Fatal("expected name-required error")
	}
	if !strings.Contains(err.Error(), "profile name is required") {
		t.Fatalf("error = %q, want name-required message", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credentials file should not have been created, stat = %v", err)
	}
}

func TestWriteProfileRejectsIncompleteIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")

	err := WriteProfile(path, "worker", Profile{
		ServerURL: "https://cinc.example.com",
		Org:       "acme",
		KeyPath:   "/keys/worker.pem",
	})
	if err == nil {
		t.Fatal("expected validate-identity error")
	}
	if !strings.Contains(err.Error(), "client_name") {
		t.Fatalf("error = %q, want client_name in message", err)
	}
}

func TestWriteProfileRejectsExistingMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("[broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteProfile(path, "worker", Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	})
	if err == nil {
		t.Fatal("expected parse error on malformed existing file")
	}
}

func TestProfileValidate(t *testing.T) {
	complete := Profile{ServerURL: "u", Org: "o", ClientName: "c", KeyPath: "k"}
	if err := complete.Validate(); err != nil {
		t.Errorf("complete profile should validate, got: %v", err)
	}

	incomplete := []Profile{
		{Org: "o", ClientName: "c", KeyPath: "k"},
		{ServerURL: "u", ClientName: "c", KeyPath: "k"},
		{ServerURL: "u", Org: "o", KeyPath: "k"},
		{ServerURL: "u", Org: "o", ClientName: "c"},
	}
	for _, p := range incomplete {
		if err := p.Validate(); err == nil {
			t.Errorf("incomplete profile %+v should fail validation", p)
		}
	}
}

func TestProfileValidateIdentity(t *testing.T) {
	complete := Profile{ClientName: "c", KeyPath: "k"}
	if err := complete.ValidateIdentity(); err != nil {
		t.Errorf("complete identity should validate, got: %v", err)
	}

	incomplete := []Profile{
		{KeyPath: "k"},
		{ClientName: "c"},
	}
	for _, p := range incomplete {
		if err := p.ValidateIdentity(); err == nil {
			t.Errorf("incomplete identity %+v should fail validation", p)
		}
	}
}

func TestConfigValidateReportsProfileIssues(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"broken": {
			SSLVerifyMode: "nope",
		},
		"supermarket": {
			ClientName:      "tim",
			KeyPath:         "/keys/tim.pem",
			SupermarketSite: "https://supermarket.chef.io",
		},
	}}

	issues := cfg.Validate()
	for _, want := range []string{"client_name", "client_key", "endpoint", "ssl_verify_mode"} {
		if !hasValidationIssue(issues, "broken", want) {
			t.Fatalf("issues = %#v, want field %q", issues, want)
		}
	}
	if hasValidationIssue(issues, "supermarket", "endpoint") {
		t.Fatalf("issues = %#v, supermarket-only profile should validate", issues)
	}
}

// A profile whose server URL is present but malformed (missing the
// /organizations/<org> segment) should report the malformed URL, not the
// misleading "you configured no endpoint" message.
func TestConfigValidateReportsMalformedServerURL(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"bad": {
			ClientName:   "tim",
			KeyPath:      "/keys/tim.pem",
			RawServerURL: "https://cinc.example.com", // no /organizations/<org>
		},
	}}

	issues := cfg.Validate()
	if !hasValidationIssue(issues, "bad", "cinc_server_url") {
		t.Fatalf("issues = %#v, want a malformed server URL issue", issues)
	}
	if hasValidationIssue(issues, "bad", "endpoint") {
		t.Fatalf("issues = %#v, a malformed URL must not be reported as a missing endpoint", issues)
	}
}

func TestConfigValidateRejectsEmptyConfig(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{}}

	issues := cfg.Validate()
	if len(issues) != 1 || issues[0].Field != "profiles" {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestDefaultPathPointsAtCincCredentialsFile(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(".cinc", "credentials")
	if !strings.HasSuffix(p, want) {
		t.Errorf("DefaultPath = %q, want it to end with %q", p, want)
	}
}

func hasValidationIssue(issues []ValidationIssue, profile, field string) bool {
	for _, issue := range issues {
		if issue.Profile == profile && issue.Field == field {
			return true
		}
	}
	return false
}

func TestUpdateProfileKeepsKeysTheMutatorDoesNotTouch(t *testing.T) {
	path := writeConfig(t, `
[default]
cinc_server_url         = "https://old.example.com/organizations/acme"
client_name             = "tim"
client_key              = "/keys/tim.pem"
secret_file             = "/keys/databag_secret"
supermarket_client_name = "tim-public"
supermarket_key         = "/keys/supermarket.pem"
node_name               = "tim"
`)

	// A caller that only knows about the connection fields, which is
	// every caller `config create` has.
	err := UpdateProfile(path, "default", func(p *Profile) error {
		p.ServerURL = "https://new.example.com"
		p.Org = "acme"
		p.ClientName = "tim"
		p.KeyPath = "/keys/rotated.pem"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Profiles["default"]
	if got.KeyPath != "/keys/rotated.pem" {
		t.Errorf("client_key not updated: %q", got.KeyPath)
	}
	if got.ServerURL != "https://new.example.com" {
		t.Errorf("server URL not updated: %q", got.ServerURL)
	}
	for name, val := range map[string]string{
		"secret_file":             got.SecretFile,
		"supermarket_client_name": got.SupermarketClientName,
		"supermarket_key":         got.SupermarketKey,
	} {
		if val == "" {
			t.Errorf("%s was wiped by an update that never mentioned it", name)
		}
	}
	// Keys cinc does not model still ride along via WriteProfile.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `node_name = "tim"`) {
		t.Errorf("unmodelled key dropped\nfile:\n%s", raw)
	}
}

func TestUpdateProfileCreatesAMissingProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cinc", "credentials")

	err := UpdateProfile(path, "default", func(p *Profile) error {
		p.ServerURL = "https://cinc.example.com"
		p.Org = "acme"
		p.ClientName = "tim"
		p.KeyPath = "/keys/tim.pem"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Profiles["default"].Org; got != "acme" {
		t.Errorf("org = %q, want acme", got)
	}
}

func TestUpdateProfilePropagatesMutatorError(t *testing.T) {
	path := writeConfig(t, `
[default]
cinc_server_url = "https://cinc.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("boom")
	if err := UpdateProfile(path, "default", func(*Profile) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("UpdateProfile error = %v, want %v", err, wantErr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("file rewritten despite a mutator error\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWriteProfileKeepsChefServerURLCurrentOnSharedProfiles(t *testing.T) {
	path := writeConfig(t, `
[default]
chef_server_url = "https://old.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
node_name       = "tim"
`)

	if err := WriteProfile(path, "default", Profile{
		ServerURL:  "https://new.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	want := "https://new.example.com/organizations/acme"
	// cinc reads cinc_server_url; knife on the same file reads
	// chef_server_url and has no notion of the cinc key. Both must point
	// at the new server.
	for _, line := range []string{
		`cinc_server_url = "` + want + `"`,
		`chef_server_url = "` + want + `"`,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("missing %s\nfile:\n%s", line, body)
		}
	}
	if strings.Contains(body, "old.example.com") {
		t.Errorf("stale server URL left behind\nfile:\n%s", body)
	}
}

func TestWriteProfileDoesNotAddChefServerURLToFreshProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cinc", "credentials")

	if err := WriteProfile(path, "default", Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "chef_server_url") {
		t.Errorf("a profile that never had chef_server_url should stay cinc-canonical\nfile:\n%s", raw)
	}
}

// The mirror copies presence, not value: a rewrite with no server URL to
// offer must leave the shared key alone rather than empty it, which the
// managed-key loop would turn into a delete.
func TestWriteProfileKeepsChefServerURLWhenTheRewriteHasNoServerURL(t *testing.T) {
	path := writeConfig(t, `
[default]
chef_server_url = "https://chef.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
node_name       = "tim"
`)

	// A Supermarket-only rewrite of the same profile: no server URL.
	if err := WriteProfile(path, "default", Profile{
		SupermarketSite: "https://supermarket.example.test",
		ClientName:      "tim",
		KeyPath:         "/keys/tim.pem",
	}); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `chef_server_url = "https://chef.example.com/organizations/acme"`) {
		t.Errorf("knife's server URL was dropped by a rewrite that had none to offer\nfile:\n%s", raw)
	}
}

// TestWriteProfileWithExtrasWritesUnmanagedKeysOnly checks the extra keys are
// written verbatim, while a managed key in extra never overrides the Profile.
func TestWriteProfileWithExtrasWritesUnmanagedKeysOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	p := Profile{
		ServerURL:  "https://cinc.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "/keys/tim.pem",
	}
	extra := map[string]any{
		"node_name":       "tim-workstation",
		"knife":           map[string]any{"ssh_user": "ubuntu"},
		"client_name":     "not-tim",
		"chef_server_url": "https://old.example.com/organizations/old",
	}
	if err := WriteProfileWithExtras(path, "default", p, extra); err != nil {
		t.Fatalf("WriteProfileWithExtras: %v", err)
	}
	var got map[string]map[string]any
	if _, err := toml.DecodeFile(path, &got); err != nil {
		t.Fatal(err)
	}
	def := got["default"]
	if def["node_name"] != "tim-workstation" {
		t.Errorf("node_name = %v, want it copied", def["node_name"])
	}
	if knife, _ := def["knife"].(map[string]any); knife["ssh_user"] != "ubuntu" {
		t.Errorf("knife = %v, want the nested table copied", def["knife"])
	}
	if def["client_name"] != "tim" {
		t.Errorf("client_name = %v, want the Profile's value, not extra's", def["client_name"])
	}
	if _, ok := def["chef_server_url"]; ok {
		t.Errorf("chef_server_url from extra should be ignored: %v", def)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Profiles["default"]; got.ServerURL != p.ServerURL || got.Org != "acme" {
		t.Errorf("reloaded profile = %+v", got)
	}
}
