package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// assertMessage fails the test unless err's message mentions every string in
// want and reads as prose: no em dashes and no Go-style "config:" prefix.
func assertMessage(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	msg := err.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error should mention %q, got:\n%s", w, msg)
		}
	}
	if strings.Contains(msg, "—") {
		t.Errorf("error should not use an em dash, got:\n%s", msg)
	}
	if strings.HasPrefix(msg, "config:") || strings.HasPrefix(msg, "toml:") {
		t.Errorf("error should start as a sentence, not a package prefix, got:\n%s", msg)
	}
}

// loadProfile writes content to a credentials file, loads it, and returns the
// named profile with the file's path.
func loadProfile(t *testing.T, content, name string) (Profile, string) {
	t.Helper()
	path := writeConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := cfg.Profile(name)
	if err != nil {
		t.Fatalf("Profile(%q): %v", name, err)
	}
	return p, path
}

func TestValidateExplainsMissingClientName(t *testing.T) {
	p, path := loadProfile(t, `
[default]
client_key      = "/keys/tim.pem"
cinc_server_url = "https://cinc.example.com/organizations/acme"
`, "default")

	assertMessage(t, p.Validate(),
		`"default" profile`, path, "client_name", `client_name = "`, "config create")
}

func TestValidateExplainsMissingClientKey(t *testing.T) {
	p, path := loadProfile(t, `
[default]
client_name     = "tim"
cinc_server_url = "https://cinc.example.com/organizations/acme"
`, "default")

	assertMessage(t, p.Validate(),
		`"default" profile`, path, "client_key", "private key", `client_key = "`, "tim")
}

func TestValidateExplainsServerURLWithoutOrganization(t *testing.T) {
	p, path := loadProfile(t, `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
cinc_server_url = "https://cinc.example.com"
`, "default")

	assertMessage(t, p.Validate(),
		`"default" profile`, path, "https://cinc.example.com", "organization",
		`cinc_server_url = "https://cinc.example.com/organizations/`)
}

// A knife-shared file keeps its server under chef_server_url, so the fix
// should name the key the user actually has.
func TestValidateNamesChefServerURLKeyWhenThatIsWhatIsSet(t *testing.T) {
	p, _ := loadProfile(t, `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
chef_server_url = "https://cinc.example.com/"
`, "default")

	assertMessage(t, p.Validate(), `chef_server_url = "https://cinc.example.com/organizations/`)
}

func TestValidateExplainsUnparseableServerURL(t *testing.T) {
	p, path := loadProfile(t, `
[default]
client_name     = "tim"
client_key      = "/keys/tim.pem"
cinc_server_url = "cinc.example.com"
`, "default")

	assertMessage(t, p.Validate(),
		`"default" profile`, path, `"cinc.example.com"`, "https://", "cinc_server_url")
}

func TestValidateMissingServerURLNamesProfileAndKeepsSentinel(t *testing.T) {
	p, path := loadProfile(t, `
[default]
client_name = "tim"
client_key  = "/keys/tim.pem"
`, "default")

	err := p.Validate()
	if !errors.Is(err, ErrMissingServerURL) {
		t.Fatalf("errors.Is(err, ErrMissingServerURL) = false for %v", err)
	}
	assertMessage(t, err, `"default" profile`, path, "cinc_server_url", "/organizations/")
}

// A profile built in memory has no name or file to report; the message
// must still read cleanly rather than naming an empty profile.
func TestValidateWithoutSourceStillReadsCleanly(t *testing.T) {
	err := Profile{KeyPath: "/keys/tim.pem"}.ValidateIdentity()
	assertMessage(t, err, "client_name")
	if strings.Contains(err.Error(), `""`) {
		t.Errorf("error should not name an empty profile, got:\n%s", err)
	}
}

func TestUnknownProfileListsTheProfilesThatExist(t *testing.T) {
	path := writeConfig(t, sampleConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.Profile("prod")
	assertMessage(t, err,
		`"prod"`, path, "default, staging, supermarket", "--profile", "config create --profile prod")
}

func TestUnknownProfileFromEnvironmentSaysWhereTheNameCameFrom(t *testing.T) {
	t.Setenv("CINC_PROFILE", "prod")
	t.Setenv("CHEF_PROFILE", "")
	cfg, err := Load(writeConfig(t, sampleConfig))
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.Profile("")
	assertMessage(t, err, `"prod"`, "CINC_PROFILE")
}

func TestUnknownProfileWithOneProfileReadsNaturally(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[default]\nclient_name = \"tim\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.Profile("prod")
	assertMessage(t, err, `The only profile it has is "default".`, "Pick that one")
}

func TestUnknownProfileInAnEmptyFile(t *testing.T) {
	path := writeConfig(t, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.Profile("default")
	assertMessage(t, err, `"default"`, path, "doesn't have any profiles", "config create")
}

func TestLoadMissingFileExplainsAndKeepsNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "credentials")

	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
	assertMessage(t, err, path, "config create --config "+path)
}

func TestLoadUnreadableFileSaysHowToFixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes don't restrict this user here")
	}
	path := writeConfig(t, sampleConfig)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("errors.Is(err, fs.ErrPermission) = false for %v", err)
	}
	assertMessage(t, err, path, "permission", "chmod 600 "+path)
}

func TestLoadSyntaxErrorNamesTheLineAndShowsTheFormat(t *testing.T) {
	path := writeConfig(t, "[default]\nclient_name = tim\n")

	_, err := Load(path)
	assertMessage(t, err, "we couldn't read", path, "line 2", "[default]", `client_name     = "`)
}

func TestLoadTypeErrorNamesTheLine(t *testing.T) {
	path := writeConfig(t, "[default]\nclient_name = 5\n")

	_, err := Load(path)
	assertMessage(t, err, "we couldn't read", path, "line 2", "client_name")
}

func TestWriteProfileOverMalformedFileExplains(t *testing.T) {
	path := writeConfig(t, "[broken")

	err := WriteProfile(path, "worker", Profile{ClientName: "tim", KeyPath: "/keys/tim.pem"})
	assertMessage(t, err, "we couldn't read", path, "line 1")
}

func TestValidateSiteURLShowsAnExample(t *testing.T) {
	assertMessage(t, ValidateSiteURL("supermarket.example.com"),
		`"supermarket.example.com"`, "https://")
}

func TestValidateSupermarketIdentityNamesTheKeys(t *testing.T) {
	p, path := loadProfile(t, `
[supermarket]
supermarket_site = "https://supermarket.example.com"
`, "supermarket")

	assertMessage(t, p.ValidateSupermarketIdentity(),
		"Supermarket identity", `"supermarket" profile`, path,
		"supermarket_client_name", "supermarket_key", "client_name", "client_key")
}
