package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
