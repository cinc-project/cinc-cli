package suite

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

var clientFamily = family{cases: []testCase{
	{"clients/lifecycle", []string{"client create", "client show", "client list", "client edit", "client delete"}, testClientLifecycle},
	{"clients/create-generated-key-signs", []string{"client create", "client key show", "client key list"}, testClientCreateGeneratedKeySigns},
	{"clients/create-public-key", []string{"client create", "client key show", "client key list"}, testClientCreatePublicKey},
	{"clients/create-missing-public-key-file", []string{"client create"}, testClientCreateMissingPublicKeyFile},
	{"clients/create-invalid-public-key", []string{"client create"}, testClientCreateInvalidPublicKey},
	{"clients/create-invalid-name", []string{"client create"}, testClientCreateInvalidName},
	{"clients/validator", []string{"client create", "client show", "client edit"}, testClientValidator},
	{"clients/already-exists", []string{"client create"}, testClientAlreadyExists},
	{"clients/not-found", []string{"client show", "client delete", "client reregister"}, testClientNotFound},
	{"clients/edit-missing", []string{"client edit"}, testClientEditMissing},
	{"clients/edit-keeps-key", []string{"client edit", "client key show"}, testClientEditKeepsKey},
	{"clients/edit-show-round-trip", []string{"client show", "client edit"}, testClientEditShowRoundTrip},
	{"clients/reregister", []string{"client reregister", "client key list", "client key show"}, testClientReregister},
	{"clients/reregister-new-key-signs", []string{"client reregister"}, testClientReregisterNewKeySigns},
	{"clients/reregister-keeps-other-keys", []string{"client reregister", "client key create", "client key list"}, testClientReregisterKeepsOtherKeys},
	{"clients/reregister-without-default-key", []string{"client reregister", "client key delete"}, testClientReregisterWithoutDefaultKey},
	{"clients/delete-revokes-key", []string{"client delete"}, testClientDeleteRevokesKey},
	{"clients/forbidden", []string{"client create", "client delete", "client edit", "client reregister"}, testClientForbidden},
}}

func showClient(c *cli, name string) cinc.APIClient {
	c.t.Helper()
	var cl cinc.APIClient
	c.json(&cl, "client", "show", name)
	return cl
}

func clientNames(c *cli) []string {
	c.t.Helper()
	var names []string
	c.json(&names, "client", "list")
	return names
}

// testClientLifecycle walks a client through create, show, list, edit and
// delete, checking the human and JSON output of each.
func testClientLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	keyPath := filepath.Join(t.TempDir(), "client.pem")
	out := c.run("client", "create", name, "--key-file", keyPath)
	c.cleanup("client", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created client %q (key written to %s)\n", name, keyPath))
	wantPrivateKey(t, "the --key-file", readFile(t, keyPath))
	wantMode(t, keyPath, 0o600)

	cl := showClient(c, name)
	wantEqual(t, "name", cl.Name, name)
	wantEqual(t, "validator", cl.Validator, false)
	if human := c.run("client", "show", name); !strings.Contains(human, fmt.Sprintf("%q: %q", "name", name)) {
		t.Errorf("client show missing the name field:\n%s", human)
	}

	if list := strings.Fields(c.run("client", "list")); !slices.Contains(list, name) {
		t.Errorf("client list does not include %s: %v", name, list)
	}
	if names := clientNames(c); !slices.Contains(names, name) {
		t.Errorf("client list --format json does not include %s: %v", name, names)
	}

	file := writeJSON(t, cinc.APIClient{Name: name, Validator: true})
	wantEqual(t, "edit output", c.run("client", "edit", name, "--file", file), fmt.Sprintf("Updated client %q\n", name))
	wantEqual(t, "validator after edit", showClient(c, name).Validator, true)

	wantEqual(t, "delete output", c.run("client", "delete", name), fmt.Sprintf("Deleted client %q\n", name))
	wantNotFound(t, c.fail("client", "show", name))
	if names := clientNames(c); slices.Contains(names, name) {
		t.Errorf("client list still includes deleted %s", name)
	}
}

// testClientCreateGeneratedKeySigns has the server generate the key, streamed
// to stdout, and proves it is the client's default key by signing with it.
func testClientCreateGeneratedKeySigns(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	out := c.run("client", "create", name)
	c.cleanup("client", "delete", name)
	wantPrivateKey(t, "client create", out)

	wantSlice(t, "keys", keyNames(c, "client", name), []string{"default"})
	def := showKey(c, "client", name, "default")
	wantSameKey(t, "the default key's public_key", def.PublicKey, out)
	wantEqual(t, "default key expiration", def.ExpirationDate, "infinity")

	wantSigns(t, c, clientProfile(c, name, writeKey(t, out)), clientProbe())
}

// testClientCreatePublicKey registers a locally generated public key: the
// server must store that key, return no private key, and accept requests
// signed with the local private half.
func testClientCreatePublicKey(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	kp := newKeyPair(t)
	out := c.run("client", "create", name, "--public-key", kp.pubPath)
	c.cleanup("client", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created client %q\n", name))

	wantSlice(t, "keys", keyNames(c, "client", name), []string{"default"})
	wantSameKey(t, "the default key's public_key", showKey(c, "client", name, "default").PublicKey, kp.pubPEM)
	wantSigns(t, c, clientProfile(c, name, kp.privPath), clientProbe())

	// A different, unregistered key for the same client is refused.
	wantRejected(t, c, clientProfile(c, name, newKeyPair(t).privPath), clientProbe())
}

// testClientCreateMissingPublicKeyFile checks that a --public-key that cannot
// be read fails before anything reaches the server.
func testClientCreateMissingPublicKeyFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	c.cleanup("client", "delete", name)
	missing := filepath.Join(t.TempDir(), "absent.pub")
	r := c.fail("client", "create", name, "--public-key", missing)
	if !strings.Contains(r.stderr, missing) {
		t.Errorf("the error should name the missing file: %s", r)
	}
	wantNotFound(t, c.fail("client", "show", name))
}

// testClientCreateInvalidPublicKey registers something that is not a public
// key. erchef validates the PEM and answers 400; nothing is created.
func testClientCreateInvalidPublicKey(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	c.cleanup("client", "delete", name)
	bad := filepath.Join(t.TempDir(), "bad.pub")
	writeFile(t, bad, "this is not a public key\n")
	wantStatus(t, c.fail("client", "create", name, "--public-key", bad), 400)
	wantNotFound(t, c.fail("client", "show", name))
}

// testClientCreateInvalidName creates a client whose name has characters
// erchef's name rule (letters, digits, '_', '-', '.') forbids.
func testClientCreateInvalidName(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client") + "!bad name"
	c.cleanup("client", "delete", name)
	wantStatus(t, c.fail("client", "create", name), 400)
}

// testClientValidator creates a validator, checks the flag round-trips and
// that the validator's key authenticates (a validator may be refused most
// reads, but with a 403, not a 401), then turns the flag off with edit.
func testClientValidator(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "validator")
	keyPath := createClient(c, name, "--validator")
	wantEqual(t, "validator", showClient(c, name).Validator, true)
	if human := c.run("client", "show", name); !strings.Contains(human, `"validator": true`) {
		t.Errorf("client show should say it is a validator:\n%s", human)
	}
	wantSigns(t, c, clientProfile(c, name, keyPath), clientProbe())

	c.run("client", "edit", name, "--file", writeJSON(t, cinc.APIClient{Name: name, Validator: false}))
	wantEqual(t, "validator after edit", showClient(c, name).Validator, false)
}

// testClientAlreadyExists re-creates an existing client. The server must
// refuse it, and must not have replaced the existing client's key.
func testClientAlreadyExists(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	keyPath := createClient(c, name)
	wantConflict(t, c.fail("client", "create", name))
	wantConflict(t, c.fail("client", "create", name, "--public-key", newKeyPair(t).pubPath))
	wantSigns(t, c, clientProfile(c, name, keyPath), clientProbe())
}

func testClientNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("client", "show", ghost))
	wantNotFound(t, c.fail("client", "delete", ghost))
	// reregister must not create the client it cannot find.
	c.cleanup("client", "delete", ghost)
	wantNotFound(t, c.fail("client", "reregister", ghost))
	wantNotFound(t, c.fail("client", "show", ghost))
}

// testClientEditMissing checks that editing a client that does not exist
// fails and does not create it.
func testClientEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("client", "delete", ghost)
	file := writeJSON(t, cinc.APIClient{Name: ghost, Validator: true})
	wantNotFound(t, c.fail("client", "edit", ghost, "--file", file))
	wantNotFound(t, c.fail("client", "show", ghost))
}

// testClientEditKeepsKey edits a client with a file that also carries a
// public_key. Under API v1 erchef rejects key fields on a client PUT (keys
// are managed through /keys), so the CLI must leave them out: the edit
// succeeds and the client keeps signing with its original key.
func testClientEditKeepsKey(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	keyPath := createClient(c, name)
	original := showKey(c, "client", name, "default").PublicKey

	file := writeJSON(t, map[string]any{
		"name":       name,
		"validator":  false,
		"public_key": newKeyPair(t).pubPEM,
		"create_key": true,
	})
	c.run("client", "edit", name, "--file", file)
	wantSameKey(t, "default key after edit", showKey(c, "client", name, "default").PublicKey, original)
	wantSigns(t, c, clientProfile(c, name, keyPath), clientProbe())
}

// testClientEditShowRoundTrip feeds `client show --format json` straight back
// into `client edit --file`, the scripted way to edit a client.
func testClientEditShowRoundTrip(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	keyPath := createClient(c, name, "--validator")
	file := filepath.Join(t.TempDir(), "client.json")
	writeFile(t, file, c.run("client", "show", name, "--format", "json"))
	wantEqual(t, "edit output", c.run("client", "edit", name, "--file", file), fmt.Sprintf("Updated client %q\n", name))
	wantEqual(t, "validator", showClient(c, name).Validator, true)
	wantSigns(t, c, clientProfile(c, name, keyPath), clientProbe())
}

// testClientReregister regenerates a client's default key: the new key is
// printed (or written with --key-file), becomes the default key, and the old
// key stops working.
func testClientReregister(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	oldKey := createClient(c, name)
	oldProfile := clientProfile(c, name, oldKey)
	wantSigns(t, c, oldProfile, clientProbe())

	out := c.run("client", "reregister", name)
	wantPrivateKey(t, "client reregister", out)
	if sameKey(t, out, readFile(t, oldKey)) {
		t.Fatal("reregister printed the old key")
	}
	wantSlice(t, "keys after reregister", keyNames(c, "client", name), []string{"default"})
	wantSameKey(t, "default key after reregister", showKey(c, "client", name, "default").PublicKey, out)
	wantRejected(t, c, oldProfile, clientProbe())

	keyFile := filepath.Join(t.TempDir(), "new.pem")
	wantEqual(t, "reregister --key-file output", c.run("client", "reregister", name, "--key-file", keyFile),
		fmt.Sprintf("Reregistered client %q (key written to %s)\n", name, keyFile))
	wantMode(t, keyFile, 0o600)
	wantSameKey(t, "default key after the second reregister", showKey(c, "client", name, "default").PublicKey, readFile(t, keyFile))
}

// testClientReregisterNewKeySigns checks the key reregister hands back
// actually authenticates.
func testClientReregisterNewKeySigns(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	createClient(c, name)
	out := c.run("client", "reregister", name)
	wantSigns(t, c, clientProfile(c, name, writeKey(t, out)), clientProbe())
}

// testClientReregisterKeepsOtherKeys checks reregister replaces only the
// default key: a client's other keys are untouched.
func testClientReregisterKeepsOtherKeys(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	createClient(c, name)
	kp := newKeyPair(t)
	c.run("client", "key", "create", name, "backup", "--public-key", kp.pubPath)
	c.run("client", "reregister", name)
	wantSlice(t, "keys after reregister", keyNames(c, "client", name), []string{"backup", "default"})
	wantSameKey(t, "backup key after reregister", showKey(c, "client", name, "backup").PublicKey, kp.pubPEM)
}

// testClientReregisterWithoutDefaultKey reregisters a client whose default
// key was deleted: there is nothing to replace, so a default key is created.
func testClientReregisterWithoutDefaultKey(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	createClient(c, name)
	c.run("client", "key", "delete", name, "default")
	out := c.run("client", "reregister", name)
	wantPrivateKey(t, "client reregister", out)
	wantSlice(t, "keys after reregister", keyNames(c, "client", name), []string{"default"})
	wantSameKey(t, "default key after reregister", showKey(c, "client", name, "default").PublicKey, out)
}

func testClientDeleteRevokesKey(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "client")
	profile := clientProfile(c, name, createClient(c, name))
	wantSigns(t, c, profile, clientProbe())
	c.run("client", "delete", name)
	wantRejected(t, c, profile, clientProbe())
}

// testClientForbidden signs as a plain (non-admin) client, which may not
// create clients or change another client, and checks the victim is intact
// afterwards: in particular reregister must not have deleted its key before
// being refused.
func testClientForbidden(t *testing.T, _ Target, c *cli) {
	actor := uniqueName(t, "client")
	as := clientProfile(c, actor, createClient(c, actor))
	victim := uniqueName(t, "client")
	victimKey := createClient(c, victim)

	newName := uniqueName(t, "client")
	c.cleanup("client", "delete", newName)
	file := writeJSON(t, cinc.APIClient{Name: victim, Validator: true})
	for _, args := range [][]string{
		{"client", "create", newName},
		{"client", "delete", victim},
		{"client", "edit", victim, "--file", file},
		{"client", "reregister", victim},
	} {
		wantStatus(t, c.fail(append(args, "--profile", as)...), 403)
	}
	wantNotFound(t, c.fail("client", "show", newName))
	wantEqual(t, "victim validator", showClient(c, victim).Validator, false)
	wantSigns(t, c, clientProfile(c, victim, victimKey), clientProbe())
}
