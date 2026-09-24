package suite

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// Helpers shared by the client and key families. They prove a key actually
// works by signing a request with it, not just that the server stored it.

// revokeTimeout bounds how long a case waits for the server to stop
// accepting a key it revoked. erchef may cache an actor's keys briefly; on
// cinc-server-ng the first attempt passes.
const revokeTimeout = 15 * time.Second

// keyPair is an RSA key generated locally, written to disk in both halves.
type keyPair struct {
	privPath, pubPath string
	pubPEM            string
}

// newKeyPair generates a 2048-bit RSA key and writes its private half
// (PKCS#1, as the server generates them) and public half (PKIX) to files.
func newKeyPair(t *testing.T) keyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	kp := keyPair{
		privPath: filepath.Join(dir, "key.pem"),
		pubPath:  filepath.Join(dir, "key.pub"),
		pubPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})),
	}
	writeFile(t, kp.privPath, string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})))
	writeFile(t, kp.pubPath, kp.pubPEM)
	return kp
}

// writeKey writes PEM text (a private key a command printed) to a new file
// and returns its path, so a profile can sign with it.
func writeKey(t *testing.T, pemText string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	writeFile(t, path, pemText)
	return path
}

// wantPrivateKey fails the case unless out is exactly one PEM private key,
// which is what a command prints when it streams a generated key to stdout.
func wantPrivateKey(t *testing.T, what, out string) {
	t.Helper()
	block, rest := pem.Decode([]byte(out))
	if block == nil || !strings.Contains(block.Type, "PRIVATE KEY") || strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("%s should print exactly one PEM private key, got:\n%s", what, out)
	}
}

// publicKeyOf parses PEM text holding an RSA public key (PKIX or PKCS#1) or
// an RSA private key (PKCS#1 or PKCS#8) and returns the public half.
func publicKeyOf(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, fmt.Errorf("%T is not an RSA key", pub)
	}
	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub, nil
	}
	if priv, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &priv.PublicKey, nil
	}
	if priv, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaPriv, ok := priv.(*rsa.PrivateKey); ok {
			return &rsaPriv.PublicKey, nil
		}
		return nil, fmt.Errorf("%T is not an RSA key", priv)
	}
	return nil, fmt.Errorf("cannot parse a %q PEM block as an RSA key", block.Type)
}

// sameKey reports whether two PEM texts (public or private, any encoding)
// hold the same RSA key pair.
func sameKey(t *testing.T, a, b string) bool {
	t.Helper()
	ka, err := publicKeyOf(a)
	if err != nil {
		t.Fatalf("parse key: %v\n%s", err, a)
	}
	kb, err := publicKeyOf(b)
	if err != nil {
		t.Fatalf("parse key: %v\n%s", err, b)
	}
	return ka.Equal(kb)
}

// wantSameKey fails the case unless got and want hold the same key pair.
func wantSameKey(t *testing.T, what, got, want string) {
	t.Helper()
	if !sameKey(t, got, want) {
		t.Fatalf("%s holds a different key than expected:\n%s", what, got)
	}
}

// createClient creates a client with a server-generated key written to a
// file, registers its cleanup, and returns the key's path.
func createClient(c *cli, name string, flags ...string) string {
	c.t.Helper()
	keyPath := filepath.Join(c.t.TempDir(), name+".pem")
	c.run(append([]string{"client", "create", name, "--key-file", keyPath}, flags...)...)
	c.cleanup("client", "delete", name)
	return keyPath
}

// createNamedUser creates a global user named name with a server-generated key written to a
// file, registers its cleanup, and returns the key's path. Users are
// server-wide, so the name must come from uniqueName.
func createNamedUser(c *cli, name string) string {
	c.t.Helper()
	keyPath := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("user", "create", name,
		"--email", name+"@example.test", "--display-name", "Test "+name,
		"--first-name", "Test", "--last-name", name,
		"--password", "pw-"+randomHex(c.t, 12),
		"--key-file", keyPath)
	c.cleanup("user", "delete", name)
	return keyPath
}

// clientProfile adds a profile signing as client name with keyPath in the
// target's org and returns the profile name.
func clientProfile(c *cli, name, keyPath string) string {
	c.t.Helper()
	profile := "as-" + randomHex(c.t, 4)
	c.addProfile(profile, c.tgt.Org, name, keyPath)
	return profile
}

// probe is a signed request an actor can make. A client lists nodes; a user
// reads its own user record, which needs no org membership.
type probe []string

func clientProbe() probe          { return probe{"node", "list"} }
func userProbe(name string) probe { return probe{"user", "show", name} }

// signAs runs p signed as profile and returns what happened.
func signAs(c *cli, profile string, p probe) result {
	c.t.Helper()
	return c.exec(runOpts{}, append(append([]string{}, p...), "--profile", profile)...)
}

// authenticated reports whether the server accepted r's signature: the
// request succeeded, or authorization refused it (403) after authentication
// passed. Only a 401 means the key was rejected.
func authenticated(r result) bool {
	return r.exitCode == 0 || (hasStatus(r.stderr, 403) && !hasStatus(r.stderr, 401))
}

// rejected reports whether the server refused r's signature with a 401.
func rejected(r result) bool {
	return r.exitCode != 0 && hasStatus(r.stderr, 401)
}

// wantSigns fails the case unless a request signed as profile authenticates.
func wantSigns(t *testing.T, c *cli, profile string, p probe) {
	t.Helper()
	if r := signAs(c, profile, p); !authenticated(r) {
		t.Fatalf("the key for profile %s should authenticate: %s", profile, r)
	}
}

// wantRejected fails the case unless requests signed as profile are refused
// with a 401 within revokeTimeout.
func wantRejected(t *testing.T, c *cli, profile string, p probe) {
	t.Helper()
	eventually(t, revokeTimeout, func() error {
		if r := signAs(c, profile, p); !rejected(r) {
			return fmt.Errorf("the key for profile %s should be rejected with a 401: %s", profile, r)
		}
		return nil
	})
}

// wantStatus fails the case unless r failed and its error names the HTTP
// status code (the CLI reports server errors as "...: 403: ...").
func wantStatus(t *testing.T, r result, code int) {
	t.Helper()
	if r.exitCode == 0 || !strings.Contains(r.stderr, fmt.Sprintf(": %d:", code)) {
		t.Fatalf("want an HTTP %d error: %s", code, r)
	}
}

// wantMode fails the case unless the file at path has exactly mode perm.
func wantMode(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != perm {
		t.Fatalf("%s mode = %v, want %v", path, got, perm)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// showKey returns one of an owner's keys; noun is "client" or "user".
func showKey(c *cli, noun, owner, key string) cinc.Key {
	c.t.Helper()
	var k cinc.Key
	c.json(&k, noun, "key", "show", owner, key)
	return k
}

// keyNames returns an owner's key names from `key list --format json`.
func keyNames(c *cli, noun, owner string) []string {
	c.t.Helper()
	var names []string
	c.json(&names, noun, "key", "list", owner)
	return names
}
