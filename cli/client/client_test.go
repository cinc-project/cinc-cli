package client

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// writeKeyFile generates an RSA key, writes it as PEM to a temp file, and
// returns the path.
func writeKeyFile(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewRejectsIncompleteProfile(t *testing.T) {
	if _, err := New(config.Profile{Org: "acme"}); err == nil {
		t.Error("expected an error building a client from an incomplete profile")
	}
}

func TestNewRejectsMissingKeyFile(t *testing.T) {
	p := config.Profile{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    filepath.Join(t.TempDir(), "absent.pem"),
	}
	if _, err := New(p); err == nil {
		t.Error("expected an error when the key file does not exist")
	}
}

func TestNewBuildsClientFromValidProfile(t *testing.T) {
	p := config.Profile{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    writeKeyFile(t),
	}

	c, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("New returned a nil client")
	}
}

// withCapturedTLSWarn swaps tlsWarnWriter for a buffer for the test's duration.
func withCapturedTLSWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := tlsWarnWriter
	tlsWarnWriter = &buf
	t.Cleanup(func() { tlsWarnWriter = orig })
	return &buf
}

func TestNewWarnsWhenTLSVerificationDisabled(t *testing.T) {
	buf := withCapturedTLSWarn(t)
	p := config.Profile{
		ServerURL:     "https://chef.example.com",
		Org:           "acme",
		ClientName:    "tim",
		KeyPath:       writeKeyFile(t),
		SSLVerifyMode: ":verify_none",
	}
	if _, err := New(p); err != nil {
		t.Fatalf("New: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "verification is disabled") || !strings.Contains(got, "chef.example.com") {
		t.Errorf("expected a TLS-disabled warning naming the server, got %q", got)
	}
}

func TestSilenceTLSWarningSuppressesAndRestores(t *testing.T) {
	buf := withCapturedTLSWarn(t)
	p := config.Profile{
		ServerURL:     "https://chef.example.com",
		Org:           "acme",
		ClientName:    "tim",
		KeyPath:       writeKeyFile(t),
		SSLVerifyMode: ":verify_none",
	}

	restore := SilenceTLSWarning()
	if _, err := New(p); err != nil {
		t.Fatalf("New: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("warning should be silenced, got %q", buf.String())
	}

	// After restore the warning is written again — silencing is scoped, not permanent.
	restore()
	if _, err := New(p); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(buf.String(), "verification is disabled") {
		t.Errorf("warning should resume after restore, got %q", buf.String())
	}
}

func TestNewDoesNotWarnWhenTLSVerified(t *testing.T) {
	buf := withCapturedTLSWarn(t)
	p := config.Profile{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    writeKeyFile(t),
	}
	if _, err := New(p); err != nil {
		t.Fatalf("New: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("did not expect a warning with verification on, got %q", buf.String())
	}
}

// TestNewExpandsTildeInClientKey covers a hand-written (or knife-written)
// profile whose client_key starts with ~/. docs/configuration.md promises the
// ~ is expanded to the home directory.
func TestNewExpandsTildeInClientKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key, err := os.ReadFile(writeKeyFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "keys", "tim.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    "~/keys/tim.pem",
	}
	if _, err := New(p); err != nil {
		t.Fatalf("New with client_key under ~: %v", err)
	}
}
