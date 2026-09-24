package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// First-run cases for a brand-new user: no ~/.cinc/credentials and no Chef
// history to migrate. They sit in cliFamily beside the migration cases in
// behaviour.go.

// firstRunGate is what the setup gate prints, and so what a case looks for
// to tell whether first-run setup was offered at all.
const firstRunGate = "first time using Cinc"

// eot is the terminal's end-of-file character (Ctrl-D). In canonical mode it
// makes the pending read return nothing, which the CLI sees as EOF.
const eot = "\x04"

// configureAnswers are the lines a new user types at the configure prompts
// to set up a profile signing as the target's admin. location and key are
// typed as given, so a case can answer with a ~ path.
func configureAnswers(tgt Target, location, key string) []string {
	return []string{
		location,      // Credentials file location
		"",            // Profile name: default
		"",            // Supermarket site
		tgt.Admin,     // Client name
		key,           // Client key path
		tgt.ServerURL, // Server host
		tgt.Org,       // Organization
		"",            // SSL verify mode
	}
}

// lines joins prompt answers, one per line, as a user types them.
func lines(answers ...string) string {
	return strings.Join(answers, "\n") + "\n"
}

// trustTargetCA puts the target's CA in the default trusted_certs
// directory, as `knife ssl fetch` would for a TLS server, so a profile the
// case configures can reach the target.
func trustTargetCA(t *testing.T, b *cli) {
	t.Helper()
	if b.tgt.CACertPath != "" {
		behCopyFile(t, b.tgt.CACertPath, filepath.Join(b.home, ".cinc", "trusted_certs", "ca.pem"))
	}
}

// testFirstRunBareCinc runs cinc with no command, the first thing many new
// users type. It offers setup; declining exits cleanly without dumping help
// on someone who said no, and finishing setup shows the help so they see
// what to run next. Once credentials exist, or when --config names a file,
// it is just help.
func testFirstRunBareCinc(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	r := b.execTTY("n\n")
	if r.exitCode != 0 || !strings.Contains(r.stderr, firstRunGate) || !strings.Contains(r.stderr, "No problem") {
		t.Errorf("bare cinc in an empty HOME should offer setup and accept a no: %s", r)
	}
	if strings.Contains(r.stdout, "Usage:") {
		t.Errorf("declining setup should not print help: %s", r)
	}
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("declining setup wrote %s", b.credentialsPath())
	}

	b = behBareCLI(c)
	trustTargetCA(t, b)
	r = b.execTTY(lines(append([]string{"y"}, configureAnswers(tgt, "", tgt.KeyPath)...)...))
	if r.exitCode != 0 {
		t.Fatalf("bare cinc through first-run setup failed: %s", r)
	}
	wrote, usage := strings.Index(r.stdout, "Wrote credentials profile"), strings.Index(r.stdout, "Usage:")
	if wrote < 0 || usage < wrote {
		t.Errorf("bare cinc should print help after finishing setup: %s", r)
	}
	b.run("node", "list")

	// A trailing "n" is queued so a regression that prompts fails on the
	// assertion below instead of hanging on the terminal.
	if r := c.execTTY("n\n"); strings.Contains(r.stderr, firstRunGate) || !strings.Contains(r.stdout, "Usage:") {
		t.Errorf("bare cinc with credentials in place should print help and nothing else: %s", r)
	}
	b = behBareCLI(c)
	cfg := filepath.Join(b.home, "elsewhere", "credentials")
	behCopyFile(t, c.credentialsPath(), cfg)
	if r := b.execTTY("n\n", "--config", cfg); strings.Contains(r.stderr, firstRunGate) || !strings.Contains(r.stdout, "Usage:") {
		t.Errorf("bare cinc --config should use that file, not offer setup: %s", r)
	}
}

// testFirstRunOtherEntryPoints checks that explore and supermarket share,
// which load credentials without going through a server command's client,
// offer the same first-run setup. Declining is enough to see the gate, and
// stops before anything reaches a Supermarket.
func testFirstRunOtherEntryPoints(t *testing.T, _ Target, c *cli) {
	for _, args := range [][]string{
		{"explore"},
		{"supermarket", "share", "mycookbook"},
	} {
		b := behBareCLI(c)
		r := b.execTTY("n\n", args...)
		what := strings.Join(args, " ")
		if r.exitCode == 0 || !strings.Contains(r.stderr, firstRunGate) || !strings.Contains(r.stderr, "No problem") {
			t.Errorf("%s in an empty HOME should offer first-run setup: %s", what, r)
		}
		if _, err := os.Stat(b.credentialsPath()); err == nil {
			t.Errorf("declining setup from %s wrote %s", what, b.credentialsPath())
		}
	}
}

// testFirstRunNeverOffered runs, on a terminal in an empty HOME, the
// commands that need no credentials or that name their own file. None of
// them may stop a new user at the setup gate: help, completion, validate
// reporting the missing file, an explicit --config, a dry-run share, and
// config create, which is the setup.
func testFirstRunNeverOffered(t *testing.T, tgt Target, c *cli) {
	for _, args := range [][]string{
		{"--help"},
		{"help", "node"},
		{"node", "list", "--help"},
		{"completion", "bash"},
		{"config", "validate"},
		{"node", "list", "--config", "~/nothing-here"},
		{"supermarket", "share", "mycookbook", "--dry-run"},
	} {
		b := behBareCLI(c)
		// A queued "n" makes a regression fail on the assertion rather
		// than hang waiting for an answer.
		r := b.execTTY("n\n", args...)
		what := strings.Join(args, " ")
		if strings.Contains(r.stderr, firstRunGate) {
			t.Errorf("%s should not offer first-run setup: %s", what, r)
		}
		if _, err := os.Stat(b.credentialsPath()); err == nil {
			t.Errorf("%s wrote %s", what, b.credentialsPath())
		}
	}

	b := behBareCLI(c)
	trustTargetCA(t, b)
	r := b.execTTY(lines(configureAnswers(tgt, "", tgt.KeyPath)...), "config", "create")
	if r.exitCode != 0 || strings.Contains(r.stderr, firstRunGate) {
		t.Errorf("config create in an empty HOME should go straight to its prompts: %s", r)
	}
	b.run("node", "list")
}

// testFirstRunGateEOF presses Ctrl-D at a first-run prompt. End of input is
// not a yes: the user gets the decline message, nothing is written, and the
// command never goes on to read prompts nobody is answering.
func testFirstRunGateEOF(t *testing.T, _ Target, c *cli) {
	for _, tc := range []struct {
		what, answers string
		chef          bool
	}{
		{"Ctrl-D at the setup gate", eot, false},
		{"Ctrl-D at the migration prompt", "y\n" + eot, true},
	} {
		b := behBareCLI(c)
		if tc.chef {
			behChefCredentials(t, b, "")
		}
		r := b.execTTY(tc.answers, "node", "list")
		if r.exitCode == 0 {
			t.Errorf("%s should fail the command: %s", tc.what, r)
		}
		if !strings.Contains(r.stderr, "No problem") || !strings.Contains(r.stderr, behProgName(b)+" config create") {
			t.Errorf("%s should decline and point at config create: %s", tc.what, r)
		}
		if strings.Contains(r.stdout, "Credentials file location") {
			t.Errorf("%s should not go on to the configure prompts: %s", tc.what, r)
		}
		if _, err := os.Stat(b.credentialsPath()); err == nil {
			t.Errorf("%s wrote %s", tc.what, b.credentialsPath())
		}
	}
}

// testFirstRunLocationTilde answers the location and key prompts with ~
// paths, as a user naturally types them. Both must mean the home directory,
// as they do for `config create`, never a directory literally named ~ under
// wherever the command ran.
func testFirstRunLocationTilde(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	trustTargetCA(t, b)
	behCopyFile(t, tgt.KeyPath, filepath.Join(b.home, "keys", "admin.pem"))

	r := b.execTTY(lines(append([]string{"y"}, configureAnswers(tgt, "~/work/credentials", "~/keys/admin.pem")...)...), "node", "list")
	if r.exitCode != 0 {
		t.Fatalf("first-run setup with ~ paths failed: %s", r)
	}
	if _, err := os.Stat(filepath.Join(b.home, "~")); err == nil {
		t.Errorf("first-run setup created a directory literally named ~: %s", r)
	}
	path := filepath.Join(b.home, "work", "credentials")
	if !strings.Contains(r.stdout, path) {
		t.Errorf("first-run setup should report the expanded path %s: %s", path, r)
	}
	written := behReadFile(t, path)
	if want := fmt.Sprintf("client_key = %q", filepath.Join(b.home, "keys", "admin.pem")); !strings.Contains(written, want) {
		t.Errorf("the key path should be written expanded, as config create writes it (%s):\n%s", want, written)
	}
	b.run("--config", "~/work/credentials", "node", "list")
}

// testFirstRunAcceptDefaults presses Enter at every first-run prompt, the
// fastest way through setup. Enter at the gate means yes, and the defaults
// write a [default] profile for $USER with no server. The next server
// command has to say which setting is missing and how to add it, not fail
// on a URL the user never typed.
func testFirstRunAcceptDefaults(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	b.env = []string{"USER=newuser"}

	r := b.execTTY(lines("", "", "", "", "", "", "", ""), "node", "list")
	if r.exitCode != 0 {
		t.Fatalf("pressing Enter through first-run setup failed: %s", r)
	}
	if !strings.Contains(r.stdout, `Wrote credentials profile "default"`) {
		t.Errorf("first-run setup should report the profile it wrote: %s", r)
	}
	written := behReadFile(t, b.credentialsPath())
	for _, want := range []string{
		"[default]",
		`client_name = "newuser"`,
		fmt.Sprintf("client_key = %q", filepath.Join(b.home, ".cinc", "newuser.pem")),
		`supermarket_site = "https://supermarket.chef.io"`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("accepting every default should write %s:\n%s", want, written)
		}
	}
	if strings.Contains(written, "server_url") {
		t.Errorf("no server was given, so none should be written:\n%s", written)
	}

	r = b.fail("node", "list")
	line := behStderrLine(t, r)
	for _, want := range []string{"cinc_server_url", `"default"`, behProgName(b) + " config create"} {
		if !strings.Contains(line, want) {
			t.Errorf("a profile with no server should name %s in its error: %s", want, r)
		}
	}
}
