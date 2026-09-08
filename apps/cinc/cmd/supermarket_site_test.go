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
)

// newSiteRecordingServer stands in for a private Supermarket. It records the
// first path it is asked for, so a test can tell whether a command talked to
// it at all, and answers the handful of endpoints the read commands use.
func newSiteRecordingServer(t *testing.T, got *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *got == "" {
			*got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write([]byte("tar-body"))
		case strings.Contains(r.URL.Path, "/search"):
			_, _ = w.Write([]byte(`{"start":0,"total":0,"items":[]}`))
		case strings.HasSuffix(r.URL.Path, "/cookbooks"):
			_, _ = w.Write([]byte(`{"start":0,"total":0,"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{"name":"nginx","latest_version":"` + srvVersionURL(r) + `","versions":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func srvVersionURL(r *http.Request) string {
	return "http://" + r.Host + "/api/v1/cookbooks/nginx/versions/1_2_0"
}

// writeSupermarketSiteConfig writes a credentials file whose profile points at
// a private Supermarket, the way `cinc config create --supermarket-site` does.
func writeSupermarketSiteConfig(t *testing.T, profile, site string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[%s]
supermarket_site = %q
client_name      = "tim"
client_key       = %q
`, profile, site, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// TestSupermarketReadCommandsUseProfileSite covers the split where
// `supermarket share` honored a profile's supermarket_site while every
// read-only command silently queried the public Supermarket instead. A user
// with a private Supermarket configured should not have to repeat
// --supermarket-site on each command, and `install` in particular should not
// pull a public cookbook onto their server when they meant their own.
func TestSupermarketReadCommandsUseProfileSite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name string
		args func(site string) []string
	}{
		{"download", func(string) []string { return []string{"supermarket", "download", "nginx"} }},
		{"show", func(string) []string { return []string{"supermarket", "show", "nginx"} }},
		{"search", func(string) []string { return []string{"supermarket", "search", "nginx"} }},
		{"list", func(string) []string { return []string{"supermarket", "list"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := newSiteRecordingServer(t, &got)
			cfgPath := writeSupermarketSiteConfig(t, "default", srv.URL)

			root := newRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			args := append(tc.args(srv.URL), "--config", cfgPath)
			if tc.name == "download" {
				args = append(args, "--file", filepath.Join(t.TempDir(), "out.tgz"))
			}
			root.SetArgs(args)
			_ = root.Execute()

			if got == "" {
				t.Fatalf("%s never reached the configured Supermarket at %s; it used the public default", tc.name, srv.URL)
			}
		})
	}
}

// The flag still wins over the profile.
func TestSupermarketSiteFlagBeatsProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := writeSupermarketSiteConfig(t, "default", "https://profile.example.test")

	cmd := fakeCmd(cfgPath, "", "", new(bytes.Buffer))
	if got := resolveSupermarketSite(cmd, "https://flag.example.test"); got != "https://flag.example.test" {
		t.Errorf("site = %q, want the --supermarket-site flag to win", got)
	}
}

// With no credentials file at all the read commands must still work, falling
// back to the public Supermarket rather than erroring or prompting for setup.
func TestSupermarketSiteFallsBackWithoutCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	cmd := fakeCmd(missing, "", "", new(bytes.Buffer))
	if got := resolveSupermarketSite(cmd, ""); got != "" {
		t.Errorf("site = %q, want \"\" so the caller uses the public default", got)
	}
}

// A profile with no supermarket_site leaves the default in place.
func TestSupermarketSiteEmptyProfileKeyFallsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "https://cinc.example.test/organizations/acme"
client_name     = "tim"
client_key      = %q
`, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := fakeCmd(cfgPath, "", "", new(bytes.Buffer))
	if got := resolveSupermarketSite(cmd, ""); got != "" {
		t.Errorf("site = %q, want \"\" when the profile sets no supermarket_site", got)
	}
}

// The conventional [supermarket] profile is preferred over [default], the
// same precedence `supermarket share` already uses.
func TestSupermarketSitePrefersSupermarketProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
supermarket_site = "https://default.example.test"
client_name      = "tim"
client_key       = %q

[supermarket]
supermarket_site = "https://supermarket-profile.example.test"
client_name      = "tim"
client_key       = %q
`, writeTestKey(t), writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := fakeCmd(cfgPath, "", "", new(bytes.Buffer))
	if got := resolveSupermarketSite(cmd, ""); !strings.Contains(got, "supermarket-profile") {
		t.Errorf("site = %q, want the [supermarket] profile to win over [default]", got)
	}
}
