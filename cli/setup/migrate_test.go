package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func writeChefCredentials(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateChefCopiesAllProfiles(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url = "https://chef.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"

[staging]
chef_server_url = "https://staging.example.com/organizations/acme-staging"
client_name     = "tim"
client_key      = "/keys/staging.pem"
ssl_verify_mode = ":verify_none"
`)
	cincPath := filepath.Join(t.TempDir(), ".cinc", "credentials")

	n, err := MigrateChef(chefPath, cincPath)
	if err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	if n != 2 {
		t.Errorf("migrated profile count = %d, want 2", n)
	}
	body := readFile(t, cincPath)
	if !strings.Contains(body, "[default]") || !strings.Contains(body, "[staging]") {
		t.Errorf("expected both profile sections in:\n%s", body)
	}
	if !strings.Contains(body, "chef.example.com/organizations/acme") {
		t.Errorf("missing server URL in output:\n%s", body)
	}
	if !strings.Contains(body, `ssl_verify_mode = ":verify_none"`) {
		t.Errorf("ssl_verify_mode should pass through:\n%s", body)
	}
}

func TestMigrateChefPrefersCincURLWhenBothPresent(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url = "https://chef.example.com/organizations/chef-org"
cinc_server_url = "https://cinc.example.com/organizations/cinc-org"
client_name     = "tim"
client_key      = "/keys/tim.pem"
`)
	cincPath := filepath.Join(t.TempDir(), "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	body := readFile(t, cincPath)
	if !strings.Contains(body, "cinc.example.com") {
		t.Errorf("cinc URL should win, got:\n%s", body)
	}
	if strings.Contains(body, "chef.example.com") {
		t.Errorf("chef URL should have been dropped, got:\n%s", body)
	}
}

func TestMigrateChefCarriesSupermarketSite(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[supermarket]
supermarket_site = "https://supermarket.example.com"
client_name      = "tim"
client_key       = "/keys/tim.pem"
`)
	cincPath := filepath.Join(t.TempDir(), "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	body := readFile(t, cincPath)
	if !strings.Contains(body, "supermarket.example.com") {
		t.Errorf("supermarket_site should pass through:\n%s", body)
	}
}

func TestMigrateChefModernizesURLAndCarriesAllKeys(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url         = "https://chef.example.com/organizations/acme"
client_name             = "tim"
client_key              = "/keys/tim.pem"
ssl_verify_mode         = ":verify_none"
secret_file             = "/keys/encrypted_data_bag_secret"
supermarket_client_name = "tim-public"
supermarket_key         = "/keys/supermarket.pem"
`)
	cincPath := filepath.Join(t.TempDir(), ".cinc", "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	body := readFile(t, cincPath)

	// The legacy chef_server_url is modernized to the cinc-canonical key.
	if !strings.Contains(body, "cinc_server_url") {
		t.Errorf("migrated file should write cinc_server_url, got:\n%s", body)
	}
	if strings.Contains(body, "chef_server_url") {
		t.Errorf("migrated file should not keep the legacy chef_server_url, got:\n%s", body)
	}

	// Nothing the Chef file held gets dropped.
	for _, want := range []string{
		"/keys/encrypted_data_bag_secret", // secret_file
		"tim-public",                      // supermarket_client_name
		"/keys/supermarket.pem",           // supermarket_key
		`ssl_verify_mode = ":verify_none"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("migrated file should carry %q, got:\n%s", want, body)
		}
	}
}

func TestMigrateChefCarriesTrustedCertsDir(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url   = "https://chef.example.com/organizations/acme"
client_name       = "tim"
client_key        = "/keys/tim.pem"
trusted_certs_dir = "~/.chef/trusted_certs"
`)
	cincPath := filepath.Join(t.TempDir(), ".cinc", "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	// Carried verbatim: the ~ stays unexpanded.
	if body := readFile(t, cincPath); !strings.Contains(body, `trusted_certs_dir = "~/.chef/trusted_certs"`) {
		t.Errorf("migrated file should carry trusted_certs_dir, got:\n%s", body)
	}
}

func TestMigrateChefReturnsErrorOnUnparseableFile(t *testing.T) {
	chefPath := writeChefCredentials(t, "this is not = valid = toml [[")
	cincPath := filepath.Join(t.TempDir(), "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err == nil {
		t.Error("expected an error parsing garbage TOML")
	}
	if _, err := os.Stat(cincPath); err == nil {
		t.Error("output file should not exist when migration fails")
	}
}

func TestMigrateChefFailsWhenProfileMissingClientName(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url = "https://chef.example.com/organizations/acme"
client_key      = "/keys/tim.pem"
`)
	cincPath := filepath.Join(t.TempDir(), "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err == nil {
		t.Error("expected an error when client_name is missing")
	}
}

// TestMigrateChefWritesNothingWhenAProfileIsUnmigratable pins migration as
// all-or-nothing. A half-written credentials file is worse than none at all:
// the first-run flow only offers to migrate when ~/.cinc/credentials is
// absent, so a partial file leaves the user stuck with some profiles missing
// and no prompt to finish the job.
func TestMigrateChefWritesNothingWhenAProfileIsUnmigratable(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url = "https://chef.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"

[aaa_broken]
chef_server_url = "https://other.example.com/organizations/acme"
ssl_verify_mode = ":verify_none"

[zzz_fine]
chef_server_url = "https://z.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
`)
	cincPath := filepath.Join(t.TempDir(), ".cinc", "credentials")

	n, err := MigrateChef(chefPath, cincPath)
	if err == nil {
		t.Fatal("expected an error for the profile with no client_name")
	}
	if n != 0 {
		t.Errorf("migrated count = %d, want 0 when migration fails", n)
	}
	if !strings.Contains(err.Error(), "aaa_broken") {
		t.Errorf("error = %v, want it to name the profile at fault", err)
	}
	if _, statErr := os.Stat(cincPath); statErr == nil {
		t.Errorf("migration failed but left a partial file at %s:\n%s", cincPath, readFile(t, cincPath))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestMigrateChefKeepsKeysCincDoesNotModel covers a knife credentials file:
// it holds knife-only settings (node_name, the validator, a nested knife
// table) that cinc never reads. docs/migrating-from-chef.md promises every
// key carries over, so they are copied verbatim rather than dropped.
func TestMigrateChefKeepsKeysCincDoesNotModel(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url        = "https://chef.example.com/organizations/acme"
client_name            = "tim"
client_key             = "/keys/tim.pem"
node_name              = "tim-workstation"
validation_client_name = "acme-validator"
validation_key         = "/keys/acme-validator.pem"
knife                  = { ssh_user = "ubuntu", ssh_port = 2222 }

[staging]
chef_server_url = "https://staging.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/staging.pem"
cookbook_path   = ["/src/cookbooks", "/src/site-cookbooks"]
`)
	cincPath := filepath.Join(t.TempDir(), ".cinc", "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	var got map[string]map[string]any
	if _, err := toml.DecodeFile(cincPath, &got); err != nil {
		t.Fatalf("decode migrated file: %v", err)
	}
	def := got["default"]
	for key, want := range map[string]any{
		"node_name":              "tim-workstation",
		"validation_client_name": "acme-validator",
		"validation_key":         "/keys/acme-validator.pem",
	} {
		if def[key] != want {
			t.Errorf("default.%s = %v, want %v (file: %v)", key, def[key], want, def)
		}
	}
	knife, _ := def["knife"].(map[string]any)
	if knife["ssh_user"] != "ubuntu" || knife["ssh_port"] != int64(2222) {
		t.Errorf("default.knife = %v, want ssh_user ubuntu and ssh_port 2222", def["knife"])
	}
	if paths, _ := got["staging"]["cookbook_path"].([]any); len(paths) != 2 {
		t.Errorf("staging.cookbook_path = %v, want both paths", got["staging"]["cookbook_path"])
	}
	// The one key that is renamed, not copied.
	if _, ok := def["chef_server_url"]; ok {
		t.Errorf("chef_server_url should become cinc_server_url, got %v", def)
	}
	if def["cinc_server_url"] != "https://chef.example.com/organizations/acme" {
		t.Errorf("default.cinc_server_url = %v", def["cinc_server_url"])
	}
}

// TestMigrateChefKeepsOrglessServerURL migrates the profile chef-zero's
// docs give knife users: a server URL with no /organizations/<org>. It is
// a server, so it stays the server URL, never becomes a Supermarket site,
// and is kept as written so config validate can say what it lacks.
func TestMigrateChefKeepsOrglessServerURL(t *testing.T) {
	chefPath := writeChefCredentials(t, `
[default]
chef_server_url = "http://127.0.0.1:8889"
client_name     = "tim"
client_key      = "/keys/tim.pem"
`)
	cincPath := filepath.Join(t.TempDir(), "credentials")

	if _, err := MigrateChef(chefPath, cincPath); err != nil {
		t.Fatalf("MigrateChef: %v", err)
	}
	var got map[string]map[string]any
	if _, err := toml.DecodeFile(cincPath, &got); err != nil {
		t.Fatal(err)
	}
	def := got["default"]
	if def["cinc_server_url"] != "http://127.0.0.1:8889" {
		t.Errorf("default.cinc_server_url = %v, want the knife URL as written", def["cinc_server_url"])
	}
	if site, ok := def["supermarket_site"]; ok {
		t.Errorf("a server URL was migrated as supermarket_site = %v", site)
	}
}
