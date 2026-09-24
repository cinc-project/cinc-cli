package suite

import (
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
