package suite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// cinc binary, built once per test process from the repository root module.
// Building from the root (go build -C), not from this module, keeps the
// binary's dependency versions exactly those of a release build: this
// module's go.mod also requires cinc-server-ng, and minimal version
// selection would otherwise let that raise shared dependencies.
var (
	binaryOnce sync.Once
	binaryPath string
	binaryErr  error
)

// repoRoot is the cinc-cli repository root, two directories above this file.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate the suite source directory")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), nil
}

// cincBinary returns the path to the cinc binary, building it on first use.
// Set CINC_BIN to test an already-built binary instead (a release archive,
// say).
func cincBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		if p := os.Getenv("CINC_BIN"); p != "" {
			binaryPath, binaryErr = filepath.Abs(p)
			return
		}
		root, err := repoRoot()
		if err != nil {
			binaryErr = err
			return
		}
		dir, err := os.MkdirTemp("", "cinc-integration-*")
		if err != nil {
			binaryErr = err
			return
		}
		out := filepath.Join(dir, "cinc")
		cmd := exec.Command("go", "build", "-C", root, "-o", out, "./apps/cinc")
		if b, err := cmd.CombinedOutput(); err != nil {
			binaryErr = fmt.Errorf("building cinc: %v\n%s", err, b)
			return
		}
		binaryPath = out
	})
	if binaryErr != nil {
		t.Fatal(binaryErr)
	}
	return binaryPath
}

// cli runs the cinc binary for one case, in an isolated HOME whose
// ~/.cinc/credentials has a "default" profile for Target.Org and an "other"
// profile for Target.OtherOrg, both signing as Target.Admin. Nothing reads
// the developer's own ~/.cinc or ~/.chef.
type cli struct {
	t    *testing.T
	tgt  Target
	bin  string
	home string
	env  []string // extra KEY=VALUE pairs for every run
}

func newCLI(t *testing.T, tgt Target) *cli {
	t.Helper()
	c := &cli{t: t, tgt: tgt, bin: cincBinary(t), home: t.TempDir()}
	c.addProfile("default", tgt.Org, tgt.Admin, tgt.KeyPath)
	c.addProfile("other", tgt.OtherOrg, tgt.Admin, tgt.KeyPath)
	if tgt.CACertPath != "" {
		// The default trusted_certs_dir, so every profile trusts the
		// target's CA without naming it.
		pem, err := os.ReadFile(tgt.CACertPath)
		if err != nil {
			t.Fatalf("read CA certificate: %v", err)
		}
		writeFile(t, filepath.Join(c.home, ".cinc", "trusted_certs", "ca.pem"), string(pem))
	}
	return c
}

// credentialsPath is the cinc credentials file in this case's HOME.
func (c *cli) credentialsPath() string {
	return filepath.Join(c.home, ".cinc", "credentials")
}

// addProfile appends a profile signing as name with the key at keyPath
// against org, so a case can act as a second user or client.
func (c *cli) addProfile(profile, org, name, keyPath string) {
	c.t.Helper()
	path := c.credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		c.t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		c.t.Fatal(err)
	}
	_, err = fmt.Fprintf(f, "[%s]\ncinc_server_url = %q\nclient_name = %q\nclient_key = %q\n\n",
		profile, c.orgURL(org), name, keyPath)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		c.t.Fatal(err)
	}
}

// orgURL is the server URL of org, as a credentials file names it.
func (c *cli) orgURL(org string) string {
	return strings.TrimRight(c.tgt.ServerURL, "/") + "/organizations/" + org
}

// result is one finished run of the binary.
type result struct {
	args           []string
	stdout, stderr string
	exitCode       int
}

func (r result) String() string {
	return fmt.Sprintf("cinc %s (exit %d)\nstdout:\n%s\nstderr:\n%s",
		strings.Join(r.args, " "), r.exitCode, r.stdout, r.stderr)
}

// runOpts adjusts a single run.
type runOpts struct {
	stdin string
	env   []string
}

// exec runs the binary and returns what happened, whatever the exit code.
func (c *cli) exec(opts runOpts, args ...string) result {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.home
	cmd.Env = append(append(c.baseEnv(), c.env...), opts.env...)
	cmd.Stdin = strings.NewReader(opts.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{args: args, stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		r.exitCode = ee.ExitCode()
	case err != nil:
		c.t.Fatalf("running cinc %s: %v", strings.Join(args, " "), err)
	}
	return r
}

// baseEnv is the parent environment with HOME replaced and every CINC_ and
// CHEF_ variable removed, so a developer's CINC_PROFILE never leaks in.
func (c *cli) baseEnv() []string {
	env := []string{"HOME=" + c.home, "NO_COLOR=1"}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "HOME", k == "NO_COLOR",
			strings.HasPrefix(k, "CINC_"), strings.HasPrefix(k, "CHEF_"):
			continue
		}
		env = append(env, kv)
	}
	return env
}

// run runs the binary, fails the case on a non-zero exit, and returns stdout.
func (c *cli) run(args ...string) string {
	c.t.Helper()
	return c.runWith(runOpts{}, args...)
}

// runWith is run with stdin and extra environment.
func (c *cli) runWith(opts runOpts, args ...string) string {
	c.t.Helper()
	r := c.exec(opts, args...)
	if r.exitCode != 0 {
		c.t.Fatalf("unexpected failure: %s", r)
	}
	return r.stdout
}

// fail runs the binary, fails the case if it exits zero, and returns the
// result so the case can check the error message.
func (c *cli) fail(args ...string) result {
	c.t.Helper()
	r := c.exec(runOpts{}, args...)
	if r.exitCode == 0 {
		c.t.Fatalf("expected a failure, got success: %s", r)
	}
	return r
}

// json runs the binary with --format json and decodes stdout into v.
func (c *cli) json(v any, args ...string) {
	c.t.Helper()
	out := c.run(append(args, "--format", "json")...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		c.t.Fatalf("cinc %s --format json: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// cleanup registers a CLI delete to run when the case ends. A delete that
// finds the object already gone is fine; any other failure fails the case,
// because a leftover object on a long-lived server is a bug worth seeing.
func (c *cli) cleanup(args ...string) {
	c.t.Helper()
	c.t.Cleanup(func() {
		r := c.exec(runOpts{}, args...)
		if r.exitCode != 0 && !isNotFound(r) {
			c.t.Errorf("cleanup failed: %s", r)
		}
	})
}

// isNotFound reports whether a failed run is the CLI's not-found error.
func isNotFound(r result) bool {
	low := strings.ToLower(r.stderr)
	return strings.Contains(low, "not found") || strings.Contains(low, "couldn't find") ||
		strings.Contains(low, "cannot find") || strings.Contains(low, "404")
}

// wantNotFound fails the case unless r is the CLI's not-found error.
func wantNotFound(t *testing.T, r result) {
	t.Helper()
	if r.exitCode == 0 || !isNotFound(r) {
		t.Fatalf("want a not-found error: %s", r)
	}
}
