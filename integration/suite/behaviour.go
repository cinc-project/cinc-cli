package suite

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// cliFamily covers CLI behaviour rather than one noun: config create and
// validate against a live server, first-run setup and ~/.chef/credentials
// migration, profile selection, the chef-compat keys and variables, the
// invoked program name, and the shape of the errors every noun shares (401,
// 403, 404, 409) and of --format. A noun's own CRUD, including its
// not-found and already-exists cases, lives in that noun's family; the
// matrices here only assert what must hold across all of them.
var cliFamily = family{cases: []testCase{
	{"cli/config-validate-live", []string{"config validate"}, testConfigValidateLive},
	{"cli/config-validate-auth-failures", []string{"config validate"}, testConfigValidateAuthFailures},
	{"cli/config-validate-wrong-org", []string{"config validate", "node list"}, testConfigValidateWrongOrg},
	{"cli/config-validate-unreachable", []string{"config validate"}, testConfigValidateUnreachable},
	{"cli/config-validate-local-failures", []string{"config validate"}, testConfigValidateLocalFailures},
	{"cli/config-validate-non-admin-client", []string{"config validate"}, testConfigValidateNonAdminClient},
	{"cli/trusted-certs-dir", []string{"config validate", "node list"}, testTrustedCertsDir},
	{"cli/trusted-certs-dir-missing", []string{"config validate", "node list"}, testTrustedCertsDirMissing},
	{"cli/trusted-certs-chef-fallback", []string{"config validate", "node list"}, testTrustedCertsChefFallback},
	{"cli/tls-untrusted", []string{"config validate", "node list"}, testTLSUntrusted},
	{"cli/tls-verify-none", []string{"config validate", "node list"}, testTLSVerifyNone},
	{"cli/client-key-tilde", []string{"config validate", "node list"}, testClientKeyTilde},
	{"cli/config-create-then-use", []string{"config create", "config validate", "node create", "node show"}, testConfigCreateThenUse},
	{"cli/config-create-updates-in-place", []string{"config create", "node list"}, testConfigCreateUpdatesInPlace},
	{"cli/config-create-rejects-incomplete", []string{"config create"}, testConfigCreateRejectsIncomplete},
	{"cli/config-create-server-url-without-org", []string{"config create"}, testConfigCreateServerURLWithoutOrg},
	{"cli/config-create-interactive", []string{"config create", "node list"}, testConfigCreateInteractive},
	{"cli/config-create-interactive-update", []string{"config create", "node list"}, testConfigCreateInteractiveUpdate},
	{"cli/first-run-migrates-chef-credentials", []string{"node list", "node create", "node show"}, testFirstRunMigrates},
	{"cli/first-run-migration-keeps-knife-keys", []string{"node list"}, testFirstRunMigrationKeepsKnifeKeys},
	{"cli/first-run-declined", []string{"node list"}, testFirstRunDeclined},
	{"cli/first-run-configure", []string{"node list"}, testFirstRunConfigure},
	{"cli/first-run-needs-a-terminal", []string{"node list"}, testFirstRunNeedsATerminal},
	{"cli/first-run-bare-cinc", []string{"node list"}, testFirstRunBareCinc},
	{"cli/first-run-other-entry-points", []string{"explore"}, testFirstRunOtherEntryPoints},
	{"cli/first-run-never-offered", []string{"config create", "config validate", "node list"}, testFirstRunNeverOffered},
	{"cli/first-run-gate-eof", []string{"node list"}, testFirstRunGateEOF},
	{"cli/chef-credentials-in-place", []string{"node list", "node create"}, testChefCredentialsInPlace},
	{"cli/chef-server-url-key", []string{"node list"}, testChefServerURLKey},
	{"cli/profile-precedence", []string{"node list"}, testProfilePrecedence},
	{"cli/multi-org-isolation", []string{"node create", "node list", "node show", "node delete"}, testMultiOrgIsolation},
	{"cli/auth-wrong-key", []string{"node list", "node create", "node show"}, testAuthWrongKey},
	{"cli/auth-unknown-client", []string{"node list"}, testAuthUnknownClient},
	// The matrices below also run other nouns' commands. Their covers name
	// only commands whose family is ported, so they never claim one another
	// family still lists as pending.
	{"cli/forbidden-shape", []string{"node list", "node delete", "node edit"}, testForbiddenShape},
	{"cli/format-rejected-before-acting", []string{"node list", "node create", "config create", "version"}, testFormatRejectedBeforeActing},
	{"cli/list-format-matrix", []string{"node list"}, testListFormatMatrix},
	{"cli/not-found-shape", []string{"node show", "node delete"}, testNotFoundShape},
	{"cli/conflict-shape", []string{"node create"}, testConflictShape},
	{"cli/version", []string{"version"}, testVersion},
	{"cli/renamed-binary", []string{"version", "config create", "node list"}, testRenamedBinary},
	{"cli/renamed-binary-unsafe-name", []string{"version"}, testRenamedBinaryUnsafeName},
}}

// Check names config validate reports, as the user reads them.
const (
	checkReachable    = "Server is reachable"
	checkKeyReadable  = "Client key file is readable"
	checkTrustedCerts = "Trusted certificates load"
	checkServerURL    = "Server URL is valid"
	checkSSLMode      = "ssl_verify_mode is valid"
	checkClientName   = "Client name is configured"
	checkTOML         = "Credentials file is valid TOML"
)

// deadURL is a server URL nothing listens on: port 1 on loopback refuses the
// connection at once, wherever the suite runs.
const deadURL = "http://127.0.0.1:1/organizations/nobody"

// testConfigValidateLive validates the case's own credentials, whose
// profiles both reach the target, in human and JSON form and with the path
// given positionally.
func testConfigValidateLive(t *testing.T, tgt Target, c *cli) {
	human := c.run("config", "validate")
	for _, want := range []string{
		"Config " + c.credentialsPath() + " is valid",
		"✓ " + checkTOML,
		"default profile [VALID]",
		"other profile [VALID]",
		"✓ " + checkReachable,
		// No supermarket_site is configured, so the check passes with a note
		// naming the public Supermarket the CLI falls back to.
		"✓ Supermarket site URL is valid: using the default https://supermarket.chef.io",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("config validate output missing %q:\n%s", want, human)
		}
	}

	rep, _ := c.validate(c.credentialsPath())
	wantEqual(t, "valid", rep.Valid, true)
	wantEqual(t, "path", rep.Path, c.credentialsPath())
	for _, name := range []string{"default", "other"} {
		p := rep.profile(t, name)
		wantEqual(t, name+" valid", p.Valid, true)
		p.wantCheck(t, checkReachable, true)
		p.wantCheck(t, checkKeyReadable, true)
		// The trusted-certs check runs only when a directory is set or
		// found at a default location: the suite writes the target's CA to
		// ~/.cinc/trusted_certs, and writes nothing for a plain-HTTP target.
		if tgt.CACertPath == "" {
			if _, ran := p.check(checkTrustedCerts); ran {
				t.Errorf("%s: %q ran with no trusted certificates anywhere: %+v", name, checkTrustedCerts, p.Checks)
			}
		} else {
			tc := p.wantCheck(t, checkTrustedCerts, true)
			if !strings.Contains(tc.Detail, "trusting 1 certificate file from "+filepath.Join(c.home, ".cinc", "trusted_certs")) {
				t.Errorf("%s: %q detail = %q", name, checkTrustedCerts, tc.Detail)
			}
		}
	}
}

// testConfigValidateAuthFailures checks that the reachability check signs a
// real request: a registered name with the wrong key and a name the server
// has never heard of both reach the server and are refused with a 401,
// which validate reports against the right profile.
func testConfigValidateAuthFailures(t *testing.T, tgt Target, c *cli) {
	key := behFreshKey(t)
	ghost := uniqueName(t, "ghost")
	c.addProfile("wrongkey", tgt.Org, tgt.Admin, key)
	c.addProfile("ghost", tgt.Org, ghost, key)

	rep, r := c.validate()
	wantEqual(t, "valid", rep.Valid, false)
	rep.profile(t, "default").wantCheck(t, checkReachable, true)
	for _, name := range []string{"wrongkey", "ghost"} {
		p := rep.profile(t, name)
		wantEqual(t, name+" valid", p.Valid, false)
		// The key parses; only the server can tell it is the wrong one.
		p.wantCheck(t, checkKeyReadable, true)
		detail := p.wantCheck(t, checkReachable, false).Detail
		if !hasStatus(detail, 401) {
			t.Errorf("%s: reachability detail should report the 401: %q\n%s", name, detail, r)
		}
	}
	if detail := rep.profile(t, "ghost").wantCheck(t, checkReachable, false).Detail; !strings.Contains(detail, ghost) {
		t.Errorf("ghost: the 401 should name the client the server did not recognise: %q", detail)
	}

	human := c.exec(runOpts{}, "config", "validate")
	for _, want := range []string{"is invalid!", "wrongkey profile [INVALID]", "ghost profile [INVALID]", "default profile [VALID]", "✗ " + checkReachable + ": "} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("config validate human output missing %q:\n%s", want, human)
		}
	}
}

// testConfigValidateWrongOrg points a profile at an organization that does
// not exist on a live server.
func testConfigValidateWrongOrg(t *testing.T, tgt Target, c *cli) {
	org := uniqueName(t, "org")
	c.addProfile("wrongorg", org, tgt.Admin, tgt.KeyPath)

	rep, _ := c.validate()
	p := rep.profile(t, "wrongorg")
	wantEqual(t, "wrongorg valid", p.Valid, false)
	p.wantCheck(t, checkServerURL, true)
	if detail := p.wantCheck(t, checkReachable, false).Detail; !hasStatus(detail, 404) {
		t.Errorf("reachability detail for a missing org should report the 404: %q", detail)
	}

	r := c.fail("node", "list", "--profile", "wrongorg")
	line := behStderrLine(t, r)
	if !isNotFound(r) || !strings.Contains(line, org) {
		t.Errorf("node list against a missing org should say the org was not found and name it: %s", r)
	}
}

func testConfigValidateUnreachable(t *testing.T, tgt Target, c *cli) {
	behAppendProfile(t, c.credentialsPath(), "dead", behWith(c.adminFields(tgt.Org), "cinc_server_url", deadURL))

	rep, _ := c.validate()
	rep.profile(t, "default").wantCheck(t, checkReachable, true)
	p := rep.profile(t, "dead")
	wantEqual(t, "dead valid", p.Valid, false)
	p.wantCheck(t, checkServerURL, true)
	if detail := p.wantCheck(t, checkReachable, false).Detail; !strings.Contains(detail, "127.0.0.1:1") {
		t.Errorf("reachability detail should name the address it could not reach: %q", detail)
	}
}

// testConfigValidateLocalFailures covers the structural checks, each in its
// own profile so one failure never hides another, plus a file that is not
// TOML at all. None of these profiles should reach for the network.
func testConfigValidateLocalFailures(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	base := c.adminFields(tgt.Org)
	behAppendProfile(t, path, "good", base)
	behAppendProfile(t, path, "nokeyfile", behWith(base, "client_key", filepath.Join(t.TempDir(), "missing.pem")))
	behAppendProfile(t, path, "noorg", behWith(base, "cinc_server_url", c.tgt.ServerURL))
	behAppendProfile(t, path, "badssl", behWith(base, "ssl_verify_mode", "verify_peer"))
	behAppendProfile(t, path, "noname", behWith(base, "client_name", ""))

	rep, _ := c.validate(path)
	wantEqual(t, "valid", rep.Valid, false)
	wantEqual(t, "good valid", rep.profile(t, "good").Valid, true)

	p := rep.profile(t, "nokeyfile")
	p.wantCheck(t, checkKeyReadable, false)
	wantEqual(t, "nokeyfile valid", p.Valid, false)

	p = rep.profile(t, "noorg")
	if detail := p.wantCheck(t, checkServerURL, false).Detail; !strings.Contains(detail, "/organizations/") {
		t.Errorf("a URL without an org should say it needs /organizations/<org>: %q", detail)
	}
	if _, ran := p.check(checkReachable); ran {
		t.Errorf("noorg: reachability should not run without a usable server URL: %+v", p.Checks)
	}

	p = rep.profile(t, "badssl")
	if detail := p.wantCheck(t, checkSSLMode, false).Detail; !strings.Contains(detail, ":verify_peer") {
		t.Errorf("a bad ssl_verify_mode should name the accepted values: %q", detail)
	}

	rep.profile(t, "noname").wantCheck(t, checkClientName, false)

	broken := filepath.Join(t.TempDir(), "broken")
	writeFile(t, broken, "[default\nclient_name = ")
	rep, _ = c.validate(broken)
	wantEqual(t, "broken valid", rep.Valid, false)
	if len(rep.TopLevel) == 0 || rep.TopLevel[0].Name != checkTOML || rep.TopLevel[0].Passed {
		t.Errorf("a file that is not TOML should fail %q first: %+v", checkTOML, rep.TopLevel)
	}
}

// testConfigValidateNonAdminClient validates a profile signing as a plain
// client, the identity a node's own credentials carry. Being authenticated
// is all "reachable" should mean; the client's ACLs are not validate's
// business.
func testConfigValidateNonAdminClient(t *testing.T, _ Target, c *cli) {
	behCreateRobot(c, "robot")
	rep, _ := c.validate()
	p := rep.profile(t, "robot")
	p.wantCheck(t, checkReachable, true)
	wantEqual(t, "robot valid", p.Valid, true)
}

// testTrustedCertsDir sets trusted_certs_dir explicitly, with a leading ~,
// to a directory holding a CA certificate and a file that is not one. The
// junk file is a warning, never a failure, and the connection still works.
func testTrustedCertsDir(t *testing.T, tgt Target, c *cli) {
	dir := filepath.Join(c.home, "certs")
	writeFile(t, filepath.Join(dir, "ca.pem"), behCACertPEM(t, tgt))
	writeFile(t, filepath.Join(dir, "junk.pem"), "this is not a certificate\n")
	writeFile(t, filepath.Join(dir, "README.txt"), "ignored: not *.crt or *.pem\n")
	behAppendProfile(t, c.credentialsPath(), "certs", behWith(c.adminFields(tgt.Org), "trusted_certs_dir", "~/certs"))

	rep, _ := c.validate()
	p := rep.profile(t, "certs")
	wantEqual(t, "certs valid", p.Valid, true)
	tc := p.wantCheck(t, checkTrustedCerts, true)
	wantEqual(t, "trusted certs warns", tc.Warn, true)
	if !strings.Contains(tc.Detail, "junk.pem") || !strings.Contains(tc.Detail, dir) || strings.Contains(tc.Detail, "README") {
		t.Errorf("trusted certs detail should name the unreadable junk.pem in %s and nothing else: %q", dir, tc.Detail)
	}
	p.wantCheck(t, checkReachable, true)

	human := c.run("config", "validate")
	if !strings.Contains(human, "✓ "+checkTrustedCerts+": we skipped 1 file in "+dir) {
		t.Errorf("config validate should show the skipped file as a warning:\n%s", human)
	}
	c.run("node", "list", "--profile", "certs")
}

// testTrustedCertsDirMissing names a trusted_certs_dir that does not exist.
// An explicit setting is an instruction, so every server command refuses to
// run rather than quietly trusting only the system certificates.
func testTrustedCertsDirMissing(t *testing.T, tgt Target, c *cli) {
	dir := filepath.Join(c.home, "no-such-certs")
	behAppendProfile(t, c.credentialsPath(), "nocerts", behWith(c.adminFields(tgt.Org), "trusted_certs_dir", dir))

	rep, _ := c.validate()
	p := rep.profile(t, "nocerts")
	wantEqual(t, "nocerts valid", p.Valid, false)
	if detail := p.wantCheck(t, checkTrustedCerts, false).Detail; !strings.Contains(detail, dir) {
		t.Errorf("trusted certs failure should name the missing directory: %q", detail)
	}

	r := c.fail("node", "list", "--profile", "nocerts")
	if line := behStderrLine(t, r); !strings.Contains(line, dir) || !strings.Contains(line, "trusted_certs_dir") {
		t.Errorf("node list should say which trusted_certs_dir is missing: %s", r)
	}
}

// testTrustedCertsChefFallback leaves trusted_certs_dir unset in a HOME that
// has only ~/.chef/trusted_certs, where `knife ssl fetch` saves
// certificates. The CLI must find and use it, so on a TLS target this is
// what lets the connection verify at all.
func testTrustedCertsChefFallback(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	dir := filepath.Join(b.home, ".chef", "trusted_certs")
	writeFile(t, filepath.Join(dir, "server.crt"), behCACertPEM(t, tgt))
	b.addProfile("default", tgt.Org, tgt.Admin, tgt.KeyPath)

	rep, _ := b.validate()
	p := rep.profile(t, "default")
	wantEqual(t, "default valid", p.Valid, true)
	tc := p.wantCheck(t, checkTrustedCerts, true)
	if !strings.Contains(tc.Detail, "trusting 1 certificate file from "+dir) {
		t.Errorf("trusted certs should come from %s: %q", dir, tc.Detail)
	}
	p.wantCheck(t, checkReachable, true)
	b.run("node", "list")
}

// testTLSUntrusted runs with no CA trusted but the system's, and with a
// trusted_certs_dir holding the wrong CA. Against a TLS target both must be
// refused: trusting an extra CA adds to what verifies, it never turns
// verification off. A plain-HTTP target has no certificate to verify, so
// there both connect.
func testTLSUntrusted(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	b.addProfile("default", tgt.Org, tgt.Admin, tgt.KeyPath)
	wrong := filepath.Join(b.home, "wrong-ca")
	writeFile(t, filepath.Join(wrong, "ca.pem"), behCACertPEM(t, Target{}))
	behAppendProfile(t, b.credentialsPath(), "wrongca", behWith(b.adminFields(tgt.Org), "trusted_certs_dir", wrong))

	rep, _ := b.validate()
	for _, name := range []string{"default", "wrongca"} {
		p := rep.profile(t, name)
		reach, _ := p.check(checkReachable)
		if tgt.CACertPath == "" {
			wantEqual(t, name+" reachable over plain HTTP", reach.Passed, true)
			b.run("node", "list", "--profile", name)
			continue
		}
		wantEqual(t, name+" reachable without the server's CA", reach.Passed, false)
		// Both say which knob fixes it, not just that x509 failed.
		if !strings.Contains(reach.Detail, "certificate") || !strings.Contains(reach.Detail, "trusted_certs") {
			t.Errorf("%s: reachability should blame the certificate and point at trusted_certs_dir: %q", name, reach.Detail)
		}
		r := b.fail("node", "list", "--profile", name)
		if line := behStderrLine(t, r); !strings.Contains(line, "certificate") || !strings.Contains(line, "trusted_certs") {
			t.Errorf("%s: node list should blame the certificate and point at trusted_certs_dir: %s", name, r)
		}
	}
}

// testTLSVerifyNone turns verification off with ssl_verify_mode =
// ":verify_none" and trusts nothing: the connection works, every server
// command warns on stderr that it is unauthenticated, and config validate
// reports the insecure mode as a warning in its own check instead of
// printing that warning line.
func testTLSVerifyNone(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	behAppendProfile(t, b.credentialsPath(), "default", behWith(b.adminFields(tgt.Org), "ssl_verify_mode", ":verify_none"))

	r := b.exec(runOpts{}, "node", "list", "--format", "json")
	if r.exitCode != 0 {
		t.Fatalf("node list with :verify_none failed: %s", r)
	}
	if !json.Valid([]byte(r.stdout)) {
		t.Errorf("the warning must stay off stdout, which should be JSON: %s", r)
	}
	if !strings.Contains(r.stderr, "TLS certificate verification is disabled") {
		t.Errorf("node list with :verify_none should warn on stderr: %s", r)
	}

	rep, vr := b.validate()
	p := rep.profile(t, "default")
	wantEqual(t, "valid", p.Valid, true)
	if ssl := p.wantCheck(t, checkSSLMode, true); !ssl.Warn {
		t.Errorf("%q should warn about :verify_none: %+v", checkSSLMode, ssl)
	}
	p.wantCheck(t, checkReachable, true)
	if strings.Contains(vr.stderr, "TLS certificate verification is disabled") {
		t.Errorf("config validate reports TLS posture as a check, not a warning line: %s", vr)
	}
}

// testClientKeyTilde writes a profile by hand whose client_key starts with
// ~/, which docs/configuration.md promises is expanded to the home
// directory. `config create` expands it before writing, but a hand-written
// or knife-written file keeps the ~.
func testClientKeyTilde(t *testing.T, tgt Target, c *cli) {
	behCopyFile(t, tgt.KeyPath, filepath.Join(c.home, "keys", "admin.pem"))
	c.addProfile("tilde", tgt.Org, tgt.Admin, "~/keys/admin.pem")

	rep, _ := c.validate()
	p := rep.profile(t, "tilde")
	p.wantCheck(t, checkKeyReadable, true)
	p.wantCheck(t, checkReachable, true)

	c.run("node", "list", "--profile", "tilde")
}

// testConfigCreateThenUse writes a profile non-interactively, with every
// value a flag, then proves the file works by creating a node through it.
func testConfigCreateThenUse(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	key := filepath.Join(c.home, ".cinc", "staging.pem")
	behCopyFile(t, tgt.KeyPath, key)

	out := c.run("config", "create", "--config", path, "--profile", "staging",
		"--cinc-server-url", c.orgURL(tgt.Org), "--client-name", tgt.Admin,
		"--client-key", "~/.cinc/staging.pem", "--ssl-verify-mode", ":verify_peer")
	if !strings.Contains(out, fmt.Sprintf("Wrote credentials profile %q to %s", "staging", path)) {
		t.Errorf("config create output = %q", out)
	}
	written := behReadFile(t, path)
	for _, want := range []string{
		fmt.Sprintf("cinc_server_url = %q", c.orgURL(tgt.Org)),
		fmt.Sprintf("client_name = %q", tgt.Admin),
		// config create expands ~ before it writes.
		fmt.Sprintf("client_key = %q", key),
		`ssl_verify_mode = ":verify_peer"`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("written credentials missing %s:\n%s", want, written)
		}
	}
	if strings.Contains(written, "chef_server_url") {
		t.Errorf("a new profile should carry only the cinc-canonical server URL key:\n%s", written)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("credentials file mode = %v, want it private to the user", info.Mode().Perm())
	}

	rep, _ := c.validate(path)
	wantEqual(t, "created profile valid", rep.profile(t, "staging").Valid, true)

	name := uniqueName(t, "node")
	c.run("node", "create", name, "--config", path, "--profile", "staging")
	c.cleanup("node", "delete", name)
	wantEqual(t, "node created through the new profile", showNode(c, name).Name, name)
}

// testConfigCreateUpdatesInPlace updates a profile in a file shared with
// knife. Keys cinc does not model (knife's node_name, validation settings, a
// nested knife table) and every other profile must survive, and the
// chef_server_url knife reads must follow the new URL rather than vanish.
func testConfigCreateUpdatesInPlace(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	writeFile(t, path, fmt.Sprintf(`[default]
chef_server_url = %q
client_name = "someone-else"
client_key = %q
node_name = "knife-node"
validation_client_name = "acme-validator"
validation_key = "/etc/chef/validation.pem"
secret_file = "/etc/chef/encrypted_data_bag_secret"
knife = { ssh_user = "ubuntu" }

[keep]
cinc_server_url = %q
client_name = "keeper"
client_key = "/keys/keeper.pem"
`, deadURL, tgt.KeyPath, c.orgURL(tgt.OtherOrg)))

	c.run("config", "create", "--config", path, "--profile", "default",
		"--server-url", c.orgURL(tgt.Org), "--client-name", tgt.Admin, "--client-key", tgt.KeyPath)

	written := behReadFile(t, path)
	for _, want := range []string{
		fmt.Sprintf("cinc_server_url = %q", c.orgURL(tgt.Org)),
		fmt.Sprintf("chef_server_url = %q", c.orgURL(tgt.Org)),
		fmt.Sprintf("client_name = %q", tgt.Admin),
		`node_name = "knife-node"`,
		`validation_client_name = "acme-validator"`,
		`validation_key = "/etc/chef/validation.pem"`,
		`secret_file = "/etc/chef/encrypted_data_bag_secret"`,
		`ssh_user = "ubuntu"`,
		"[keep]",
		`client_name = "keeper"`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("updated credentials missing %s:\n%s", want, written)
		}
	}
	if strings.Contains(written, deadURL) || strings.Contains(written, "someone-else") {
		t.Errorf("the old URL and client name should be gone:\n%s", written)
	}
	c.run("node", "list", "--config", path)
}

// testConfigCreateRejectsIncomplete leaves out a required flag; nothing may
// be written.
func testConfigCreateRejectsIncomplete(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--server-url", c.orgURL(tgt.Org), "--client-key", tgt.KeyPath}, "--client-name"},
		{[]string{"--server-url", c.orgURL(tgt.Org), "--client-name", tgt.Admin}, "--client-key"},
	} {
		r := c.fail(append([]string{"config", "create", "--config", path}, tc.args...)...)
		if line := behStderrLine(t, r); !strings.Contains(line, tc.want) {
			t.Errorf("config create should say %s is required: %s", tc.want, r)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("a rejected config create wrote %s:\n%s", path, behReadFile(t, path))
		}
	}
}

// testConfigCreateServerURLWithoutOrg passes --server-url without the
// /organizations/<org> segment the flag's help asks for, the most likely
// typo there is. It has to be refused with a pointer at the missing
// segment; filing the URL away as a Supermarket site instead leaves a
// profile no server command can use.
func testConfigCreateServerURLWithoutOrg(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	r := c.exec(runOpts{}, "config", "create", "--config", path,
		"--server-url", c.tgt.ServerURL, "--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	if r.exitCode == 0 {
		t.Fatalf("config create accepted a server URL without /organizations/<org> and wrote:\n%s", behReadFile(t, path))
	}
	if line := behStderrLine(t, r); !strings.Contains(line, "/organizations/") {
		t.Errorf("config create should point at the missing /organizations/<org>: %s", r)
	}
}

// testConfigCreateInteractive answers every prompt of `config create` on
// stdin, writing a new file. A user pasting the server's URL, scheme and
// port included, at the host prompt is the natural answer, and the only one
// that can describe a plain-HTTP or non-443 server.
func testConfigCreateInteractive(t *testing.T, tgt Target, c *cli) {
	path := filepath.Join(t.TempDir(), "credentials")
	answers := strings.Join([]string{
		path,          // Credentials file location
		"lab",         // Profile name
		"",            // Supermarket site: accept the default
		tgt.Admin,     // Client name
		tgt.KeyPath,   // Client key path
		tgt.ServerURL, // Server host
		tgt.Org,       // Organization
		"",            // SSL verify mode: accept :verify_peer
	}, "\n") + "\n"
	r := c.exec(runOpts{stdin: answers}, "config", "create")
	if r.exitCode != 0 {
		t.Fatalf("interactive config create failed: %s", r)
	}
	if !strings.Contains(r.stdout, fmt.Sprintf("Wrote credentials profile %q to %s", "lab", path)) {
		t.Errorf("config create output: %s", r)
	}
	if written := behReadFile(t, path); !strings.Contains(written, fmt.Sprintf("cinc_server_url = %q", c.orgURL(tgt.Org))) {
		t.Errorf("interactive config create should write the URL it was given:\n%s", written)
	}
	c.run("node", "list", "--config", path, "--profile", "lab")
}

// testConfigCreateInteractiveUpdate picks "update an existing profile" and
// accepts every default. Accepting the defaults must leave a working
// profile working, whatever its URL's scheme and port.
func testConfigCreateInteractiveUpdate(t *testing.T, tgt Target, c *cli) {
	answers := strings.Join([]string{
		"",  // Credentials file location: ~/.cinc/credentials
		"2", // Update an existing profile
		"1", // default
		"", "", "", "", "", "",
	}, "\n") + "\n"
	r := c.exec(runOpts{stdin: answers}, "config", "create")
	if r.exitCode != 0 {
		t.Fatalf("interactive config update failed: %s", r)
	}
	written := behReadFile(t, c.credentialsPath())
	if !strings.Contains(written, fmt.Sprintf("cinc_server_url = %q", c.orgURL(tgt.Org))) {
		t.Errorf("accepting every default should keep the server URL %s:\n%s", c.orgURL(tgt.Org), written)
	}
	c.run("node", "list")
}

// behChefCredentials writes ~/.chef/credentials in b's HOME, with its key
// alongside as knife keeps it, and trusts the target's CA where knife ssl
// fetch would have put it. extra is appended verbatim.
func behChefCredentials(t *testing.T, b *cli, extra string) {
	t.Helper()
	key := filepath.Join(b.home, ".chef", "admin.pem")
	behCopyFile(t, b.tgt.KeyPath, key)
	if b.tgt.CACertPath != "" {
		behCopyFile(t, b.tgt.CACertPath, filepath.Join(b.home, ".chef", "trusted_certs", "ca.pem"))
	}
	writeFile(t, filepath.Join(b.home, ".chef", "credentials"), fmt.Sprintf(`[default]
chef_server_url = %q
client_name = %q
client_key = %q
%s`, b.orgURL(b.tgt.Org), b.tgt.Admin, key, extra))
}

// testFirstRunMigrates runs a server command in a HOME that has only
// ~/.chef/credentials, on a terminal, and accepts both first-run prompts.
// The first run writes ~/.cinc/credentials and stops without running the
// command; the next run works through the migrated profile.
func testFirstRunMigrates(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, "")

	r := b.execTTY("y\ny\n", "node", "list")
	if r.exitCode != 0 {
		t.Fatalf("first run with migration failed: %s", r)
	}
	for _, want := range []string{"Welcome to", "first time using Cinc", "migrate it", "Wrote 1 profile"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("first-run stderr missing %q: %s", want, r)
		}
	}
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("the first run must not go on to run node list: %s", r)
	}
	written := behReadFile(t, b.credentialsPath())
	if !strings.Contains(written, fmt.Sprintf("cinc_server_url = %q", b.orgURL(b.tgt.Org))) || strings.Contains(written, "chef_server_url") {
		t.Errorf("migration should turn chef_server_url into cinc_server_url:\n%s", written)
	}

	name := uniqueName(t, "node")
	b.run("node", "create", name)
	c.cleanup("node", "delete", name)
	wantEqual(t, "node created through the migrated profile", showNode(c, name).Name, name)
}

// testFirstRunMigrationKeepsKnifeKeys migrates a knife credentials file whose
// profiles carry knife-only keys. docs/migrating-from-chef.md promises every
// key in each profile carries over, so the knife settings the user still
// needs are not silently dropped.
func testFirstRunMigrationKeepsKnifeKeys(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, fmt.Sprintf(`node_name = "knife-node"
validation_client_name = "acme-validator"
validation_key = "/etc/chef/validation.pem"
knife = { ssh_user = "ubuntu" }

[other]
chef_server_url = %q
client_name = %q
client_key = %q
ssl_verify_mode = ":verify_peer"
`, b.orgURL(tgt.OtherOrg), tgt.Admin, filepath.Join(b.home, ".chef", "admin.pem")))

	r := b.execTTY("y\ny\n", "node", "list")
	if r.exitCode != 0 {
		t.Fatalf("first run with migration failed: %s", r)
	}
	written := behReadFile(t, b.credentialsPath())
	for _, want := range []string{
		`node_name = "knife-node"`,
		`validation_client_name = "acme-validator"`,
		`validation_key = "/etc/chef/validation.pem"`,
		`ssh_user = "ubuntu"`,
		`ssl_verify_mode = ":verify_peer"`,
		"[other]",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("migrated credentials dropped %s:\n%s", want, written)
		}
	}
	b.run("node", "list")
	b.run("node", "list", "--profile", "other")
}

// testFirstRunDeclined says no, first at the setup gate and then at the
// migration prompt. Either way nothing is written and the command explains
// how to set up later.
func testFirstRunDeclined(t *testing.T, _ Target, c *cli) {
	for _, answers := range []string{"n\n", "y\nn\n"} {
		b := behBareCLI(c)
		behChefCredentials(t, b, "")
		r := b.execTTY(answers, "node", "list")
		if r.exitCode == 0 {
			t.Errorf("declining setup (%q) should fail the command: %s", answers, r)
		}
		if !strings.Contains(r.stderr, "No problem") || !strings.Contains(r.stderr, behProgName(b)+" config create") {
			t.Errorf("declining setup (%q) should point at config create: %s", answers, r)
		}
		if _, err := os.Stat(b.credentialsPath()); err == nil {
			t.Errorf("declining setup (%q) wrote %s", answers, b.credentialsPath())
		}
	}
}

// testFirstRunConfigure runs a server command on a terminal in an empty
// HOME, so the first run walks through the configure prompts inline.
func testFirstRunConfigure(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	if tgt.CACertPath != "" {
		behCopyFile(t, tgt.CACertPath, filepath.Join(b.home, ".cinc", "trusted_certs", "ca.pem"))
	}
	answers := strings.Join([]string{
		"y",           // Run the interactive setup?
		"",            // Credentials file location: ~/.cinc/credentials
		"",            // Profile name: default
		"",            // Supermarket site
		tgt.Admin,     // Client name
		tgt.KeyPath,   // Client key path
		tgt.ServerURL, // Server host
		tgt.Org,       // Organization
		"",            // SSL verify mode
	}, "\n") + "\n"
	r := b.execTTY(answers, "node", "list")
	if r.exitCode != 0 {
		t.Fatalf("first-run configure failed: %s", r)
	}
	if !strings.Contains(r.stdout, "Wrote credentials profile \"default\"") {
		t.Errorf("first-run configure output: %s", r)
	}
	b.run("node", "list")
}

// testFirstRunNeedsATerminal runs a server command with no terminal in a
// HOME that has only knife's files. Nothing is migrated behind a script's
// back, knife.rb is never read, and the error says how to set up.
func testFirstRunNeedsATerminal(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, "")
	writeFile(t, filepath.Join(b.home, ".chef", "knife.rb"), "chef_server_url 'https://knife.example.test/organizations/x'\n")

	r := b.fail("node", "list")
	if line := behStderrLine(t, r); !strings.Contains(line, behProgName(b)+" config create") || !strings.Contains(line, b.credentialsPath()) {
		t.Errorf("a first run without a terminal should name the missing file and config create: %s", r)
	}
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("a first run without a terminal wrote %s", b.credentialsPath())
	}
}

// testChefCredentialsInPlace points --config at a knife credentials file,
// written with ~ so the CLI has to expand it, and uses it unchanged: the
// chef_server_url key, knife-only keys it must ignore, and knife's
// ~/.chef/trusted_certs.
func testChefCredentialsInPlace(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, "node_name = \"knife-node\"\nknife = { ssh_user = \"ubuntu\" }\n")

	b.run("node", "list", "--config", "~/.chef/credentials")
	name := uniqueName(t, "node")
	b.run("node", "create", name, "--config", "~/.chef/credentials")
	c.cleanup("node", "delete", name)
	wantEqual(t, "node created through the knife file", showNode(c, name).Name, name)
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("using --config must not create %s", b.credentialsPath())
	}
}

// testChefServerURLKey checks the chef_server_url key alone, and both keys
// together in each order: cinc_server_url wins, so the profile whose cinc
// key is dead fails even though its chef key would work.
func testChefServerURLKey(t *testing.T, tgt Target, c *cli) {
	base := behWith(c.adminFields(tgt.Org), "cinc_server_url", "")
	behAppendProfile(t, c.credentialsPath(), "chefonly", behWith(base, "chef_server_url", c.orgURL(tgt.Org)))
	behAppendProfile(t, c.credentialsPath(), "cincwins", behWith(base, "cinc_server_url", c.orgURL(tgt.Org), "chef_server_url", deadURL))
	behAppendProfile(t, c.credentialsPath(), "cincdead", behWith(base, "cinc_server_url", deadURL, "chef_server_url", c.orgURL(tgt.Org)))

	name := uniqueName(t, "node")
	createNode(c, name)
	for _, profile := range []string{"chefonly", "cincwins"} {
		var names []string
		c.json(&names, "node", "list", "--profile", profile)
		if !slices.Contains(names, name) {
			t.Errorf("node list --profile %s = %v, want it to include %s", profile, names, name)
		}
	}
	r := c.fail("node", "list", "--profile", "cincdead")
	if !strings.Contains(r.stderr, "127.0.0.1:1") {
		t.Errorf("cincdead should have used its (dead) cinc_server_url: %s", r)
	}
}

// testProfilePrecedence tells the two orgs apart by a node that exists only
// in Target.Org: --profile beats $CINC_PROFILE beats $CHEF_PROFILE beats
// "default".
func testProfilePrecedence(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	sees := func(opts runOpts, args ...string) bool {
		t.Helper()
		out := c.runWith(opts, append(append([]string{"node", "list"}, args...), "--format", "json")...)
		var names []string
		if err := json.Unmarshal([]byte(out), &names); err != nil {
			t.Fatalf("node list --format json: %v\n%s", err, out)
		}
		return slices.Contains(names, name)
	}
	for _, tc := range []struct {
		what string
		env  []string
		args []string
		want bool
	}{
		{"no selection uses default", nil, nil, true},
		{"CHEF_PROFILE selects", []string{"CHEF_PROFILE=other"}, nil, false},
		{"CINC_PROFILE selects", []string{"CINC_PROFILE=other"}, nil, false},
		{"CINC_PROFILE beats CHEF_PROFILE", []string{"CINC_PROFILE=default", "CHEF_PROFILE=other"}, nil, true},
		{"CINC_PROFILE beats CHEF_PROFILE, reversed", []string{"CINC_PROFILE=other", "CHEF_PROFILE=default"}, nil, false},
		{"--profile beats both", []string{"CINC_PROFILE=other", "CHEF_PROFILE=other"}, []string{"--profile", "default"}, true},
	} {
		if got := sees(runOpts{env: tc.env}, tc.args...); got != tc.want {
			t.Errorf("%s: sees the default-org node = %v, want %v", tc.what, got, tc.want)
		}
	}

	for _, opts := range []runOpts{{env: []string{"CINC_PROFILE=nosuch"}}, {env: []string{"CHEF_PROFILE=nosuch"}}} {
		r := c.exec(opts, "node", "list")
		if r.exitCode == 0 {
			t.Fatalf("an unknown profile from %v should fail: %s", opts.env, r)
		}
		if line := behStderrLine(t, r); !strings.Contains(line, `"nosuch"`) {
			t.Errorf("the error should name the unknown profile: %s", r)
		}
	}
}

// testMultiOrgIsolation creates a node in each org, one through --profile
// other, and checks neither leaks into the other org.
func testMultiOrgIsolation(t *testing.T, _ Target, c *cli) {
	here := uniqueName(t, "node")
	createNode(c, here)
	there := uniqueName(t, "node")
	c.run("node", "create", there, "--profile", "other")
	c.cleanup("node", "delete", there, "--profile", "other")

	list := func(args ...string) []string {
		t.Helper()
		var names []string
		c.json(&names, append([]string{"node", "list"}, args...)...)
		return names
	}
	inDefault, inOther := list(), list("--profile", "other")
	if !slices.Contains(inDefault, here) || slices.Contains(inDefault, there) {
		t.Errorf("default org nodes = %v, want %s and not %s", inDefault, here, there)
	}
	if !slices.Contains(inOther, there) || slices.Contains(inOther, here) {
		t.Errorf("other org nodes = %v, want %s and not %s", inOther, there, here)
	}
	wantNotFound(t, c.fail("node", "show", there))
	wantNotFound(t, c.fail("node", "delete", here, "--profile", "other"))
	wantEqual(t, "node in the other org", showNodeIn(c, there, "other").Name, there)
}

func showNodeIn(c *cli, name, profile string) cinc.Node {
	c.t.Helper()
	var n cinc.Node
	c.json(&n, "node", "show", name, "--profile", profile)
	return n
}

// behWantUnauthorized fails the case unless r is a one-line 401 that points the
// user at their key and clock, the two things they can fix.
func behWantUnauthorized(t *testing.T, r result) string {
	t.Helper()
	line := behStderrLine(t, r)
	if r.exitCode == 0 || !hasStatus(line, 401) {
		t.Errorf("want a 401: %s", r)
	}
	if !strings.Contains(line, "client key") || !strings.Contains(line, "clock") {
		t.Errorf("a 401 should tell the user to check their client key and clock: %s", r)
	}
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("a refused request should print nothing on stdout: %s", r)
	}
	return line
}

// testAuthWrongKey signs as the admin with a key the server has never seen:
// signatures are verified on reads and writes alike, and the refused write
// changes nothing.
func testAuthWrongKey(t *testing.T, tgt Target, c *cli) {
	c.addProfile("wrongkey", tgt.Org, tgt.Admin, behFreshKey(t))
	behWantUnauthorized(t, c.fail("node", "list", "--profile", "wrongkey"))

	name := uniqueName(t, "node")
	c.cleanup("node", "delete", name)
	behWantUnauthorized(t, c.fail("node", "create", name, "--profile", "wrongkey"))
	wantNotFound(t, c.fail("node", "show", name))
}

// testAuthUnknownClient signs as a name the server has never heard of.
func testAuthUnknownClient(t *testing.T, tgt Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.addProfile("ghost", tgt.Org, ghost, behFreshKey(t))
	if line := behWantUnauthorized(t, c.fail("node", "list", "--profile", "ghost")); !strings.Contains(line, ghost) {
		t.Errorf("the 401 should name the unknown actor %s: %s", ghost, line)
	}
}

// testForbiddenShape has a plain client try writes its ACLs do not allow.
// Each is a one-line 403, distinct from a 401 or a 404, and leaves the
// object as it was. The ACL family covers grants; this covers the message.
func testForbiddenShape(t *testing.T, _ Target, c *cli) {
	behCreateRobot(c, "robot")
	node := uniqueName(t, "node")
	createNode(c, node, "--run-list", "recipe[original]")
	env := uniqueName(t, "env")
	createEnvironment(c, env)
	role := uniqueName(t, "role")
	c.run("role", "create", role)
	c.cleanup("role", "delete", role)
	bag := uniqueName(t, "bag")
	behCreateDataBag(c, bag)
	newEnv := uniqueName(t, "env")
	c.cleanup("environment", "delete", newEnv)
	envFile := writeJSON(t, map[string]any{"name": env, "description": "changed by robot"})
	nodeFile := writeJSON(t, cinc.Node{Name: node, Environment: "_default", RunList: []string{"recipe[robot]"}})

	// Reads are allowed, so the robot is a working, recognised actor.
	c.run("node", "list", "--profile", "robot")
	for _, args := range [][]string{
		{"node", "delete", node},
		{"node", "edit", node, "--file", nodeFile},
		{"environment", "create", newEnv},
		{"environment", "delete", env},
		{"environment", "edit", env, "--file", envFile},
		{"role", "delete", role},
		{"databag", "delete", bag},
	} {
		r := c.fail(append(args, "--profile", "robot")...)
		line := behStderrLine(t, r)
		if !hasStatus(line, 403) || hasStatus(line, 401) || isNotFound(r) {
			t.Errorf("%s as a plain client should be a 403: %s", strings.Join(args, " "), r)
		}
	}
	wantSlice(t, "node run list after the refused edit", showNode(c, node).RunList, []string{"recipe[original]"})
	wantNotFound(t, c.fail("environment", "show", newEnv))
	var e map[string]any
	c.json(&e, "environment", "show", env)
	if e["description"] == "changed by robot" {
		t.Errorf("a refused edit changed the environment: %v", e)
	}
	c.run("role", "show", role)
	c.run("databag", "show", bag)
}

// testFormatRejectedBeforeActing passes an output format no command knows.
// The flag has to be rejected before the command does anything: a create
// that runs and then complains about --format has already changed the
// server (or the credentials file).
func testFormatRejectedBeforeActing(t *testing.T, tgt Target, c *cli) {
	node := uniqueName(t, "node")
	env := uniqueName(t, "env")
	c.cleanup("node", "delete", node)
	c.cleanup("environment", "delete", env)
	path := filepath.Join(t.TempDir(), "credentials")

	for _, args := range [][]string{
		{"node", "list"},
		{"node", "create", node},
		{"environment", "create", env},
		{"config", "create", "--config", path, "--server-url", c.orgURL(tgt.Org), "--client-name", tgt.Admin, "--client-key", tgt.KeyPath},
		{"version"},
	} {
		r := c.exec(runOpts{}, append(args, "--format", "yaml")...)
		if r.exitCode == 0 {
			t.Errorf("%s --format yaml should be rejected: %s", strings.Join(args, " "), r)
			continue
		}
		if line := behStderrLine(t, r); !strings.Contains(line, `"yaml"`) || !strings.Contains(line, "json") {
			t.Errorf("the error should name the bad format and the ones we accept: %s", r)
		}
	}
	wantNotFound(t, c.fail("node", "show", node))
	wantNotFound(t, c.fail("environment", "show", env))
	if _, err := os.Stat(path); err == nil {
		t.Errorf("config create --format yaml wrote %s", path)
	}
}

// testListFormatMatrix runs every noun's list in both formats: the human
// form succeeds, and the JSON form is a JSON document that holds what the
// org always has. org list is left to the orgs family: erchef reserves
// GET /organizations to pivotal, and the suite's admin is not pivotal.
func testListFormatMatrix(t *testing.T, tgt Target, c *cli) {
	for _, tc := range []struct {
		args []string
		want string // a substring every org's JSON holds, or ""
	}{
		{[]string{"node", "list"}, ""},
		{[]string{"role", "list"}, ""},
		{[]string{"environment", "list"}, `"_default"`},
		{[]string{"client", "list"}, ""},
		{[]string{"databag", "list"}, ""},
		{[]string{"group", "list"}, `"admins"`},
		{[]string{"policy", "list"}, ""},
		{[]string{"policy-group", "list"}, ""},
		{[]string{"user", "list"}, `"` + tgt.Admin + `"`},
		{[]string{"cookbook", "list"}, ""},
	} {
		what := strings.Join(tc.args, " ")
		c.run(tc.args...)
		out := c.run(append(tc.args, "--format", "json")...)
		if !json.Valid([]byte(out)) {
			t.Errorf("%s --format json is not JSON:\n%s", what, out)
			continue
		}
		if tc.want != "" && !strings.Contains(out, tc.want) {
			t.Errorf("%s --format json missing %s:\n%s", what, tc.want, out)
		}
	}
}

// testNotFoundShape runs show and delete of a missing object on every noun.
// Each fails with one line on stderr that says it was not found and names
// the object, and prints nothing on stdout: never an empty success.
func testNotFoundShape(t *testing.T, _ Target, c *cli) {
	g := uniqueName(t, "ghost")
	bag := uniqueName(t, "bag")
	behCreateDataBag(c, bag)
	for _, args := range [][]string{
		{"node", "show", g},
		{"role", "show", g},
		{"environment", "show", g},
		{"client", "show", g},
		{"group", "show", g},
		{"databag", "show", g},
		{"databag", "item", "show", bag, g},
		{"user", "show", g},
		{"org", "show", g},
		{"policy", "show", g},
		{"policy-group", "show", g},
		{"cookbook", "show", g},
		{"node", "delete", g},
		{"role", "delete", g},
		{"environment", "delete", g},
		{"client", "delete", g},
		{"group", "delete", g},
		{"databag", "delete", g},
		{"user", "delete", g},
		{"policy", "delete", g},
		{"policy-group", "delete", g},
		{"client", "key", "list", g},
		{"user", "key", "list", g},
	} {
		what := strings.Join(args, " ")
		r := c.exec(runOpts{}, args...)
		if r.exitCode == 0 {
			t.Errorf("%s succeeded on a missing object: %s", what, r)
			continue
		}
		line := behStderrLine(t, r)
		if !isNotFound(r) || !strings.Contains(line, g) {
			t.Errorf("%s should say %s was not found: %s", what, g, r)
		}
		if strings.TrimSpace(r.stdout) != "" {
			t.Errorf("%s printed on stdout: %s", what, r)
		}
	}
}

func behCreateDataBag(c *cli, name string) {
	c.t.Helper()
	c.run("databag", "create", name)
	c.cleanup("databag", "delete", name)
}

// behMentionsConflict reports whether a line reads as a 409 however the server
// phrases it.
func behMentionsConflict(line string) bool {
	low := strings.ToLower(line)
	return hasStatus(line, 409) || strings.Contains(low, "already exist") || strings.Contains(low, "conflict")
}

// testConflictShape creates, then creates again, one object of every noun
// whose create can collide. The repeat is a one-line conflict and leaves
// the original alone.
func testConflictShape(t *testing.T, _ Target, c *cli) {
	node, role, env, client, group, bag := uniqueName(t, "node"), uniqueName(t, "role"),
		uniqueName(t, "env"), uniqueName(t, "client"), uniqueName(t, "group"), uniqueName(t, "bag")
	keyFile := filepath.Join(t.TempDir(), "client.pem")
	createNode(c, node, "--run-list", "recipe[original]")
	c.run("role", "create", role)
	c.cleanup("role", "delete", role)
	createEnvironment(c, env)
	c.run("client", "create", client, "--key-file", keyFile)
	c.cleanup("client", "delete", client)
	c.run("group", "create", group)
	c.cleanup("group", "delete", group)
	behCreateDataBag(c, bag)
	c.run("client", "key", "create", client, "rotation")

	for _, args := range [][]string{
		{"node", "create", node},
		{"role", "create", role},
		{"environment", "create", env},
		{"client", "create", client, "--key-file", filepath.Join(t.TempDir(), "again.pem")},
		{"group", "create", group},
		{"databag", "create", bag},
		{"client", "key", "create", client, "rotation"},
	} {
		what := strings.Join(args, " ")
		r := c.exec(runOpts{}, args...)
		if r.exitCode == 0 {
			t.Errorf("repeating %s succeeded: %s", what, r)
			continue
		}
		if line := behStderrLine(t, r); !behMentionsConflict(line) {
			t.Errorf("repeating %s should report a conflict: %s", what, r)
		}
	}
	wantSlice(t, "node run list after the refused create", showNode(c, node).RunList, []string{"recipe[original]"})
}

func testVersion(t *testing.T, _ Target, c *cli) {
	out := c.run("version")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[0], behProgName(c)+" ") {
		t.Fatalf("version output = %q, want a \"%s <version>\" line and four details", out, behProgName(c))
	}
	for i, key := range []string{"commit:", "built:", "go:", "platform:"} {
		if !strings.HasPrefix(strings.TrimSpace(lines[i+1]), key) {
			t.Errorf("version line %d = %q, want it to start with %q", i+2, lines[i+1], key)
		}
	}
	// version needs no credentials: it works in an empty HOME, without
	// offering first-run setup.
	b := behBareCLI(c)
	wantEqual(t, "version in an empty HOME", b.run("version"), out)
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("version wrote %s", b.credentialsPath())
	}
	r := c.fail("version", "extra")
	behStderrLine(t, r)
}

// behRenamedCLI returns a cli whose binary is a copy of c's installed under
// name, as a distribution that has already spent the name cinc would.
func behRenamedCLI(c *cli, name string) *cli {
	c.t.Helper()
	b := behBareCLI(c)
	b.bin = filepath.Join(c.t.TempDir(), name)
	data, err := os.ReadFile(c.bin)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(b.bin, data, 0o755); err != nil {
		c.t.Fatal(err)
	}
	waitUntilExecutable(c.t, b.bin)
	return b
}

// waitUntilExecutable returns once a freshly written binary can be run.
// Cases run in parallel, so another case can fork while the copy above
// still has the file open for writing. The forked child holds that
// descriptor until it execs, and on Linux running the file meanwhile
// fails with "text file busy". The window closes on its own, and no new
// writer can open, so retrying until the error stops is enough.
func waitUntilExecutable(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := exec.Command(path, "version").Run()
		if !errors.Is(err, syscall.ETXTBSY) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s stayed busy: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// testRenamedBinary checks that a renamed install names itself everywhere
// it tells the user what to run: usage, examples, the version banner, the
// missing-credentials guidance, the first-run prompts and the completion
// script. It then configures and uses a profile under the new name.
func testRenamedBinary(t *testing.T, tgt Target, c *cli) {
	b := behRenamedCLI(c, "cinc-ng")

	if help := b.run("--help"); !strings.Contains(help, "cinc-ng [command]") {
		t.Errorf("--help usage does not follow the invoked name:\n%s", help)
	}
	if v := b.run("version"); !strings.HasPrefix(v, "cinc-ng ") {
		t.Errorf("version does not lead with the invoked name:\n%s", v)
	}
	// Cobra prints Example verbatim, which is what a rename that stops at
	// the root command gets wrong.
	createHelp := b.run("config", "create", "--help")
	if !strings.Contains(createHelp, "cinc-ng config create") {
		t.Errorf("examples do not follow the invoked name:\n%s", createHelp)
	}
	if regexp.MustCompile(`(?m)(?:^|[^-\w])cinc (?:config|node|policy) `).MatchString(createHelp) {
		t.Errorf("help still advertises the canonical binary:\n%s", createHelp)
	}
	if r := b.fail("node", "list"); !strings.Contains(r.stderr, "cinc-ng config create") {
		t.Errorf("missing-credentials guidance should name the invoked binary: %s", r)
	}
	if r := b.execTTY("n\n", "node", "list"); !strings.Contains(r.stderr, "cinc-ng config create") {
		t.Errorf("the declined first-run prompt should name the invoked binary: %s", r)
	}
	if comp := b.run("completion", "bash"); !strings.Contains(comp, "__start_cinc-ng()") {
		t.Errorf("bash completion should register under the invoked name:\n%.300s", comp)
	}

	if tgt.CACertPath != "" {
		behCopyFile(t, tgt.CACertPath, filepath.Join(b.home, ".cinc", "trusted_certs", "ca.pem"))
	}
	b.run("config", "create", "--server-url", b.orgURL(tgt.Org), "--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	b.run("node", "list")
}

// testRenamedBinaryUnsafeName covers the security half: argv[0] is
// caller-controlled and cobra interpolates the root name into completion
// scripts unquoted, so a name that is not a plain command name must never
// reach the output.
func testRenamedBinaryUnsafeName(t *testing.T, _ Target, c *cli) {
	b := behRenamedCLI(c, "cinc`id`")
	comp := b.run("completion", "bash")
	if strings.Contains(comp, "`id`") {
		t.Errorf("an unsafe argv[0] reached the completion script:\n%.400s", comp)
	}
	if !strings.Contains(comp, "__start_cinc()") {
		t.Errorf("expected a fallback to the canonical name:\n%.300s", comp)
	}
	if v := b.run("version"); !strings.HasPrefix(v, "cinc ") {
		t.Errorf("version under an unsafe name should fall back to cinc:\n%s", v)
	}
}

func behReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// behProgName is the name the binary under test was installed as, which is
// the name it tells users to run: "cinc" for a normal build, but CINC_BIN
// may point at a copy with any name.
func behProgName(c *cli) string {
	return strings.TrimSuffix(filepath.Base(c.bin), ".exe")
}
