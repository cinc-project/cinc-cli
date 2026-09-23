package cmd

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configValidateTLSServer is configValidateServer over TLS with a self-signed
// certificate, so "Server is reachable" passes only when that certificate is
// trusted.
func configValidateTLSServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/clients", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeTrustedCert(t *testing.T, srv *httptest.Server, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runConfigValidate(t *testing.T, cfgPath string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "validate", cfgPath})
	err := root.Execute()
	return out.String(), err
}

func TestConfigValidateTrustsCertsAndReportsUnparseableFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := configValidateTLSServer(t)
	dir := filepath.Join(t.TempDir(), "certs")
	writeTrustedCert(t, srv, dir, "server.crt")
	if err := os.WriteFile(filepath.Join(dir, "broken.pem"), []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name       = "tim"
client_key        = %q
cinc_server_url   = "%s/organizations/acme"
trusted_certs_dir = %q
`, writeTestKey(t), srv.URL, dir))

	got, err := runConfigValidate(t, cfgPath)
	if err != nil {
		t.Fatalf("an unparseable cert file is a warning, not a failure: %v\n%s", err, got)
	}
	for _, want := range []string{
		"default profile [VALID]",
		"Trusted certificates load: we skipped 1 file in " + dir,
		"broken.pem",
		"✓ Server is reachable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	}
}

func TestConfigValidateNotesDefaultTrustedCertsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv := configValidateTLSServer(t)
	dir := filepath.Join(home, ".chef", "trusted_certs")
	writeTrustedCert(t, srv, dir, "server.pem")
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name     = "tim"
client_key      = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	got, err := runConfigValidate(t, cfgPath)
	if err != nil {
		t.Fatalf("cinc config validate: %v\n%s", err, got)
	}
	want := "✓ Trusted certificates load: trusting 1 certificate file from " + dir
	if !strings.Contains(got, want) {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestConfigValidateFailsOnMissingTrustedCertsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := configValidateServer(t, http.StatusOK)
	missing := filepath.Join(t.TempDir(), "absent")
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name       = "tim"
client_key        = %q
cinc_server_url   = "%s/organizations/acme"
trusted_certs_dir = %q
`, writeTestKey(t), srv.URL, missing))

	got, err := runConfigValidate(t, cfgPath)
	if err == nil {
		t.Fatalf("expected validation to fail for a missing trusted_certs_dir:\n%s", got)
	}
	for _, want := range []string{"default profile [INVALID]", "✗ Trusted certificates load", "we couldn't find the trusted_certs_dir " + missing} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	}
}

func TestConfigValidateOmitsTrustedCertsCheckWhenNoneConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := configValidateServer(t, http.StatusOK)
	cfgPath := writeValidateConfig(t, fmt.Sprintf(`
[default]
client_name     = "tim"
client_key      = %q
cinc_server_url = "%s/organizations/acme"
`, writeTestKey(t), srv.URL))

	got, err := runConfigValidate(t, cfgPath)
	if err != nil {
		t.Fatalf("cinc config validate: %v\n%s", err, got)
	}
	if strings.Contains(got, "Trusted certificates") {
		t.Errorf("no trusted certs are configured, so the check should be omitted:\n%s", got)
	}
}
