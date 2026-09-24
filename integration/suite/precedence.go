package suite

import (
	"path/filepath"
	"strings"
	"testing"
)

// Cases for how a configured CLI settles which credentials file and profile
// to use, and what config validate checks, once setup is behind the user.
// They sit in cliFamily beside the profile and chef-compat cases in
// behaviour.go.

// testSSLVerifyModeTypo sets ssl_verify_mode = "verify_none", without the
// leading colon knife requires. Only the exact ":verify_none" turns
// certificate checks off, so the typo never weakens TLS: commands verify as
// usual and print no warning about skipped checks.
func testSSLVerifyModeTypo(t *testing.T, tgt Target, c *cli) {
	behAppendProfile(t, c.credentialsPath(), "typo", behWith(c.adminFields(tgt.Org), "ssl_verify_mode", "verify_none"))
	r := c.exec(runOpts{}, "node", "list", "--profile", "typo")
	if strings.Contains(strings.ToLower(r.stderr), "verif") && strings.Contains(strings.ToLower(r.stderr), "warning") {
		t.Errorf("a mistyped ssl_verify_mode must not turn off certificate checks: %s", r)
	}
	if r.exitCode != 0 {
		t.Errorf("with the target's CA trusted, the typo profile should work with verification on: %s", r)
	}

	if tgt.CACertPath == "" {
		return
	}
	// With nothing trusting the server, verification being on means the
	// command fails on the certificate rather than connecting anyway.
	empty := filepath.Join(c.home, "no-certs")
	writeFile(t, filepath.Join(empty, "README.txt"), "no certificates here\n")
	behAppendProfile(t, c.credentialsPath(), "typo-untrusted",
		behWith(behWith(c.adminFields(tgt.Org), "ssl_verify_mode", "verify_none"), "trusted_certs_dir", empty))
	r = c.fail("node", "list", "--profile", "typo-untrusted")
	if !strings.Contains(strings.ToLower(r.stderr), "certificate") {
		t.Errorf("a mistyped ssl_verify_mode should still verify the server's certificate: %s", r)
	}
}

// testConfigRelativePath passes --config, and config validate's path
// argument, as a path relative to the directory the command runs in, the
// way a shell completes a file in the current directory.
func testConfigRelativePath(t *testing.T, _ Target, c *cli) {
	rel := filepath.Join("work", "credentials")
	behCopyFile(t, c.credentialsPath(), filepath.Join(c.home, rel))

	c.run("node", "list", "--config", rel)
	if rep, r := c.validate(rel); !rep.Valid || rep.Path != filepath.Join(c.home, rel) && rep.Path != rel {
		t.Errorf("config validate with a relative path should validate that file: %s", r)
	}
}

// testExploreProfileFromEnv starts explore with CINC_PROFILE set. Like
// every other command, explore uses that profile without asking, so it
// opens straight onto the object types of the other organization; an
// unknown one is refused before the TUI starts.
func testExploreProfileFromEnv(t *testing.T, _ Target, c *cli) {
	node := uniqueName(t, "node")
	c.run("node", "create", node, "--profile", "other")
	c.cleanup("node", "delete", node, "--profile", "other")

	u := c.withEnv("CINC_PROFILE=other").startTUI("explore")
	u.waitFor("Object types")
	u.waitFor(selected("Nodes"))
	u.send("\r")
	u.send("/", node, "\r")
	u.waitFor(selected(node))
	u.quit()

	// On a terminal, so the refusal comes from the profile and not from the
	// check that explore is interactive.
	c.withEnv("CINC_PROFILE=nosuch").startTUI("explore").waitFor(`"nosuch"`)
}

// withEnv returns a copy of c whose runs also set env.
func (c *cli) withEnv(env ...string) *cli {
	d := *c
	d.env = append(append([]string{}, c.env...), env...)
	return &d
}
