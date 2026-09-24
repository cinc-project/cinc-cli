package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinc-project/cinc-cli/cli/config"
)

func TestConfigValidateCommandReportsValidConfig(t *testing.T) {
	srv := configValidateServer(t, http.StatusOK)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config validate: %v", err)
	}
	got := out.String()
	for _, want := range []string{"is valid", "default profile [VALID]", "✓ Server URL is valid", "✓ Server is reachable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

// TestConfigValidateBudgetsEachNetworkCheckSeparately covers a file with more
// than one profile. The checks run in sequence, so a shared deadline meant an
// unreachable endpoint early on could burn the whole budget and leave later
// profiles reported as failures when the only thing wrong was the clock. Each
// network check gets its own deadline instead.
func TestConfigValidateBudgetsEachNetworkCheckSeparately(t *testing.T) {
	// Stands in for a slow endpoint: it consumes the caller's deadline in full
	// rather than answering.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	healthy := configValidateServer(t, http.StatusOK)

	// "aaa" sorts before "zzz", so the slow profile is checked first.
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[aaa_slow]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"

[zzz_healthy]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), slow.URL, writeTestKey(t), healthy.URL))

	prev := networkCheckTimeout
	networkCheckTimeout = 500 * time.Millisecond
	t.Cleanup(func() { networkCheckTimeout = prev })

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	result := runConfigChecks(context.Background(), cfgPath, cfg)

	var healthyProfile *profileResult
	for i := range result.Profiles {
		if result.Profiles[i].Name == "zzz_healthy" {
			healthyProfile = &result.Profiles[i]
		}
	}
	if healthyProfile == nil {
		t.Fatal("zzz_healthy profile missing from the report")
	}
	for _, c := range healthyProfile.Checks {
		if c.Name == "Server is reachable" && !c.Passed {
			t.Errorf("a healthy profile was reported unreachable because an earlier profile was slow: %s", c.Detail)
		}
	}
}

func TestParseServerHost(t *testing.T) {
	ok := map[string]string{
		"https://chef.example.com":     "chef.example.com",
		"http://chef.example.com:8443": "chef.example.com",
		"https://127.0.0.1:8889":       "127.0.0.1",
	}
	for in, want := range ok {
		if got, err := parseServerHost(in); err != nil || got != want {
			t.Errorf("parseServerHost(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "chef.example.com", "ftp://host", "https://", "://nope"} {
		if _, err := parseServerHost(in); err == nil {
			t.Errorf("parseServerHost(%q) = nil error, want a format error", in)
		}
	}
}

func withStubResolver(t *testing.T, fn func(context.Context, string) ([]string, error)) {
	t.Helper()
	orig := resolveHost
	resolveHost = fn
	t.Cleanup(func() { resolveHost = orig })
}

func TestConfigValidateServerReachableFailsOnDNS(t *testing.T) {
	// The reachable check resolves DNS before connecting; a resolution failure
	// is reported there (and the server is never dialed).
	srv := configValidateServer(t, http.StatusOK)
	contacted := false
	srv.Config.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { contacted = true })
	withStubResolver(t, func(context.Context, string) ([]string, error) {
		return nil, fmt.Errorf("no such host")
	})

	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err == nil {
		t.Fatal("expected validation error when DNS fails")
	}
	if got := out.String(); !strings.Contains(got, "✗ Server is reachable: cannot resolve host") {
		t.Errorf("stdout = %q, want a DNS resolution failure on the reachable check", got)
	}
	if contacted {
		t.Error("the server should not be dialed when DNS resolution fails")
	}
}

func TestConfigValidateCommandPreflightsSupermarketSite(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The reachable check hits the Supermarket health endpoint via the
		// cinc-supermarket client.
		if r.URL.Path != "/api/v1/health" {
			t.Errorf("path = %q, want /api/v1/health", r.URL.Path)
		}
		hit = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	t.Cleanup(srv.Close)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[supermarket]
client_name = "tim"
client_key = %q
supermarket_site = "%s"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config validate: %v", err)
	}
	if !hit {
		t.Fatal("supermarket reachable check did not contact the site")
	}
	if got := out.String(); !strings.Contains(got, "✓ Supermarket is reachable") {
		t.Errorf("stdout = %q, want the supermarket check to pass", got)
	}
}

func TestConfigValidateCommandRejectsServerURLMissingOrg(t *testing.T) {
	// A server URL that omits /organizations/<org> fails the single combined
	// "Server URL is valid" check; the load itself still succeeds.
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "https://cinc.example.com"
`, writeTestKey(t)))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err == nil {
		t.Fatal("expected validation error for a server URL missing /organizations/<org>")
	}
	got := out.String()
	if !strings.Contains(got, "default profile [INVALID]") ||
		!strings.Contains(got, "✗ Server URL is valid") ||
		!strings.Contains(got, "organizations") {
		t.Fatalf("stdout = %q, want the combined server URL check to fail citing /organizations", got)
	}
	// The reachable check shouldn't run when the URL never parsed.
	if strings.Contains(got, "Server is reachable") {
		t.Errorf("reachable check should be skipped for an unparseable URL:\n%s", got)
	}
}

func TestConfigValidateCommandNotesDefaultSupermarketSite(t *testing.T) {
	// With no supermarket_site set, the site-URL check passes (green) with a
	// note naming the default public Supermarket it falls back to.
	srv := configValidateServer(t, http.StatusOK)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config validate: %v", err)
	}
	want := "✓ Supermarket site URL is valid: using the default https://supermarket.chef.io"
	if got := out.String(); !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestConfigValidateCommandWarnsOnInsecureSSLVerifyMode(t *testing.T) {
	// :verify_none is a warning, not a failure: a yellow check with a note, and
	// the profile stays VALID.
	srv := configValidateServer(t, http.StatusOK)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
ssl_verify_mode = ":verify_none"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc config validate should pass with a warning: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "default profile [VALID]") {
		t.Errorf("a :verify_none warning should not invalidate the profile:\n%s", got)
	}
	if !strings.Contains(got, "ssl_verify_mode is valid: Insecure :verify_none mode configured") {
		t.Errorf("stdout = %q, want the insecure-mode warning note", got)
	}
}

func TestConfigValidateCommandReportsUnreachableServer(t *testing.T) {
	srv := configValidateServer(t, http.StatusInternalServerError)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	if err := root.Execute(); err == nil {
		t.Fatal("expected validation error for an unreachable server")
	}
	got := out.String()
	for _, want := range []string{"is invalid", "default profile [INVALID]", "✗ Server is reachable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func TestConfigValidateCommandReportsInvalidConfig(t *testing.T) {
	cfgPath := writeValidateConfig(t, `
[broken]
client_name = "tim"
`)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Fatalf("error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"broken profile [INVALID]",
		"✗ Client key is configured: client_key is required",
		"✗ An endpoint is configured: configure cinc_server_url, chef_server_url, or supermarket_site",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func TestConfigValidateCommandFormatsChecks(t *testing.T) {
	cfgPath := writeValidateConfig(t, `
[broken]
client_name = "tim"
`)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected validation error")
	}
	got := out.String()

	// Overall line, then the top-level checks.
	if !strings.HasPrefix(got, "Config ") || !strings.Contains(got, " is invalid!\n") {
		t.Errorf("missing overall line:\n%s", got)
	}
	if !strings.Contains(got, "✓ Credentials file is valid TOML") ||
		!strings.Contains(got, "✓ At least one profile is defined") {
		t.Errorf("missing top-level checks:\n%s", got)
	}
	// A blank line separates the top-level checks from the profile block, which
	// is tagged and has indented per-profile checks.
	if !strings.Contains(got, "\n\nbroken profile [INVALID]\n") {
		t.Errorf("profile block not separated/tagged:\n%s", got)
	}
	if !strings.Contains(got, "  ✓ Client name is configured\n") {
		t.Errorf("per-profile checks not indented:\n%s", got)
	}
	// No leftover wording from the old issue-count format.
	if strings.Contains(got, "issue(s)") {
		t.Errorf("output still mentions issue counts:\n%s", got)
	}
	// The error wraps errAlreadyReported so Execute exits non-zero without
	// re-printing a generic "Error: ..." line.
	if !errors.Is(err, errAlreadyReported) {
		t.Errorf("error should wrap errAlreadyReported; got %v", err)
	}
}

func TestConfigValidateCommandSupportsJSONOutput(t *testing.T) {
	cfgPath := writeValidateConfig(t, `
[broken]
client_key = "/keys/tim.pem"
`)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath, "--format", "json"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected validation error")
	}

	var result configValidationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Valid {
		t.Error("result.valid should be false")
	}
	if len(result.TopLevel) == 0 {
		t.Error("expected top-level checks in JSON")
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Name != "broken" || result.Profiles[0].Valid {
		t.Fatalf("profiles = %+v", result.Profiles)
	}
	var found bool
	for _, c := range result.Profiles[0].Checks {
		if c.Name == "Client name is configured" {
			found = true
			if c.Passed {
				t.Error("'Client name is configured' should have failed for the broken profile")
			}
		}
	}
	if !found {
		t.Error("expected a 'Client name is configured' check in the JSON output")
	}
}

func writeValidateConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func configValidateServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestConfigValidateExpandsTildeInClientKey checks the key-readable check
// reads a client_key written as ~/... from the home directory, as the real
// client does.
func TestConfigValidateExpandsTildeInClientKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv := configValidateServer(t, http.StatusOK)
	key, err := os.ReadFile(writeTestKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tim.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = "~/tim.pem"
cinc_server_url = "%s/organizations/acme"
`, srv.URL))

	out, _, err := runRoot(t, "config", "validate", cfgPath)
	if err != nil {
		t.Fatalf("cinc config validate: %v\n%s", err, out)
	}
	for _, want := range []string{"✓ Client key file is readable", "✓ Server is reachable"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	}
}

// TestConfigValidateExplainsRefusedConnection checks the reachability
// failure for a server that isn't listening reads as a sentence naming the
// address, not Go's dial error chain.
func TestConfigValidateExplainsRefusedConnection(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "http://%s/organizations/acme"
`, writeTestKey(t), addr))

	out, _, err := runRoot(t, "config", "validate", cfgPath)
	if err == nil {
		t.Fatalf("validate should fail for a server that isn't listening:\n%s", out)
	}
	want := "✗ Server is reachable: we couldn't connect to " + addr
	if !strings.Contains(out, want) {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// TestConfigValidateReachableForAnActorThatCannotListClients covers a profile
// that signs as a node's own client. The reachability check lists clients,
// which erchef refuses a plain client with a 403. A 403 still proves the
// server answered and accepted the signature, which is all "reachable"
// means, so the profile is valid.
func TestConfigValidateReachableForAnActorThatCannotListClients(t *testing.T) {
	srv := configValidateServer(t, http.StatusForbidden)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "web01"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	out, _, err := runRoot(t, "config", "validate", cfgPath)
	if err != nil {
		t.Fatalf("cinc config validate: %v\n%s", err, out)
	}
	for _, want := range []string{"default profile [VALID]", "✓ Server is reachable: ", "web01"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	}
}

// TestConfigValidateReportsRejectedSignature keeps a 401 a failure: the
// server answered, but it does not accept who the profile says it is.
func TestConfigValidateReportsRejectedSignature(t *testing.T) {
	srv := configValidateServer(t, http.StatusUnauthorized)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	out, _, err := runRoot(t, "config", "validate", cfgPath)
	if err == nil {
		t.Fatalf("a 401 should fail validation:\n%s", out)
	}
	if !strings.Contains(out, "✗ Server is reachable") {
		t.Errorf("stdout = %q, want the reachability check to fail", out)
	}
}

// TestConfigValidateChecksOnlyTheProfileFlagNames validates a file with a
// good and a broken profile. With --profile only that profile is checked;
// CINC_PROFILE, which users often set for every command, does not narrow
// it; and an unknown --profile is an error naming the profile and file.
func TestConfigValidateChecksOnlyTheProfileFlagNames(t *testing.T) {
	srv := configValidateServer(t, http.StatusOK)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name = "tim"
client_key = %q
cinc_server_url = "%s/organizations/acme"

[broken]
client_name = "tim"
client_key = "/no/such/key.pem"
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL, srv.URL))

	out, _, err := runRoot(t, "config", "validate", cfgPath, "--profile", "default", "--format", "json")
	if err != nil {
		t.Fatalf("config validate --profile default: %v\n%s", err, out)
	}
	var result configValidationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Name != "default" {
		t.Errorf("profiles checked = %+v, want only default", result.Profiles)
	}

	t.Setenv("CINC_PROFILE", "default")
	out, _, err = runRoot(t, "config", "validate", cfgPath, "--format", "json")
	if err == nil || !strings.Contains(out, `"broken"`) {
		t.Errorf("CINC_PROFILE should not narrow config validate; err = %v, out:\n%s", err, out)
	}

	_, _, err = runRoot(t, "config", "validate", cfgPath, "--profile", "nosuch")
	if err == nil || !strings.Contains(err.Error(), `"nosuch"`) || !strings.Contains(err.Error(), cfgPath) {
		t.Errorf("unknown --profile: err = %v, want one naming the profile and %s", err, cfgPath)
	}
}
