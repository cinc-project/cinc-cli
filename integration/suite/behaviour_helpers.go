package suite

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Helpers for the cliFamily (behaviour.go). They are prefixed or scoped to
// that family's needs so they never collide with another family's helpers
// in this shared package.

// behBareCLI returns a cli for the same target and binary as c but with an empty
// HOME: no ~/.cinc/credentials, no trusted_certs, nothing. First-run and
// chef-compat cases start from here.
func behBareCLI(c *cli) *cli {
	c.t.Helper()
	return &cli{t: c.t, tgt: c.tgt, bin: c.bin, home: c.t.TempDir()}
}

// behProfileTOML renders one credentials profile as TOML, with keys in a
// fixed order so a case can compare files.
func behProfileTOML(name string, fields map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", name)
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		fmt.Fprintf(&b, "%s = %q\n", k, fields[k])
	}
	b.WriteString("\n")
	return b.String()
}

// behAppendProfile appends a profile with arbitrary keys to the
// credentials file at path, for profiles addProfile cannot express
// (trusted_certs_dir, chef_server_url, knife-only keys, ...).
func behAppendProfile(t *testing.T, path, name string, fields map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(behProfileTOML(name, fields))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}

// adminFields are the connection keys of a profile that signs as the
// target's admin against org.
func (c *cli) adminFields(org string) map[string]string {
	return map[string]string{
		"cinc_server_url": c.orgURL(org),
		"client_name":     c.tgt.Admin,
		"client_key":      c.tgt.KeyPath,
	}
}

// behWith returns a copy of fields with the given key/value pairs set; an
// empty value deletes the key.
func behWith(fields map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			delete(out, kv[i])
			continue
		}
		out[kv[i]] = kv[i+1]
	}
	return out
}

// behFreshKey writes a new RSA private key that no server knows about and
// returns its path.
func behFreshKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fresh.pem")
	writeFile(t, path, string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})))
	return path
}

// behCopyFile copies src to dst, creating dst's directory.
func behCopyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, string(b))
}

// behCACertPEM returns a CA certificate to drop into a trusted_certs_dir: the
// target's own CA when it has one (so the connection still verifies), or a
// throwaway self-signed CA for a plain-HTTP target, where the certificate
// only has to parse.
func behCACertPEM(t *testing.T, tgt Target) string {
	t.Helper()
	if tgt.CACertPath != "" {
		b, err := os.ReadFile(tgt.CACertPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cinc integration throwaway CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// behCreateRobot creates a plain (non-admin, non-validator) client in the
// target's org, registers its cleanup, and adds a profile named profile that
// signs as it. ACL enforcement is on, so it gets real 403s.
func behCreateRobot(c *cli, profile string) string {
	c.t.Helper()
	name := uniqueName(c.t, "robot")
	key := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("client", "create", name, "--key-file", key)
	c.cleanup("client", "delete", name)
	c.addProfile(profile, c.tgt.Org, name, key)
	return name
}

// execTTY runs the binary with HOME set to c.home (like exec) but lets the
// caller replace stdin with a pseudo-terminal, so the CLI's TTY guard sees an
// interactive session and the first-run prompts fire. stdout and stderr stay
// on plain buffers. answers are queued on the terminal before the process
// starts; a terminal in canonical mode hands them over one line per read, so
// each prompt reads exactly its own answer.
func (c *cli) execTTY(answers string, args ...string) result {
	c.t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		c.t.Fatalf("pty.Open: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.home
	cmd.Env = append(c.baseEnv(), c.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, &stdout, &stderr

	// Drain the terminal's output side (the echo of what we type) so the
	// kernel buffer can never fill and stall the child.
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	if _, err := io.WriteString(ptmx, answers); err != nil {
		c.t.Fatalf("write answers to pty: %v", err)
	}
	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		c.t.Fatalf("start cinc: %v", err)
	}
	// The child holds its own copy of the terminal.
	_ = tty.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		c.t.Fatalf("cinc %s did not finish; it is probably waiting on a prompt\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), stdout.String(), stderr.String())
	}
	r := result{args: args, stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(waitErr, &ee):
		r.exitCode = ee.ExitCode()
	case waitErr != nil:
		c.t.Fatalf("running cinc %s: %v", strings.Join(args, " "), waitErr)
	}
	return r
}

// validateReport mirrors the JSON `cinc config validate --format json`
// prints.
type validateReport struct {
	Path     string `json:"path"`
	Valid    bool   `json:"valid"`
	TopLevel []struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
		Detail string `json:"detail"`
	} `json:"top_level_checks"`
	Profiles []validateProfile `json:"profiles"`
}

type validateProfile struct {
	Name   string          `json:"name"`
	Valid  bool            `json:"valid"`
	Checks []validateCheck `json:"checks"`
}

type validateCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Warn   bool   `json:"warn"`
	Detail string `json:"detail"`
}

// validate runs `cinc config validate --format json` with args and decodes
// the report whatever the exit code, which it returns alongside.
func (c *cli) validate(args ...string) (validateReport, result) {
	c.t.Helper()
	r := c.exec(runOpts{}, append(append([]string{"config", "validate"}, args...), "--format", "json")...)
	var rep validateReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		c.t.Fatalf("config validate --format json is not JSON: %v\n%s", err, r)
	}
	// A failed validation has already printed its report; it must not add
	// a generic "Error: ..." line on top.
	if strings.Contains(r.stderr, "Error:") {
		c.t.Errorf("config validate printed a second error line: %s", r)
	}
	wantEqual(c.t, "config validate exit code for valid="+fmt.Sprint(rep.Valid), r.exitCode == 0, rep.Valid)
	return rep, r
}

// profile returns the named profile's results, failing the case if the
// report has none.
func (rep validateReport) profile(t *testing.T, name string) validateProfile {
	t.Helper()
	for _, p := range rep.Profiles {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("config validate reported no profile %q: %+v", name, rep)
	return validateProfile{}
}

// check returns the named check and whether the profile ran it.
func (p validateProfile) check(name string) (validateCheck, bool) {
	for _, c := range p.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return validateCheck{}, false
}

// wantCheck fails the case unless the profile ran the named check with the
// given outcome, and returns it for detail assertions.
func (p validateProfile) wantCheck(t *testing.T, name string, passed bool) validateCheck {
	t.Helper()
	c, ok := p.check(name)
	if !ok {
		t.Fatalf("profile %q did not run check %q: %+v", p.Name, name, p.Checks)
	}
	if c.Passed != passed {
		t.Fatalf("profile %q check %q passed = %v, want %v (detail: %q)", p.Name, name, c.Passed, passed, c.Detail)
	}
	return c
}

// behStderrLine returns r's stderr as one trimmed line, failing the case if the
// CLI printed more than one: an error is one "Error: ..." sentence, not a
// stack of wrapped fragments or a usage dump.
func behStderrLine(t *testing.T, r result) string {
	t.Helper()
	line := strings.TrimSpace(r.stderr)
	if strings.Count(line, "\n") > 0 {
		t.Errorf("want a single error line on stderr: %s", r)
	}
	if !strings.HasPrefix(line, "Error: ") {
		t.Errorf("want stderr to start with \"Error: \": %s", r)
	}
	return line
}
