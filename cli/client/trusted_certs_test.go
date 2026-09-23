package client

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// tlsServer starts a self-signed TLS server that answers the org's clients
// endpoint, so a List call succeeds only when the server's certificate is
// trusted.
func tlsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeServerCert writes srv's certificate as PEM into dir/name.
func writeServerCert(t *testing.T, srv *httptest.Server, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func profileFor(t *testing.T, srv *httptest.Server) config.Profile {
	return config.Profile{
		ServerURL:  srv.URL,
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    writeKeyFile(t),
	}
}

func listClients(t *testing.T, p config.Profile) error {
	t.Helper()
	c, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err = c.Clients.List(ctx)
	return err
}

func TestNewRejectsSelfSignedServerWithoutTrustedCerts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := tlsServer(t)
	if err := listClients(t, profileFor(t, srv)); err == nil {
		t.Fatal("expected a certificate verification error without trusted certs")
	}
}

func TestNewTrustsCertsFromConfiguredTrustedCertsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := tlsServer(t)
	dir := filepath.Join(t.TempDir(), "certs")
	writeServerCert(t, srv, dir, "server.crt")

	p := profileFor(t, srv)
	p.TrustedCertsDir = dir
	if err := listClients(t, p); err != nil {
		t.Fatalf("request with the server's cert trusted: %v", err)
	}
}

func TestNewTrustsCertsFromDefaultTrustedCertsDirs(t *testing.T) {
	for _, sub := range []string{".cinc", ".chef"} {
		t.Run(sub, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			srv := tlsServer(t)
			writeServerCert(t, srv, filepath.Join(home, sub, "trusted_certs"), "server.pem")
			if err := listClients(t, profileFor(t, srv)); err != nil {
				t.Fatalf("request with the server's cert in ~/%s/trusted_certs: %v", sub, err)
			}
		})
	}
}

func TestNewIgnoresUnparseableFilesInTrustedCertsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := tlsServer(t)
	dir := filepath.Join(t.TempDir(), "certs")
	writeServerCert(t, srv, dir, "server.crt")
	writeFile(t, filepath.Join(dir, "garbage.pem"), "not a certificate")

	p := profileFor(t, srv)
	p.TrustedCertsDir = dir
	if err := listClients(t, p); err != nil {
		t.Fatalf("an unparseable file should not break the request: %v", err)
	}
}

func TestNewRejectsMissingConfiguredTrustedCertsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := config.Profile{
		ServerURL:       "https://chef.example.com",
		Org:             "acme",
		ClientName:      "tim",
		KeyPath:         writeKeyFile(t),
		TrustedCertsDir: filepath.Join(t.TempDir(), "absent"),
	}
	_, err := New(p)
	if err == nil {
		t.Fatal("expected an error for a trusted_certs_dir that does not exist")
	}
	if !strings.Contains(err.Error(), "trusted_certs_dir") || !strings.Contains(err.Error(), "absent") {
		t.Errorf("error should name the key and the path, got %q", err)
	}
	// Must not look like a missing client key, or the command layer rewrites it
	// into a "can't find your client key" message.
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should not wrap fs.ErrNotExist, got %v", err)
	}
}

func TestLoadTrustedCertsReportsLoadedAndSkippedFiles(t *testing.T) {
	srv := tlsServer(t)
	dir := t.TempDir()
	writeServerCert(t, srv, dir, "a.crt")
	writeServerCert(t, srv, dir, "b.PEM")
	writeFile(t, filepath.Join(dir, "garbage.pem"), "not a certificate")
	writeFile(t, filepath.Join(dir, "README.txt"), "ignored: wrong extension")
	if err := os.Mkdir(filepath.Join(dir, "sub.pem"), 0o700); err != nil {
		t.Fatal(err)
	}

	tc, err := LoadTrustedCerts(dir)
	if err != nil {
		t.Fatalf("LoadTrustedCerts: %v", err)
	}
	if tc.Pool == nil {
		t.Fatal("expected a cert pool")
	}
	if got := strings.Join(tc.Loaded, ","); got != "a.crt,b.PEM" {
		t.Errorf("Loaded = %q, want a.crt,b.PEM", got)
	}
	if got := strings.Join(tc.Skipped, ","); got != "garbage.pem" {
		t.Errorf("Skipped = %q, want garbage.pem", got)
	}
}

func TestLoadTrustedCertsRejectsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "certs")
	writeFile(t, path, "x")
	if _, err := LoadTrustedCerts(path); err == nil {
		t.Fatal("expected an error when trusted_certs_dir is a file")
	}
}

func TestTrustedHTTPClientKeepsTimeout(t *testing.T) {
	tc, err := LoadTrustedCerts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hc := trustedHTTPClient(tc.Pool)
	if hc.Timeout != defaultHTTPTimeout {
		t.Errorf("Timeout = %v, want %v", hc.Timeout, defaultHTTPTimeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs != tc.Pool {
		t.Fatalf("transport should carry the trusted pool, got %#v", hc.Transport)
	}
	if tr.Proxy == nil {
		t.Error("transport should keep the default proxy-from-environment behavior")
	}
}
