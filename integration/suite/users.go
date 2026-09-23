package suite

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// Users are global to the server, not scoped to an org, so every case names
// its users with uniqueName and deletes them itself: a leftover user on a
// long-lived erchef is visible to every later run.
//
// erchef validates a user on create and on update (chef_user.erl): username
// must match ^[a-z0-9_-]+$, display_name and a valid email are required, and
// a local user needs a password of at least 6 characters. The helpers here
// always send all of them, so a case fails only on what it means to test.
var userFamily = family{cases: []testCase{
	{"users/lifecycle", []string{"user create", "user show", "user list", "user edit", "user delete"}, testUserLifecycle},
	{"users/create-key-file", []string{"user create", "user show"}, testUserCreateKeyFile},
	{"users/create-public-key", []string{"user create", "user show"}, testUserCreatePublicKey},
	{"users/already-exists", []string{"user create"}, testUserAlreadyExists},
	{"users/not-found", []string{"user show", "user delete", "user password"}, testUserNotFound},
	{"users/edit-missing", []string{"user edit"}, testUserEditMissing},
	{"users/password", []string{"user password", "user show"}, testUserPassword},
	{"users/password-prompt", []string{"user password"}, testUserPasswordPrompt},
	{"users/password-too-short", []string{"user password"}, testUserPasswordTooShort},
	{"users/create-invalid-name", []string{"user create"}, testUserCreateInvalidName},
	{"users/create-missing-fields", []string{"user create"}, testUserCreateMissingFields},
	{"users/email-lowercased", []string{"user create", "user show"}, testUserEmailLowercased},
	{"users/edit-keeps-omitted-fields", []string{"user edit"}, testUserEditKeepsOmittedFields},
	{"users/self-edit", []string{"user edit", "user show"}, testUserSelfEdit},
	{"users/non-admin-forbidden", []string{"user create", "user edit", "user delete", "user password"}, testUserNonAdminForbidden},
	{"users/delete-pivotal-declined", []string{"user delete", "user list"}, testUserDeletePivotalDeclined},
	{"users/delete-member", []string{"user delete", "org member list"}, testUserDeleteMember},
	{"users/delete-invited", []string{"user delete", "org invite list"}, testUserDeleteInvited},
}}

// testUser is a user a case created, with the private key it signs as.
type testUser struct {
	name     string
	keyPath  string
	password string
}

// createUser creates a user with every field erchef requires, writes its
// server-generated key to a temp file, and registers its cleanup.
func createUser(c *cli) testUser {
	c.t.Helper()
	u := testUser{
		name:     uniqueName(c.t, "user"),
		keyPath:  filepath.Join(c.t.TempDir(), "user.pem"),
		password: "pw-" + randomHex(c.t, 8),
	}
	c.run("user", "create", u.name,
		"--email", u.name+"@example.test",
		"--display-name", "Test User "+u.name,
		"--first-name", "Test",
		"--last-name", "User",
		"--password", u.password,
		"--key-file", u.keyPath)
	c.cleanupMembership("user", "delete", u.name)
	return u
}

// actAs returns the flags that select a profile signing as u against
// tgt.Org, adding the profile on first use.
func actAs(c *cli, u testUser) []string {
	c.t.Helper()
	profile := "as-" + u.name
	existing, err := os.ReadFile(c.credentialsPath())
	if err != nil {
		c.t.Fatal(err)
	}
	if !strings.Contains(string(existing), "["+profile+"]") {
		c.addProfile(profile, c.tgt.Org, u.name, u.keyPath)
	}
	return []string{"--profile", profile}
}

func showUser(c *cli, name string, flags ...string) cinc.User {
	c.t.Helper()
	var u cinc.User
	c.json(&u, append([]string{"user", "show", name}, flags...)...)
	return u
}

// showUserRaw returns `user show --format json` decoded into a map, so a case
// can check for fields cinc.User would drop.
func showUserRaw(c *cli, name string) map[string]any {
	c.t.Helper()
	var u map[string]any
	c.json(&u, "user", "show", name)
	return u
}

func listUsers(c *cli) []string {
	c.t.Helper()
	var names []string
	c.json(&names, "user", "list")
	return names
}

// wantFailure fails the case unless r failed and its stderr mentions one of
// wants (case-insensitive).
func wantFailure(t *testing.T, r result, wants ...string) {
	t.Helper()
	if r.exitCode == 0 {
		t.Fatalf("want a failure, got success: %s", r)
	}
	low := strings.ToLower(r.stderr)
	for _, w := range wants {
		if strings.Contains(low, strings.ToLower(w)) {
			return
		}
	}
	t.Fatalf("want stderr to mention one of %q: %s", wants, r)
}

// wantForbidden fails the case unless r is the server's 403.
func wantForbidden(t *testing.T, r result) {
	t.Helper()
	wantFailure(t, r, "403", "forbidden", "permission", "not allowed", "not authorized")
}

func testUserLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "user")
	out := c.run("user", "create", name,
		"--email", name+"@example.test",
		"--display-name", "Carol Carter",
		"--first-name", "Carol",
		"--middle-name", "Q",
		"--last-name", "Carter",
		"--password", "s3cret-"+randomHex(t, 4))
	c.cleanupMembership("user", "delete", name)
	// With no --key-file the server-generated private key goes to stdout.
	if !strings.Contains(out, "BEGIN RSA PRIVATE KEY") && !strings.Contains(out, "BEGIN PRIVATE KEY") {
		t.Errorf("user create did not print the generated private key:\n%s", out)
	}

	u := showUser(c, name)
	wantEqual(t, "username", u.UserName, name)
	wantEqual(t, "email", u.Email, name+"@example.test")
	wantEqual(t, "display_name", u.DisplayName, "Carol Carter")
	wantEqual(t, "first_name", u.FirstName, "Carol")
	wantEqual(t, "middle_name", u.MiddleName, "Q")
	wantEqual(t, "last_name", u.LastName, "Carter")

	human := c.run("user", "show", name)
	for _, want := range []string{name, "Carol Carter", name + "@example.test"} {
		if !strings.Contains(human, want) {
			t.Errorf("user show missing %q:\n%s", want, human)
		}
	}

	if list := strings.Fields(c.run("user", "list")); !slices.Contains(list, name) {
		t.Errorf("user list does not include %s: %v", name, list)
	}
	if names := listUsers(c); !slices.Contains(names, name) {
		t.Errorf("user list --format json does not include %s: %v", name, names)
	}

	// The file's username is ignored: the argument names the user, so an
	// edit can never rename one by accident.
	file := writeJSON(t, cinc.User{
		UserName:    "ignored",
		DisplayName: "Carol Operator",
		Email:       name + "@example.test",
		FirstName:   "Caroline",
		LastName:    "Carter",
	})
	wantEqual(t, "edit output", c.run("user", "edit", name, "--file", file), fmt.Sprintf("Updated user %q\n", name))
	u = showUser(c, name)
	wantEqual(t, "username after edit", u.UserName, name)
	wantEqual(t, "display_name after edit", u.DisplayName, "Carol Operator")
	wantEqual(t, "first_name after edit", u.FirstName, "Caroline")
	wantNotFound(t, c.fail("user", "show", "ignored"))

	wantEqual(t, "delete output", c.runMembership("user", "delete", name), fmt.Sprintf("Deleted user %q\n", name))
	wantNotFound(t, c.fail("user", "show", name))
	if names := listUsers(c); slices.Contains(names, name) {
		t.Errorf("user list still includes deleted %s", name)
	}
}

// testUserCreateKeyFile proves the key user create writes out is the one the
// server holds: the new user signs a request with it and the server accepts
// it. A user may always read its own record, so no org membership is needed.
func testUserCreateKeyFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "user")
	keyPath := filepath.Join(t.TempDir(), "user.pem")
	out := c.run("user", "create", name,
		"--email", name+"@example.test", "--display-name", "Key File",
		"--password", "pw-"+randomHex(t, 6), "--key-file", keyPath)
	c.cleanupMembership("user", "delete", name)
	if strings.Contains(out, "PRIVATE KEY") {
		t.Errorf("user create --key-file should not print the key:\n%s", out)
	}
	if !strings.Contains(out, keyPath) {
		t.Errorf("user create --key-file output = %q, want it to name %s", out, keyPath)
	}
	if _, err := cinc.LoadKeyFile(keyPath); err != nil {
		t.Fatalf("user create --key-file wrote an unusable key: %v", err)
	}

	u := testUser{name: name, keyPath: keyPath}
	wantEqual(t, "username signed as the user", showUser(c, name, actAs(c, u)...).UserName, name)
}

// testUserCreatePublicKey registers a key pair made here: the server keeps
// the public half and generates nothing, and the private half signs.
func testUserCreatePublicKey(t *testing.T, _ Target, c *cli) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(dir, "user.pub")
	writeFile(t, pubPath, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})))
	keyPath := filepath.Join(dir, "user.pem")
	writeFile(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))

	name := uniqueName(t, "user")
	out := c.run("user", "create", name,
		"--email", name+"@example.test", "--display-name", "Own Key",
		"--password", "pw-"+randomHex(t, 6), "--public-key", pubPath)
	c.cleanupMembership("user", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created user %q\n", name))

	u := testUser{name: name, keyPath: keyPath}
	wantEqual(t, "username signed as the user", showUser(c, name, actAs(c, u)...).UserName, name)
}

func testUserAlreadyExists(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	r := c.fail("user", "create", u.name,
		"--email", "other-"+u.name+"@example.test", "--display-name", "Duplicate",
		"--password", "pw-"+randomHex(t, 6))
	wantFailure(t, r, "already exists", "409", "conflict")
}

func testUserNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("user", "show", ghost))
	wantNotFound(t, c.failMembership("user", "delete", ghost))
	wantNotFound(t, c.fail("user", "password", ghost, "--password", "n3w-s3cret!"))
}

// testUserEditMissing checks that editing a user that does not exist fails
// and does not create it: erchef's named-user endpoint 404s before it reads
// the body.
func testUserEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanupMembership("user", "delete", ghost)
	file := writeJSON(t, cinc.User{DisplayName: "Ghost", Email: ghost + "@example.test"})
	wantNotFound(t, c.fail("user", "edit", ghost, "--file", file))
	wantNotFound(t, c.fail("user", "show", ghost))
}

// testUserPassword sets a password and checks what can be checked without
// POST /authenticate_user, which erchef reserves to pivotal: the password is
// never echoed back, the rest of the record survives the round trip, and the
// user's key still signs (a password update must not clobber the key).
func testUserPassword(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	before := showUser(c, u.name)

	secret := "n3w-s3cret-" + randomHex(t, 6)
	out := c.run("user", "password", u.name, "--password", secret)
	wantEqual(t, "password output", out, fmt.Sprintf("Updated password for user %q\n", u.name))

	after := showUser(c, u.name)
	wantEqual(t, "record after password change", after, before)
	raw := showUserRaw(c, u.name)
	if _, ok := raw["password"]; ok {
		t.Errorf("user show returned a password field: %v", raw)
	}
	for _, args := range [][]string{
		{"user", "show", u.name},
		{"user", "show", u.name, "--format", "json"},
	} {
		if got := c.run(args...); strings.Contains(got, secret) {
			t.Errorf("cinc %s leaked the password:\n%s", strings.Join(args, " "), got)
		}
	}
	wantEqual(t, "username signed as the user", showUser(c, u.name, actAs(c, u)...).UserName, u.name)
}

// testUserPasswordPrompt reads the password from stdin when --password is
// absent, and refuses an empty one before touching the server.
func testUserPasswordPrompt(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	out := c.runWith(runOpts{stdin: "pr0mpted-" + randomHex(t, 4) + "\n"}, "user", "password", u.name)
	if !strings.Contains(out, "New password") || !strings.Contains(out, fmt.Sprintf("Updated password for user %q", u.name)) {
		t.Errorf("user password with a prompt = %q", out)
	}

	r := c.exec(runOpts{stdin: "\n"}, "user", "password", u.name)
	wantFailure(t, r, "non-empty password")
}

// testUserPasswordTooShort checks erchef's password rule: fewer than 6
// characters is a 400 ("Password must have at least 6 characters").
func testUserPasswordTooShort(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	r := c.fail("user", "password", u.name, "--password", "abc")
	wantFailure(t, r, "at least 6 characters", "400")
}

// testUserCreateInvalidName checks erchef's username rule, ^[a-z0-9_-]+$:
// upper case and spaces are a 400, and nothing is created.
func testUserCreateInvalidName(t *testing.T, _ Target, c *cli) {
	for _, name := range []string{
		"T-User-" + randomHex(t, 4),
		"t_user " + randomHex(t, 4),
	} {
		r := c.exec(runOpts{}, "user", "create", name,
			"--email", "bad-"+randomHex(t, 4)+"@example.test", "--display-name", "Bad Name",
			"--password", "pw-"+randomHex(t, 6))
		if r.exitCode == 0 {
			c.cleanupMembership("user", "delete", name)
			t.Errorf("user create %q succeeded, want a 400: %s", name, r)
			continue
		}
		wantFailure(t, r, "malformed user name", "400", "invalid")
		if names := listUsers(c); slices.Contains(names, name) {
			t.Errorf("an invalid user name %q was created anyway", name)
		}
	}
}

// testUserCreateMissingFields checks the fields erchef requires on create:
// display_name, a valid email, and a password. Each omission is a 400.
func testUserCreateMissingFields(t *testing.T, _ Target, c *cli) {
	type attempt struct {
		what  string
		flags []string
		want  []string
	}
	for _, a := range []attempt{
		{"no display name", []string{"--email", "x@example.test", "--password", "pw-123456"}, []string{"display_name", "400"}},
		{"no email", []string{"--display-name", "No Email", "--password", "pw-123456"}, []string{"email", "400"}},
		{"bad email", []string{"--display-name", "Bad Email", "--email", "not-an-email", "--password", "pw-123456"}, []string{"email", "400"}},
		{"no password", []string{"--display-name", "No Password", "--email", "x@example.test"}, []string{"password", "400"}},
	} {
		name := uniqueName(t, "user")
		r := c.exec(runOpts{}, append([]string{"user", "create", name}, a.flags...)...)
		if r.exitCode == 0 {
			c.cleanupMembership("user", "delete", name)
			t.Errorf("%s: user create succeeded, want a 400: %s", a.what, r)
			continue
		}
		wantFailure(t, r, a.want...)
	}
}

// testUserEmailLowercased checks that erchef stores an email in lower case
// (chef_user:common_user_validation), so two users cannot share an address
// that differs only in case.
func testUserEmailLowercased(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "user")
	c.run("user", "create", name,
		"--email", "Mixed."+name+"@Example.TEST", "--display-name", "Mixed Case",
		"--password", "pw-"+randomHex(t, 6))
	c.cleanupMembership("user", "delete", name)
	wantEqual(t, "email", showUser(c, name).Email, "mixed."+name+"@example.test")
}

// testUserEditKeepsOmittedFields checks that erchef merges a user update
// into the stored record (chef_user:update_from_ejson_common): a field the
// new body leaves out keeps its old value.
func testUserEditKeepsOmittedFields(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	file := writeJSON(t, map[string]any{
		"display_name": "Partial Edit",
		"email":        u.name + "@example.test",
	})
	c.run("user", "edit", u.name, "--file", file)
	got := showUser(c, u.name)
	wantEqual(t, "display_name", got.DisplayName, "Partial Edit")
	wantEqual(t, "first_name kept", got.FirstName, "Test")
	wantEqual(t, "last_name kept", got.LastName, "User")
}

// testUserSelfEdit checks that a user may edit its own record: erchef
// authorizes a user PUT against the user's own actor ACL.
func testUserSelfEdit(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	file := writeJSON(t, cinc.User{DisplayName: "Self Edited", Email: u.name + "@example.test", FirstName: "Self"})
	as := actAs(c, u)
	c.run(append([]string{"user", "edit", u.name, "--file", file}, as...)...)
	got := showUser(c, u.name)
	wantEqual(t, "display_name", got.DisplayName, "Self Edited")
	wantEqual(t, "first_name", got.FirstName, "Self")
}

// testUserNonAdminForbidden checks that an ordinary user cannot create,
// edit, delete or reset the password of another user.
func testUserNonAdminForbidden(t *testing.T, _ Target, c *cli) {
	actor := createUser(c)
	victim := createUser(c)
	as := actAs(c, actor)

	ghost := uniqueName(t, "user")
	c.cleanupMembership("user", "delete", ghost)
	wantForbidden(t, c.fail(append([]string{"user", "create", ghost,
		"--email", ghost + "@example.test", "--display-name", "Nope", "--password", "pw-123456"}, as...)...))

	file := writeJSON(t, cinc.User{DisplayName: "Hijacked", Email: victim.name + "@example.test"})
	wantForbidden(t, c.fail(append([]string{"user", "edit", victim.name, "--file", file}, as...)...))
	wantForbidden(t, c.failMembership(append([]string{"user", "delete", victim.name}, as...)...))

	// user password reads the user first, then writes it; either may be the
	// request refused, but it must be refused.
	wantForbidden(t, c.fail(append([]string{"user", "password", victim.name, "--password", "hijack-123"}, as...)...))

	got := showUser(c, victim.name)
	if got.DisplayName == "Hijacked" {
		t.Errorf("a non-admin edited another user: %+v", got)
	}
	wantNotFound(t, c.fail("user", "show", ghost))
}

// testUserDeletePivotalDeclined asks before deleting the pivotal superuser,
// and with no answer on stdin (the No default) never contacts the server.
// Only the decline path is safe to run: pivotal signs erchef's own requests.
func testUserDeletePivotalDeclined(t *testing.T, _ Target, c *cli) {
	r := c.exec(runOpts{}, "user", "delete", "pivotal")
	if r.exitCode != 0 {
		t.Fatalf("declining the pivotal delete should exit cleanly: %s", r)
	}
	if !strings.Contains(r.stderr, "superuser") {
		t.Errorf("want a superuser warning on stderr: %s", r)
	}
	if !strings.Contains(r.stderr, "was not deleted") {
		t.Errorf("want the decline confirmed on stderr: %s", r)
	}
	wantEqual(t, "stdout", r.stdout, "")

	r = c.exec(runOpts{stdin: "n\n"}, "user", "delete", "pivotal")
	if r.exitCode != 0 || r.stdout != "" {
		t.Errorf("answering n should decline cleanly: %s", r)
	}

	if names := listUsers(c); !slices.Contains(names, "pivotal") {
		t.Errorf("pivotal should still exist after declining the delete: %v", names)
	}
}

// testUserDeleteMember deletes a user who belongs to an org. erchef's
// org_user_associations rows cascade on the user's delete, so the org stops
// listing the user.
func testUserDeleteMember(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	joinOrg(c, u)
	if !slices.Contains(orgMembers(c), u.name) {
		t.Fatalf("%s should be a member before the delete", u.name)
	}
	c.runMembership("user", "delete", u.name)
	if members := orgMembers(c); slices.Contains(members, u.name) {
		t.Errorf("org member list still includes deleted user %s: %v", u.name, members)
	}
}

// testUserDeleteInvited deletes a user with a pending invitation. erchef's
// org_user_invites rows cascade on the user's delete too.
func testUserDeleteInvited(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	inviteUser(c, u.name)
	if _, ok := findInvite(c, u.name); !ok {
		t.Fatalf("%s should have a pending invitation before the delete", u.name)
	}
	c.runMembership("user", "delete", u.name)
	if inv, ok := findInvite(c, u.name); ok {
		t.Errorf("org invite list still includes deleted user %s: %+v", u.name, inv)
	}
}
