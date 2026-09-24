package suite

import (
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
