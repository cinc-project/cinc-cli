package suite

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// config create cases for a user who already has a credentials file: the
// add, update and replace menu, the profile name the command settles on,
// and a file the command cannot or must not rewrite blindly. They sit in
// cliFamily beside the config create cases in behaviour.go.

// serverAnswers are the lines that follow the profile-name prompt when a
// user sets up a profile signing as the target's admin in org: the default
// Supermarket, client name, key, server, organization and SSL mode.
func serverAnswers(tgt Target, org string) []string {
	return []string{"", tgt.Admin, tgt.KeyPath, tgt.ServerURL, org, ""}
}

// profileNames returns the profile names in the credentials file at path,
// in file order.
func profileNames(t *testing.T, path string) []string {
	t.Helper()
	var names []string
	for line := range strings.SplitSeq(behReadFile(t, path), "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && !strings.Contains(line, ".") {
			names = append(names, strings.Trim(line, "[]"))
		}
	}
	return names
}

// testConfigCreateReplace answers "replace" at the existing-file menu. With
// a yes, the file holds only the new profile; with a no, the menu comes
// back and nothing is deleted.
func testConfigCreateReplace(t *testing.T, tgt Target, c *cli) {
	answers := append([]string{"", "3", "y", "fresh"}, serverAnswers(tgt, tgt.Org)...)
	c.runWith(runOpts{stdin: lines(answers...)}, "config", "create")
	wantSlice(t, "profiles after replacing the file", profileNames(t, c.credentialsPath()), []string{"fresh"})
	c.run("node", "list", "--profile", "fresh")

	d := newCLI(t, tgt)
	answers = append([]string{"", "3", "n", "1", "added"}, serverAnswers(tgt, tgt.Org)...)
	out := d.runWith(runOpts{stdin: lines(answers...)}, "config", "create")
	if strings.Count(out, "Choice [1]") != 2 {
		t.Errorf("declining the replace should ask for a menu choice again:\n%s", out)
	}
	names := profileNames(t, d.credentialsPath())
	for _, want := range []string{"default", "other", "added"} {
		if !slices.Contains(names, want) {
			t.Errorf("declining the replace kept profiles %v, want %s among them", names, want)
		}
	}
}

// testConfigCreateSymlinkedFile keeps ~/.cinc/credentials as a symlink into
// a dotfiles checkout, as many users do. Updating and replacing both write
// through the link: the link survives and the dotfiles copy holds the
// result.
func testConfigCreateSymlinkedFile(t *testing.T, tgt Target, c *cli) {
	real := filepath.Join(c.home, "dotfiles", "cinc-credentials")
	writeFile(t, real, behReadFile(t, c.credentialsPath()))
	if err := os.Remove(c.credentialsPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, c.credentialsPath()); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	wantLink := func(what string) {
		t.Helper()
		info, err := os.Lstat(c.credentialsPath())
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s replaced the symlink at %s with a plain file", what, c.credentialsPath())
		}
	}

	c.run("config", "create", "--profile", "extra", "--server-url", c.orgURL(tgt.Org),
		"--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	wantLink("updating")
	if !strings.Contains(behReadFile(t, real), "[extra]") {
		t.Errorf("the update should land in the symlink's target:\n%s", behReadFile(t, real))
	}

	answers := append([]string{"", "3", "y", "fresh"}, serverAnswers(tgt, tgt.Org)...)
	c.runWith(runOpts{stdin: lines(answers...)}, "config", "create")
	wantLink("replacing")
	wantSlice(t, "profiles in the symlink's target after replacing", profileNames(t, real), []string{"fresh"})
	c.run("node", "list", "--profile", "fresh")
}

// testConfigCreateTightensPermissions updates a profile in a credentials
// file others can read, as knife setups often leave it. The file names
// private keys and secrets, so config create leaves it readable by its
// owner alone.
func testConfigCreateTightensPermissions(t *testing.T, tgt Target, c *cli) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no owner-only file mode to check")
	}
	if err := os.Chmod(c.credentialsPath(), 0o644); err != nil {
		t.Fatal(err)
	}
	c.run("config", "create", "--profile", "extra", "--server-url", c.orgURL(tgt.Org),
		"--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	info, err := os.Stat(c.credentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "credentials file mode after config create", info.Mode().Perm(), os.FileMode(0o600))
	c.run("node", "list", "--profile", "extra")
}

// testConfigCreateNameCollision adds a profile under a name the file
// already has. Yes to "Update it instead?" updates that profile in place,
// offering its current values; no asks for another name.
func testConfigCreateNameCollision(t *testing.T, tgt Target, c *cli) {
	// Enter at every prompt after the switch keeps [other]'s values.
	out := c.runWith(runOpts{stdin: lines("", "1", "other", "y", "", "", "", "", "", "")}, "config", "create")
	if !strings.Contains(out, `A profile named "other" already exists.`) || !strings.Contains(out, `Wrote credentials profile "other"`) {
		t.Errorf("a colliding name should offer, then run, an update of it:\n%s", out)
	}
	wantSlice(t, "profiles after updating through a collision", profileNames(t, c.credentialsPath()), []string{"default", "other"})
	var nodes []string
	c.json(&nodes, "node", "list", "--profile", "other")

	answers := append([]string{"", "1", "other", "n", "renamed"}, serverAnswers(tgt, tgt.OtherOrg)...)
	c.runWith(runOpts{stdin: lines(answers...)}, "config", "create")
	wantSlice(t, "profiles after choosing another name", profileNames(t, c.credentialsPath()), []string{"default", "other", "renamed"})
	c.run("node", "list", "--profile", "renamed")
}

// testConfigCreateMalformedFile runs config create over a credentials file
// that is not TOML, with flags and interactively. It must not overwrite
// the file, which may hold knife settings the user can still fix, and it
// has to say which file is broken.
func testConfigCreateMalformedFile(t *testing.T, tgt Target, c *cli) {
	const broken = "[default\ncinc_server_url = \n"
	for _, tc := range []struct {
		what string
		opts runOpts
		args []string
	}{
		{"with flags", runOpts{}, []string{"--server-url", c.orgURL(tgt.Org), "--client-name", tgt.Admin, "--client-key", tgt.KeyPath}},
		{"interactively", runOpts{stdin: lines(append([]string{"", "default"}, serverAnswers(tgt, tgt.Org)...)...)}, nil},
	} {
		b := behBareCLI(c)
		writeFile(t, b.credentialsPath(), broken)
		r := b.exec(tc.opts, append([]string{"config", "create"}, tc.args...)...)
		if r.exitCode == 0 {
			t.Errorf("config create %s over a malformed file should fail: %s", tc.what, r)
		}
		if line := behStderrLine(t, r); !strings.Contains(line, b.credentialsPath()) {
			t.Errorf("config create %s should name the malformed file: %s", tc.what, r)
		}
		wantEqual(t, "malformed file after config create "+tc.what, behReadFile(t, b.credentialsPath()), broken)
	}
	// Every command reports it the same way.
	if line := behStderrLine(t, c.fail("node", "list", "--config", writeJSON(t, "not toml"))); !strings.Contains(line, "we couldn't read") {
		t.Errorf("a server command over a malformed file should say it couldn't read it: %s", line)
	}
}

// testConfigCreateProfileName checks the name config create writes under.
// A public-Supermarket-only profile left at the default name becomes
// [supermarket], where the supermarket commands look first; a name the user
// types, or sets with CINC_PROFILE or CHEF_PROFILE, is kept.
func testConfigCreateProfileName(t *testing.T, tgt Target, c *cli) {
	for _, tc := range []struct {
		what    string
		env     []string
		profile string // answer at the profile-name prompt
		want    string
	}{
		{"the default name", nil, "", "supermarket"},
		{"a typed name", nil, "lab", "lab"},
		{"CINC_PROFILE", []string{"CINC_PROFILE=cincprof", "CHEF_PROFILE=chefprof"}, "", "cincprof"},
		{"CHEF_PROFILE", []string{"CHEF_PROFILE=chefprof"}, "", "chefprof"},
	} {
		b := behBareCLI(c)
		// Location, profile, Supermarket, client name, key, no server, SSL.
		r := b.exec(runOpts{env: tc.env, stdin: lines("", tc.profile, "", tgt.Admin, tgt.KeyPath, "", "")}, "config", "create")
		if r.exitCode != 0 {
			t.Errorf("config create with %s failed: %s", tc.what, r)
			continue
		}
		wantSlice(t, "profile written with "+tc.what, profileNames(t, b.credentialsPath()), []string{tc.want})
	}

	// The same variables name the profile a non-interactive create writes.
	b := behBareCLI(c)
	b.runWith(runOpts{env: []string{"CINC_PROFILE=staging"}}, "config", "create",
		"--server-url", b.orgURL(tgt.Org), "--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	wantSlice(t, "profile written with flags and CINC_PROFILE", profileNames(t, b.credentialsPath()), []string{"staging"})
}

// testConfigCreateConflictingServerURLs passes two different server URLs
// through the spellings of the server URL flag. They are one setting, so
// the command refuses to guess which one was meant rather than keeping
// whichever came last; the same URL twice is fine.
func testConfigCreateConflictingServerURLs(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	r := b.fail("config", "create", "--server-url", b.orgURL(tgt.Org), "--chef-server-url", b.orgURL(tgt.OtherOrg),
		"--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	if line := behStderrLine(t, r); !strings.Contains(line, "--server-url") || !strings.Contains(line, "--chef-server-url") {
		t.Errorf("conflicting server URLs should name both flags: %s", r)
	}
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("a refused config create wrote %s", b.credentialsPath())
	}

	trustTargetCA(t, b)
	b.run("config", "create", "--server-url", b.orgURL(tgt.Org), "--cinc-server-url", b.orgURL(tgt.Org),
		"--client-name", tgt.Admin, "--client-key", tgt.KeyPath)
	b.run("node", "list")
}
