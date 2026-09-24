package suite

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// First-run cases for a Chef user moving to cinc: ~/.chef/credentials exists
// and first-run setup offers to migrate it. They sit in cliFamily beside the
// migration cases in behaviour.go.

// migrateOnFirstRun accepts setup and the migration on a terminal, the way a
// knife user answers the first time they run cinc, and fails the case if the
// migration does not succeed.
func migrateOnFirstRun(t *testing.T, b *cli) result {
	t.Helper()
	r := b.execTTY("y\ny\n", "node", "list")
	if r.exitCode != 0 || !strings.Contains(r.stderr, "migrate it") {
		t.Fatalf("first run with migration failed: %s", r)
	}
	return r
}

// chefProfile renders one knife credentials profile signing as the
// target's admin with the key behChefCredentials keeps in ~/.chef.
func chefProfile(b *cli, name, org, extra string) string {
	return fmt.Sprintf("\n[%s]\nchef_server_url = %q\nclient_name = %q\nclient_key = %q\n%s",
		name, b.orgURL(org), b.tgt.Admin, filepath.Join(b.home, ".chef", "admin.pem"), extra)
}

// testMigrationThenValidate migrates a two-profile knife file and runs
// config validate straight away. A migrated file has to pass every check
// with no hand edits: that is the promise of migrating rather than making
// the user start over.
func testMigrationThenValidate(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, chefProfile(b, "other", tgt.OtherOrg, ""))
	migrateOnFirstRun(t, b)

	rep, r := b.validate()
	if !rep.Valid {
		t.Fatalf("a freshly migrated file should validate: %s", r)
	}
	for _, name := range []string{"default", "other"} {
		rep.profile(t, name).wantCheck(t, checkReachable, true)
	}
}

// testMigrationLeavesChefFileAlone checks the migration only reads the
// knife file, which knife still uses, and writes the cinc file readable by
// its owner alone, since it names private keys.
func testMigrationLeavesChefFileAlone(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, "node_name = \"knife-node\"\n")
	chefPath := filepath.Join(b.home, ".chef", "credentials")
	before, err := os.ReadFile(chefPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(chefPath)
	if err != nil {
		t.Fatal(err)
	}

	migrateOnFirstRun(t, b)

	after, err := os.ReadFile(chefPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("migration changed %s:\nbefore:\n%s\nafter:\n%s", chefPath, before, after)
	}
	afterInfo, err := os.Stat(chefPath)
	if err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "mode of the knife file after migration", afterInfo.Mode(), beforeInfo.Mode())

	if runtime.GOOS == "windows" {
		return
	}
	for path, want := range map[string]os.FileMode{
		filepath.Dir(b.credentialsPath()): 0o700,
		b.credentialsPath():               0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		wantEqual(t, "permissions of "+path, info.Mode().Perm(), want)
	}
}

// testMigrationCarriedSettingsWork migrates a profile that names its own
// data bag secret and trusted_certs_dir, and uses both afterwards. Copying
// the keys across is not enough if the migrated profile cannot use them.
func testMigrationCarriedSettingsWork(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	const secret = "migrated-secret"
	writeFile(t, filepath.Join(b.home, ".chef", "encrypted_data_bag_secret"), secret)
	certs := filepath.Join(b.home, ".chef", "custom_certs")
	writeFile(t, filepath.Join(certs, "ca.pem"), behCACertPEM(t, tgt))
	behCopyFile(t, tgt.KeyPath, filepath.Join(b.home, ".chef", "admin.pem"))
	// Only the named directory trusts the target, so a TLS target proves
	// the profile really reads trusted_certs_dir rather than knife's default.
	writeFile(t, filepath.Join(b.home, ".chef", "credentials"), chefProfile(b, "default", tgt.Org,
		"secret_file = \"~/.chef/encrypted_data_bag_secret\"\ntrusted_certs_dir = \"~/.chef/custom_certs\"\n"))

	migrateOnFirstRun(t, b)

	rep, r := b.validate()
	if !rep.Valid {
		t.Fatalf("the migrated profile should validate: %s", r)
	}
	if detail := rep.profile(t, "default").wantCheck(t, checkTrustedCerts, true).Detail; !strings.Contains(detail, "custom_certs") {
		t.Errorf("trusted certificates should come from the migrated trusted_certs_dir, got %q", detail)
	}

	bag := databagNew(c)
	b.run("databag", "secret", "create", bag, "item", "--file", writeJSON(t, map[string]any{"id": "item", "v": "ok"}))
	wantEqual(t, "item encrypted with the migrated secret_file", databagSecretShow(c, nil, bag, "item", "--secret", secret)["v"], any("ok"))
}

// testMigrationPrefersCincServerURL migrates a profile carrying both
// server URL keys, as one shared by knife and an earlier cinc does. The
// cinc key wins, as it does everywhere else, and the chef URL is not
// carried across to confuse a later edit.
func testMigrationPrefersCincServerURL(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	behCopyFile(t, tgt.KeyPath, filepath.Join(b.home, ".chef", "admin.pem"))
	if tgt.CACertPath != "" {
		behCopyFile(t, tgt.CACertPath, filepath.Join(b.home, ".chef", "trusted_certs", "ca.pem"))
	}
	writeFile(t, filepath.Join(b.home, ".chef", "credentials"), fmt.Sprintf(
		"[default]\nchef_server_url = %q\ncinc_server_url = %q\nclient_name = %q\nclient_key = %q\n",
		deadURL, b.orgURL(tgt.Org), tgt.Admin, filepath.Join(b.home, ".chef", "admin.pem")))

	migrateOnFirstRun(t, b)

	written := behReadFile(t, b.credentialsPath())
	if !strings.Contains(written, fmt.Sprintf("cinc_server_url = %q", b.orgURL(tgt.Org))) || strings.Contains(written, deadURL) {
		t.Errorf("migration should keep cinc_server_url and drop chef_server_url:\n%s", written)
	}
	b.run("node", "list")
}

// testMigrationAllOrNothing migrates a knife file with one profile cinc
// cannot use. Nothing is written, since a partial file would stop first-run
// setup from ever offering to finish; the error names the profile and what
// it lacks; and the next run offers the migration again.
func testMigrationAllOrNothing(t *testing.T, tgt Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, fmt.Sprintf("\n[broken]\nchef_server_url = %q\nclient_name = %q\n", b.orgURL(tgt.Org), tgt.Admin))

	r := b.execTTY("y\ny\n", "node", "list")
	if r.exitCode == 0 {
		t.Fatalf("migrating a profile with no client_key should fail: %s", r)
	}
	if !strings.Contains(r.stderr, `"broken"`) || !strings.Contains(r.stderr, "client_key") {
		t.Errorf("the error should name the profile and the missing client_key: %s", r)
	}
	if _, err := os.Stat(b.credentialsPath()); err == nil {
		t.Errorf("a failed migration wrote %s:\n%s", b.credentialsPath(), behReadFile(t, b.credentialsPath()))
	}
	if r := b.execTTY("n\n", "node", "list"); !strings.Contains(r.stderr, firstRunGate) {
		t.Errorf("after a failed migration the next run should offer setup again: %s", r)
	}
}

// testMigrationKeepsInvalidSSLMode migrates a profile whose ssl_verify_mode
// has a typo knife itself ignores. Migration carries it across unchanged
// rather than guessing, and config validate is where the user finds out.
func testMigrationKeepsInvalidSSLMode(t *testing.T, _ Target, c *cli) {
	b := behBareCLI(c)
	behChefCredentials(t, b, "ssl_verify_mode = \"verify_none\"\n")
	migrateOnFirstRun(t, b)

	if written := behReadFile(t, b.credentialsPath()); !strings.Contains(written, `ssl_verify_mode = "verify_none"`) {
		t.Errorf("migration should carry ssl_verify_mode across as written:\n%s", written)
	}
	rep, _ := b.validate()
	if rep.Valid {
		t.Errorf("config validate should reject ssl_verify_mode = \"verify_none\": %+v", rep)
	}
	rep.profile(t, "default").wantCheck(t, checkSSLMode, false)
}
