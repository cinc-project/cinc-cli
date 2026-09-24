package suite

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

var keyFamily = family{cases: []testCase{
	{"keys/client-lifecycle", []string{"client key create", "client key list", "client key show", "client key delete"}, testClientKeyLifecycle},
	{"keys/client-create-flags", []string{"client key create", "client key show"}, testClientKeyCreateFlags},
	{"keys/client-added-key-signs", []string{"client key create", "client key delete"}, testClientKeyAddedKeySigns},
	{"keys/client-expired-key-rejected", []string{"client key create"}, testClientKeyExpiredRejected},
	{"keys/client-invalid-expiration", []string{"client key create"}, testClientKeyInvalidExpiration},
	{"keys/client-delete-default-revokes", []string{"client key delete", "client key list"}, testClientKeyDeleteDefaultRevokes},
	{"keys/client-edit-expiration", []string{"client key show", "client key edit"}, testClientKeyEditExpiration},
	{"keys/client-edit-default-public-key", []string{"client key edit", "client key show"}, testClientKeyEditDefaultPublicKey},
	{"keys/client-edit-default-expiration", []string{"client key edit", "client key show"}, testClientKeyEditDefaultExpiration},
	{"keys/client-edit-partial", []string{"client key edit", "client key show"}, testClientKeyEditPartial},
	{"keys/client-edit-rename", []string{"client key edit", "client key show", "client key list"}, testClientKeyEditRename},
	{"keys/client-edit-create-key", []string{"client key edit"}, testClientKeyEditCreateKey},
	{"keys/client-already-exists", []string{"client key create"}, testClientKeyAlreadyExists},
	{"keys/client-default-already-exists", []string{"client key create"}, testClientKeyDefaultAlreadyExists},
	{"keys/client-not-found", []string{"client key show", "client key delete", "client key edit", "client key list", "client key create"}, testClientKeyNotFound},
	{"keys/client-forbidden", []string{"client key create", "client key delete", "client key edit"}, testClientKeyForbidden},
	{"keys/user-lifecycle", []string{"user key create", "user key list", "user key show", "user key delete"}, testUserKeyLifecycle},
	{"keys/user-edit", []string{"user key show", "user key edit"}, testUserKeyEdit},
	{"keys/user-added-key-signs", []string{"user key create", "user key delete"}, testUserKeyAddedKeySigns},
	{"keys/user-self-service", []string{"user key create", "user key list", "user key delete"}, testUserKeySelfService},
	{"keys/user-not-found", []string{"user key show", "user key delete", "user key edit", "user key list", "user key create"}, testUserKeyNotFound},
}}

// testClientKeyLifecycle adds a server-generated key to a client, reads it
// back through list and show, and deletes it. The added key must be the one
// printed, and adding it must leave the default key alone.
func testClientKeyLifecycle(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	ownerKey := createClient(c, owner)

	out := c.run("client", "key", "create", owner, "rotation")
	wantPrivateKey(t, "client key create", out)

	if list := strings.Fields(c.run("client", "key", "list", owner)); !slices.Contains(list, "rotation") {
		t.Errorf("client key list missing the new key: %v", list)
	}
	wantSlice(t, "keys", keyNames(c, "client", owner), []string{"default", "rotation"})

	if human := c.run("client", "key", "show", owner, "rotation"); !strings.Contains(human, `"name": "rotation"`) {
		t.Errorf("client key show missing the name field:\n%s", human)
	}
	k := showKey(c, "client", owner, "rotation")
	wantEqual(t, "name", k.Name, "rotation")
	wantEqual(t, "expiration_date", k.ExpirationDate, "infinity")
	wantSameKey(t, "the added key's public_key", k.PublicKey, out)
	wantSameKey(t, "the default key's public_key", showKey(c, "client", owner, "default").PublicKey, readFile(t, ownerKey))

	wantEqual(t, "delete output", c.run("client", "key", "delete", owner, "rotation"),
		fmt.Sprintf("Deleted key %q from client %q\n", "rotation", owner))
	wantSlice(t, "keys after delete", keyNames(c, "client", owner), []string{"default"})
	wantNotFound(t, c.fail("client", "key", "show", owner, "rotation"))
	wantSigns(t, c, clientProfile(c, owner, ownerKey), clientProbe())
}

// testClientKeyCreateFlags covers --public-key, --key-file and --expires.
func testClientKeyCreateFlags(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)

	kp := newKeyPair(t)
	wantEqual(t, "--public-key output", c.run("client", "key", "create", owner, "laptop", "--public-key", kp.pubPath),
		fmt.Sprintf("Added key %q to client %q\n", "laptop", owner))
	wantSameKey(t, "the registered key", showKey(c, "client", owner, "laptop").PublicKey, kp.pubPEM)

	keyFile := filepath.Join(t.TempDir(), "ci.pem")
	const expires = "2040-06-30T12:00:00Z"
	wantEqual(t, "--key-file output", c.run("client", "key", "create", owner, "ci", "--key-file", keyFile, "--expires", expires),
		fmt.Sprintf("Added key %q to client %q (key written to %s)\n", "ci", owner, keyFile))
	wantMode(t, keyFile, 0o600)
	k := showKey(c, "client", owner, "ci")
	wantEqual(t, "expiration_date", k.ExpirationDate, expires)
	wantSameKey(t, "the --key-file key", k.PublicKey, readFile(t, keyFile))
}

// testClientKeyAddedKeySigns proves an added key, generated by the server or
// supplied by the caller, authenticates the client, and that deleting it
// revokes it without touching the default key.
func testClientKeyAddedKeySigns(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	defaultProfile := clientProfile(c, owner, createClient(c, owner))

	generated := filepath.Join(t.TempDir(), "generated.pem")
	c.run("client", "key", "create", owner, "generated", "--key-file", generated)
	generatedProfile := clientProfile(c, owner, generated)
	kp := newKeyPair(t)
	c.run("client", "key", "create", owner, "supplied", "--public-key", kp.pubPath)
	suppliedProfile := clientProfile(c, owner, kp.privPath)

	wantSigns(t, c, generatedProfile, clientProbe())
	wantSigns(t, c, suppliedProfile, clientProbe())

	c.run("client", "key", "delete", owner, "generated")
	wantRejected(t, c, generatedProfile, clientProbe())
	wantSigns(t, c, suppliedProfile, clientProbe())
	wantSigns(t, c, defaultProfile, clientProbe())
}

// testClientKeyExpiredRejected adds a key whose expiration date has passed.
// erchef accepts the key but refuses requests signed with it.
func testClientKeyExpiredRejected(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	defaultProfile := clientProfile(c, owner, createClient(c, owner))
	keyFile := filepath.Join(t.TempDir(), "expired.pem")
	c.run("client", "key", "create", owner, "expired", "--key-file", keyFile, "--expires", "2020-01-01T00:00:00Z")
	wantEqual(t, "expiration_date", showKey(c, "client", owner, "expired").ExpirationDate, "2020-01-01T00:00:00Z")
	wantRejected(t, c, clientProfile(c, owner, keyFile), clientProbe())
	wantSigns(t, c, defaultProfile, clientProbe())
}

// testClientKeyInvalidExpiration checks that the server refuses an
// expiration date that is neither "infinity" nor an ISO-8601 timestamp.
func testClientKeyInvalidExpiration(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	wantStatus(t, c.fail("client", "key", "create", owner, "junk", "--expires", "next tuesday"), 400)
	wantNotFound(t, c.fail("client", "key", "show", owner, "junk"))
}

// testClientKeyDeleteDefaultRevokes deletes a client's default key: the key
// disappears from the list and stops authenticating.
func testClientKeyDeleteDefaultRevokes(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	profile := clientProfile(c, owner, createClient(c, owner))
	wantSigns(t, c, profile, clientProbe())
	c.run("client", "key", "delete", owner, "default")
	if names := keyNames(c, "client", owner); slices.Contains(names, "default") {
		t.Errorf("key list still has the deleted default key: %v", names)
	}
	wantNotFound(t, c.fail("client", "key", "show", owner, "default"))
	wantRejected(t, c, profile, clientProbe())
}

// editKeyExpiration reads a key with `key show --format json`, sets a new
// expiration date, writes it back with `key edit --file`, and checks the
// change stuck and the public key did not move.
func editKeyExpiration(t *testing.T, c *cli, noun, owner, keyName string) {
	t.Helper()
	const expires = "2040-12-31T00:00:00Z"
	k := showKey(c, noun, owner, keyName)
	wantEqual(t, "name", k.Name, keyName)
	before := k.PublicKey
	k.ExpirationDate = expires
	wantEqual(t, "edit output", c.run(noun, "key", "edit", owner, keyName, "--file", writeJSON(t, k)),
		fmt.Sprintf("Updated key %q on %s %q\n", keyName, noun, owner))
	after := showKey(c, noun, owner, keyName)
	wantEqual(t, "expiration_date after edit", after.ExpirationDate, expires)
	wantSameKey(t, "public_key after edit", after.PublicKey, before)
}

func testClientKeyEditExpiration(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	c.run("client", "key", "create", owner, "editable")
	editKeyExpiration(t, c, "client", owner, "editable")
}

// testClientKeyEditDefaultPublicKey replaces a client's default public key
// with key edit: the new private key signs and the old one is refused.
func testClientKeyEditDefaultPublicKey(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	oldProfile := clientProfile(c, owner, createClient(c, owner))
	kp := newKeyPair(t)
	file := writeJSON(t, cinc.Key{Name: "default", PublicKey: kp.pubPEM, ExpirationDate: "infinity"})
	c.run("client", "key", "edit", owner, "default", "--file", file)
	wantSameKey(t, "default key after edit", showKey(c, "client", owner, "default").PublicKey, kp.pubPEM)
	wantSigns(t, c, clientProfile(c, owner, kp.privPath), clientProbe())
	wantRejected(t, c, oldProfile, clientProbe())
}

// testClientKeyEditDefaultExpiration edits the expiration date of the key
// client create made.
func testClientKeyEditDefaultExpiration(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	editKeyExpiration(t, c, "client", owner, "default")
}

// testClientKeyEditPartial edits a key with a body holding only the
// expiration date. erchef keeps the stored value of every field the body
// omits, so the key's public key survives.
func testClientKeyEditPartial(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	keyFile := filepath.Join(t.TempDir(), "partial.pem")
	c.run("client", "key", "create", owner, "partial", "--key-file", keyFile)

	const expires = "2041-01-01T00:00:00Z"
	c.run("client", "key", "edit", owner, "partial", "--file", writeJSON(t, map[string]string{"expiration_date": expires}))
	k := showKey(c, "client", owner, "partial")
	wantEqual(t, "expiration_date", k.ExpirationDate, expires)
	if k.PublicKey == "" {
		t.Fatal("a partial key edit dropped the key's public_key")
	}
	wantSameKey(t, "public_key after a partial edit", k.PublicKey, readFile(t, keyFile))
}

// testClientKeyEditRename renames a key by giving the edit body a new name.
func testClientKeyEditRename(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	keyFile := filepath.Join(t.TempDir(), "old.pem")
	c.run("client", "key", "create", owner, "old-name", "--key-file", keyFile)

	c.run("client", "key", "edit", owner, "old-name", "--file", writeJSON(t, map[string]string{"name": "new-name"}))
	wantSameKey(t, "the renamed key", showKey(c, "client", owner, "new-name").PublicKey, readFile(t, keyFile))
	wantNotFound(t, c.fail("client", "key", "show", owner, "old-name"))
	wantSlice(t, "keys after rename", keyNames(c, "client", owner), []string{"default", "new-name"})
}

// testClientKeyEditCreateKey asks the server to regenerate a key in place
// ({"create_key": true}, knife's `key edit --create-key`). erchef answers
// with the new private key, which the CLI must hand to the user: it is the
// only copy, and the key's old private half no longer matches.
func testClientKeyEditCreateKey(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	oldFile := filepath.Join(t.TempDir(), "old.pem")
	c.run("client", "key", "create", owner, "rotating", "--key-file", oldFile)

	out := c.run("client", "key", "edit", owner, "rotating", "--file", writeJSON(t, map[string]any{"create_key": true}))
	wantPrivateKey(t, "key edit with create_key", out)
	k := showKey(c, "client", owner, "rotating")
	wantSameKey(t, "the regenerated key", k.PublicKey, out)
	if sameKey(t, k.PublicKey, readFile(t, oldFile)) {
		t.Fatal("create_key did not replace the key")
	}
}

func testClientKeyAlreadyExists(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	keyFile := filepath.Join(t.TempDir(), "dup.pem")
	c.run("client", "key", "create", owner, "dup", "--key-file", keyFile)
	wantConflict(t, c.fail("client", "key", "create", owner, "dup"))
	wantConflict(t, c.fail("client", "key", "create", owner, "dup", "--public-key", newKeyPair(t).pubPath))
	wantSameKey(t, "the original key", showKey(c, "client", owner, "dup").PublicKey, readFile(t, keyFile))
}

// testClientKeyDefaultAlreadyExists adds a key named "default" to a client
// whose default key client create made. It is a key like any other, so the
// server refuses the duplicate and the client keeps its key.
func testClientKeyDefaultAlreadyExists(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	keyPath := createClient(c, owner)
	wantConflict(t, c.fail("client", "key", "create", owner, "default", "--public-key", newKeyPair(t).pubPath))
	wantConflict(t, c.fail("client", "key", "create", owner, "default"))
	wantSameKey(t, "the default key", showKey(c, "client", owner, "default").PublicKey, readFile(t, keyPath))
	wantSigns(t, c, clientProfile(c, owner, keyPath), clientProbe())
}

func testClientKeyNotFound(t *testing.T, _ Target, c *cli) {
	owner := uniqueName(t, "client")
	createClient(c, owner)
	file := writeJSON(t, cinc.Key{ExpirationDate: "2040-01-01T00:00:00Z"})
	wantNotFound(t, c.fail("client", "key", "show", owner, "ghost"))
	wantNotFound(t, c.fail("client", "key", "delete", owner, "ghost"))
	wantNotFound(t, c.fail("client", "key", "edit", owner, "ghost", "--file", file))
	wantNotFound(t, c.fail("client", "key", "show", owner, "ghost"))

	ghost := uniqueName(t, "ghost")
	c.cleanup("client", "delete", ghost)
	wantNotFound(t, c.fail("client", "key", "list", ghost))
	wantNotFound(t, c.fail("client", "key", "show", ghost, "default"))
	wantNotFound(t, c.fail("client", "key", "create", ghost, "k"))
	wantNotFound(t, c.fail("client", "show", ghost))
}

// testClientKeyForbidden signs as a plain client, which may not change
// another client's keys.
func testClientKeyForbidden(t *testing.T, _ Target, c *cli) {
	actor := uniqueName(t, "client")
	as := clientProfile(c, actor, createClient(c, actor))
	victim := uniqueName(t, "client")
	createClient(c, victim)

	file := writeJSON(t, cinc.Key{Name: "default", PublicKey: newKeyPair(t).pubPEM, ExpirationDate: "infinity"})
	for _, args := range [][]string{
		{"client", "key", "create", victim, "sneaky"},
		{"client", "key", "create", victim, "sneaky", "--public-key", newKeyPair(t).pubPath},
		{"client", "key", "edit", victim, "default", "--file", file},
		{"client", "key", "delete", victim, "default"},
	} {
		wantStatus(t, c.fail(append(args, "--profile", as)...), 403)
	}
	wantSlice(t, "victim keys", keyNames(c, "client", victim), []string{"default"})
}

// testUserKeyLifecycle is the client key lifecycle on a global user's keys,
// which live at /users/NAME/keys rather than under the org.
func testUserKeyLifecycle(t *testing.T, _ Target, c *cli) {
	user := uniqueName(t, "user")
	userKey := createNamedUser(c, user)
	wantSlice(t, "keys", keyNames(c, "user", user), []string{"default"})
	wantSameKey(t, "default key", showKey(c, "user", user, "default").PublicKey, readFile(t, userKey))
	wantSigns(t, c, userProfile(c, user, userKey), userProbe(user))

	out := c.run("user", "key", "create", user, "laptop")
	wantPrivateKey(t, "user key create", out)
	if list := strings.Fields(c.run("user", "key", "list", user)); !slices.Contains(list, "laptop") {
		t.Errorf("user key list missing the new key: %v", list)
	}
	wantSlice(t, "keys", keyNames(c, "user", user), []string{"default", "laptop"})
	if human := c.run("user", "key", "show", user, "laptop"); !strings.Contains(human, `"name": "laptop"`) {
		t.Errorf("user key show missing the name field:\n%s", human)
	}
	wantSameKey(t, "the added key", showKey(c, "user", user, "laptop").PublicKey, out)
	wantConflict(t, c.fail("user", "key", "create", user, "laptop"))

	wantEqual(t, "delete output", c.run("user", "key", "delete", user, "laptop"),
		fmt.Sprintf("Deleted key %q from user %q\n", "laptop", user))
	wantSlice(t, "keys after delete", keyNames(c, "user", user), []string{"default"})
	wantNotFound(t, c.fail("user", "key", "show", user, "laptop"))
}

func testUserKeyEdit(t *testing.T, _ Target, c *cli) {
	user := uniqueName(t, "user")
	createNamedUser(c, user)
	kp := newKeyPair(t)
	wantEqual(t, "--public-key output", c.run("user", "key", "create", user, "editable", "--public-key", kp.pubPath),
		fmt.Sprintf("Added key %q to user %q\n", "editable", user))
	editKeyExpiration(t, c, "user", user, "editable")
}

// testUserKeyAddedKeySigns proves a key added to a user authenticates that
// user, and stops once deleted.
func testUserKeyAddedKeySigns(t *testing.T, _ Target, c *cli) {
	user := uniqueName(t, "user")
	createNamedUser(c, user)
	keyFile := filepath.Join(t.TempDir(), "laptop.pem")
	c.run("user", "key", "create", user, "laptop", "--key-file", keyFile)
	profile := userProfile(c, user, keyFile)
	wantSigns(t, c, profile, userProbe(user))
	c.run("user", "key", "delete", user, "laptop")
	wantRejected(t, c, profile, userProbe(user))
}

// testUserKeySelfService signs as an ordinary user, who may manage their own
// keys but not another user's.
func testUserKeySelfService(t *testing.T, _ Target, c *cli) {
	user := uniqueName(t, "user")
	as := userProfile(c, user, createNamedUser(c, user))
	other := uniqueName(t, "user")
	createNamedUser(c, other)

	kp := newKeyPair(t)
	c.run("user", "key", "create", user, "own", "--public-key", kp.pubPath, "--profile", as)
	var names []string
	c.json(&names, "user", "key", "list", user, "--profile", as)
	wantSlice(t, "own keys", names, []string{"default", "own"})
	c.run("user", "key", "delete", user, "own", "--profile", as)
	wantSlice(t, "own keys after delete", keyNames(c, "user", user), []string{"default"})

	wantStatus(t, c.fail("user", "key", "create", other, "sneaky", "--public-key", kp.pubPath, "--profile", as), 403)
	wantStatus(t, c.fail("user", "key", "delete", other, "default", "--profile", as), 403)
	wantSlice(t, "other user's keys", keyNames(c, "user", other), []string{"default"})
}

func testUserKeyNotFound(t *testing.T, _ Target, c *cli) {
	user := uniqueName(t, "user")
	createNamedUser(c, user)
	file := writeJSON(t, cinc.Key{ExpirationDate: "2040-01-01T00:00:00Z"})
	wantNotFound(t, c.fail("user", "key", "show", user, "ghost"))
	wantNotFound(t, c.fail("user", "key", "delete", user, "ghost"))
	wantNotFound(t, c.fail("user", "key", "edit", user, "ghost", "--file", file))

	ghost := uniqueName(t, "ghost")
	c.cleanup("user", "delete", ghost)
	wantNotFound(t, c.fail("user", "key", "list", ghost))
	wantNotFound(t, c.fail("user", "key", "create", ghost, "k"))
}

// userProfile adds a profile signing as user name with keyPath and returns
// the profile name. User requests go to /users, outside any org, but the
// profile still names the target's org as its server URL.
func userProfile(c *cli, name, keyPath string) string {
	c.t.Helper()
	return clientProfile(c, name, keyPath)
}
