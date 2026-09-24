package suite

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var databagFamily = family{cases: []testCase{
	{"databags/lifecycle", []string{
		"databag create", "databag list", "databag show", "databag delete",
		"databag item create", "databag item list", "databag item show", "databag item edit", "databag item delete",
	}, testDatabagLifecycle},
	{"databags/empty-bag", []string{"databag show", "databag item list"}, testDatabagEmptyBag},
	{"databags/create-with-item", []string{"databag create", "databag item show"}, testDatabagCreateWithItem},
	{"databags/create-with-item-bad-file", []string{"databag create", "databag list"}, testDatabagCreateWithItemBadFile},
	{"databags/item-show-exact", []string{"databag item create", "databag item show", "databag item edit"}, testDatabagItemShowExact},
	{"databags/item-id-mismatch", []string{"databag item create", "databag item edit", "databag item list"}, testDatabagItemIDMismatch},
	{"databags/special-names", []string{"databag create", "databag item create", "databag item show", "databag item delete"}, testDatabagSpecialNames},
	{"databags/invalid-names", []string{"databag create", "databag item create"}, testDatabagInvalidNames},
	{"databags/already-exists", []string{"databag create", "databag item create", "databag secret create"}, testDatabagAlreadyExists},
	{"databags/bag-not-found", []string{
		"databag show", "databag delete", "databag item list", "databag item show", "databag item create",
		"databag item edit", "databag item delete", "databag secret create", "databag secret show", "databag secret edit",
	}, testDatabagBagNotFound},
	{"databags/item-not-found", []string{"databag item show", "databag item delete", "databag secret show", "databag secret edit"}, testDatabagItemNotFound},
	{"databags/edit-missing", []string{"databag item edit", "databag secret edit"}, testDatabagEditMissing},
	{"databags/delete-with-items", []string{"databag delete", "databag item list"}, testDatabagDeleteWithItems},
	{"databags/forbidden", []string{
		"databag list", "databag show", "databag item show", "databag create", "databag delete",
		"databag item create", "databag item edit", "databag item delete", "databag secret create",
	}, testDatabagForbidden},
	{"databags/secret-round-trip", []string{"databag secret create", "databag secret show", "databag item show", "databag item list"}, testDatabagSecretRoundTrip},
	{"databags/secret-chef-can-decrypt", []string{"databag secret create", "databag item show"}, testDatabagSecretChefCanDecrypt},
	{"databags/secret-reads-chef-formats", []string{"databag item create", "databag secret show", "databag secret edit"}, testDatabagSecretReadsChefFormats},
	{"databags/secret-edit", []string{"databag secret edit", "databag secret show", "databag item show"}, testDatabagSecretEdit},
	{"databags/secret-wrong-secret", []string{"databag secret show", "databag secret edit"}, testDatabagSecretWrongSecret},
	{"databags/secret-not-encrypted", []string{"databag secret show", "databag secret edit"}, testDatabagSecretNotEncrypted},
	{"databags/secret-sources", []string{"databag secret create", "databag secret show"}, testDatabagSecretSources},
	{"databags/secret-missing", []string{"databag secret create", "databag secret show", "databag secret edit"}, testDatabagSecretMissing},
	{"databags/secret-file-whitespace", []string{"databag secret create", "databag secret show"}, testDatabagSecretFileWhitespace},
	{"databags/secret-file-empty", []string{"databag secret create", "databag secret show"}, testDatabagSecretFileEmpty},
}}

func testDatabagLifecycle(t *testing.T, _ Target, c *cli) {
	bag := uniqueName(t, "bag")
	out := c.run("databag", "create", bag)
	c.cleanup("databag", "delete", bag)
	wantEqual(t, "create output", out, fmt.Sprintf("Created data bag %q\n", bag))

	if !slices.Contains(databagNames(c), bag) {
		t.Errorf("databag list --format json does not include %s", bag)
	}
	if human := strings.Fields(c.run("databag", "list")); !slices.Contains(human, bag) {
		t.Errorf("databag list does not include %s: %v", bag, human)
	}

	// Two items, created out of order, so the sorted listings are checked.
	for _, item := range []map[string]any{
		{"id": "carol", "role": "admin"},
		{"id": "alice", "role": "viewer"},
	} {
		id := item["id"].(string)
		out := c.run("databag", "item", "create", bag, id, "--file", writeJSON(t, item))
		wantEqual(t, "item create output", out, fmt.Sprintf("Created item %q in data bag %q\n", id, bag))
	}
	wantEqual(t, "item list", c.run("databag", "item", "list", bag), "alice\ncarol\n")
	wantSlice(t, "item list --format json", databagItemIDs(c, bag), []string{"alice", "carol"})
	wantEqual(t, "databag show", c.run("databag", "show", bag), "alice\ncarol\n")
	var shown []string
	c.json(&shown, "databag", "show", bag)
	wantSlice(t, "databag show --format json", shown, []string{"alice", "carol"})

	human := c.run("databag", "item", "show", bag, "alice")
	for _, want := range []string{`"id"`, `"alice"`, `"role"`, `"viewer"`} {
		if !strings.Contains(human, want) {
			t.Errorf("databag item show missing %s:\n%s", want, human)
		}
	}
	if got := databagItemShow(c, bag, "alice"); !reflect.DeepEqual(got, map[string]any{"id": "alice", "role": "viewer"}) {
		t.Errorf("databag item show --format json = %v", got)
	}

	file := writeJSON(t, map[string]any{"id": "alice", "role": "editor"})
	wantEqual(t, "item edit output", c.run("databag", "item", "edit", bag, "alice", "--file", file),
		fmt.Sprintf("Updated item %q in bag %q\n", "alice", bag))
	if got := databagItemShow(c, bag, "alice"); !reflect.DeepEqual(got, map[string]any{"id": "alice", "role": "editor"}) {
		t.Errorf("item after edit = %v, want role editor", got)
	}

	wantEqual(t, "item delete output", c.run("databag", "item", "delete", bag, "alice"),
		fmt.Sprintf("Deleted item %q from data bag %q\n", "alice", bag))
	wantNotFound(t, c.fail("databag", "item", "show", bag, "alice"))
	wantSlice(t, "item list after delete", databagItemIDs(c, bag), []string{"carol"})

	wantEqual(t, "delete output", c.run("databag", "delete", bag), fmt.Sprintf("Deleted data bag %q\n", bag))
	wantNotFound(t, c.fail("databag", "show", bag))
	if slices.Contains(databagNames(c), bag) {
		t.Errorf("databag list still includes deleted %s", bag)
	}
}

// testDatabagEmptyBag checks that a bag with no items lists nothing in human
// output and an empty array, not null, in JSON.
func testDatabagEmptyBag(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	wantEqual(t, "databag show", c.run("databag", "show", bag), "")
	wantEqual(t, "databag item list", c.run("databag", "item", "list", bag), "")
	wantEqual(t, "databag show --format json", strings.TrimSpace(c.run("databag", "show", bag, "--format", "json")), "[]")
	wantEqual(t, "databag item list --format json", strings.TrimSpace(c.run("databag", "item", "list", bag, "--format", "json")), "[]")
}

// testDatabagCreateWithItem covers the two-argument `databag create BAG ITEM`
// form: it creates the bag and the item, and into an existing bag it adds
// the item without complaining that the bag is already there.
func testDatabagCreateWithItem(t *testing.T, _ Target, c *cli) {
	bag := uniqueName(t, "bag")
	c.cleanup("databag", "delete", bag)
	file := writeJSON(t, map[string]any{"id": "db-password", "password": "hunter2"})
	out := c.run("databag", "create", bag, "db-password", "--file", file)
	wantEqual(t, "create output", out,
		fmt.Sprintf("Created data bag %q\nCreated item %q in data bag %q\n", bag, "db-password", bag))
	if got := databagItemShow(c, bag, "db-password"); !reflect.DeepEqual(got, map[string]any{"id": "db-password", "password": "hunter2"}) {
		t.Errorf("item = %v", got)
	}

	file = writeJSON(t, map[string]any{"id": "api-key", "token": "abc"})
	out = c.run("databag", "create", bag, "api-key", "--file", file)
	wantEqual(t, "create into an existing bag", out, fmt.Sprintf("Created item %q in data bag %q\n", "api-key", bag))
	wantSlice(t, "items", databagItemIDs(c, bag), []string{"api-key", "db-password"})
}

// testDatabagCreateWithItemBadFile checks that `databag create BAG ITEM
// --file` with an unreadable or invalid file fails without creating the bag:
// the CLI validates its input before it changes anything on the server.
func testDatabagCreateWithItemBadFile(t *testing.T, _ Target, c *cli) {
	for name, content := range map[string]string{
		"invalid JSON": `{"id": "x",`,
		"missing id":   `{"password": "hunter2"}`,
	} {
		bag := uniqueName(t, "bag")
		c.cleanup("databag", "delete", bag)
		path := databagSecretFile(t, content)
		c.fail("databag", "create", bag, "item", "--file", path)
		if slices.Contains(databagNames(c), bag) {
			t.Errorf("%s: databag create created bag %s although the item file was rejected", name, bag)
		}
	}
	bag := uniqueName(t, "bag")
	c.cleanup("databag", "delete", bag)
	c.fail("databag", "create", bag, "item", "--file", "/nonexistent/item.json")
	if slices.Contains(databagNames(c), bag) {
		t.Errorf("databag create created bag %s although the item file does not exist", bag)
	}

	// --file only means something when an item is named; without one it
	// is refused rather than ignored.
	bag = uniqueName(t, "bag")
	c.cleanup("databag", "delete", bag)
	databagWantStderr(t, c.fail("databag", "create", bag, "--file", writeJSON(t, map[string]any{"id": "x"})), "--file", "item")
	if slices.Contains(databagNames(c), bag) {
		t.Errorf("databag create --file without an item created bag %s", bag)
	}
}

// testDatabagItemShowExact checks that `databag item show --format json`
// prints exactly the stored item, every JSON type intact. erchef adds
// chef_type and data_bag to the item it echoes back from a create or update;
// neither may leak into what the CLI shows, nor be written back by an edit.
func testDatabagItemShowExact(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	item := map[string]any{
		"id":      "app",
		"port":    float64(8080),
		"ratio":   0.5,
		"enabled": true,
		"nothing": nil,
		"tags":    []any{"a", "b"},
		"nested":  map[string]any{"deep": map[string]any{"list": []any{float64(1), "two", false}}},
		"unicode": "caf\u00e9 \u2713",
	}
	databagItemCreate(c, bag, item)
	if got := databagItemShow(c, bag, "app"); !reflect.DeepEqual(got, item) {
		t.Errorf("item show = %v\nwant exactly %v", got, item)
	}

	item["port"] = float64(9090)
	c.run("databag", "item", "edit", bag, "app", "--file", writeJSON(t, item))
	if got := databagItemShow(c, bag, "app"); !reflect.DeepEqual(got, item) {
		t.Errorf("item show after edit = %v\nwant exactly %v", got, item)
	}
}

// testDatabagItemIDMismatch checks that a --file naming another item is
// refused, naming both ids: create stores nothing, and edit leaves the
// argument's item as it was rather than overwriting it with the file.
func testDatabagItemIDMismatch(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	databagWantStderr(t, c.fail("databag", "item", "create", bag, "foo", "--file", writeJSON(t, map[string]any{"id": "bar", "v": "1"})), `"bar"`, `"foo"`)
	wantSlice(t, "items after a refused create", databagItemIDs(c, bag), []string{})

	databagItemCreate(c, bag, map[string]any{"id": "foo", "v": "1"})
	databagWantStderr(t, c.fail("databag", "item", "edit", bag, "foo", "--file", writeJSON(t, map[string]any{"id": "baz", "v": "2"})), `"baz"`, `"foo"`)
	wantSlice(t, "items after a refused edit", databagItemIDs(c, bag), []string{"foo"})
	if got := databagItemShow(c, bag, "foo"); !reflect.DeepEqual(got, map[string]any{"id": "foo", "v": "1"}) {
		t.Errorf("item after a refused edit = %v, want it unchanged", got)
	}
}

// testDatabagSpecialNames uses every character class Chef allows in data bag
// and item names (letters of both cases, digits, '-', '_' and, for items,
// '.'), so the CLI's paths and the server's routing agree on them.
func testDatabagSpecialNames(t *testing.T, _ Target, c *cli) {
	bag := "t-Bag_" + randomHex(t, 4)
	databagCreate(c, bag)
	ids := []string{"Db.Primary-01_x", "a.b.c", "UPPER", "1234", "with--dashes__and__underscores"}
	for _, id := range ids {
		databagItemCreate(c, bag, map[string]any{"id": id, "v": id})
	}
	want := slices.Clone(ids)
	slices.Sort(want)
	wantSlice(t, "items", databagItemIDs(c, bag), want)
	for _, id := range ids {
		if got := databagItemShow(c, bag, id); got["id"] != id || got["v"] != id {
			t.Errorf("item %q = %v", id, got)
		}
		c.run("databag", "item", "delete", bag, id)
		wantNotFound(t, c.fail("databag", "item", "show", bag, id))
	}
	wantSlice(t, "items after delete", databagItemIDs(c, bag), []string{})
}

// testDatabagInvalidNames checks that names Chef forbids are refused (erchef
// answers 400) and create nothing.
func testDatabagInvalidNames(t *testing.T, _ Target, c *cli) {
	badBag := "t bad " + randomHex(t, 4)
	c.cleanup("databag", "delete", badBag)
	databagWantStderr(t, c.fail("databag", "create", badBag), "400")
	if slices.Contains(databagNames(c), badBag) {
		t.Errorf("databag create %q created the bag", badBag)
	}

	bag := databagNew(c)
	for _, id := range []string{"bad id", "bad/id", "bad!id"} {
		databagWantStderr(t, c.fail("databag", "item", "create", bag, id, "--file", writeJSON(t, map[string]any{"id": id})), "400")
	}
	wantSlice(t, "items after invalid creates", databagItemIDs(c, bag), []string{})

	// Reading names that need percent-escaping puts the escapes in the
	// request path. The signature must still verify (a 401 here means the
	// server and client disagree on the signed path), so the answer is a
	// plain not-found.
	for _, args := range [][]string{
		{"databag", "show", badBag},
		{"databag", "item", "show", bag, "bad id"},
		{"databag", "item", "show", bag, "bad!id"},
		{"databag", "item", "delete", bag, "bad id"},
	} {
		r := c.fail(args...)
		if hasStatus(r.stderr, 401) {
			t.Errorf("an escaped path failed signature verification: %s", r)
			continue
		}
		wantNotFound(t, r)
	}
}

func testDatabagAlreadyExists(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	databagWantConflict(t, c.fail("databag", "create", bag))

	databagItemCreate(c, bag, map[string]any{"id": "dup", "v": "first"})
	databagWantConflict(t, c.fail("databag", "item", "create", bag, "dup", "--file", writeJSON(t, map[string]any{"id": "dup", "v": "second"})))
	// The two-argument create tolerates an existing bag, but not an
	// existing item.
	databagWantConflict(t, c.fail("databag", "create", bag, "dup", "--file", writeJSON(t, map[string]any{"id": "dup", "v": "third"})))
	secret := databagSecretFile(t, "open-sesame")
	databagWantConflict(t, c.fail("databag", "secret", "create", bag, "dup",
		"--file", writeJSON(t, map[string]any{"id": "dup", "v": "fourth"}), "--secret-file", secret))
	if got := databagItemShow(c, bag, "dup"); got["v"] != "first" {
		t.Errorf("a refused create changed the item: %v", got)
	}
}

// testDatabagBagNotFound runs every command that names a bag against one
// that does not exist.
func testDatabagBagNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("databag", "delete", ghost)
	file := writeJSON(t, map[string]any{"id": "x", "v": "1"})
	secret := databagSecretFile(t, "open-sesame")
	for _, args := range [][]string{
		{"databag", "show", ghost},
		{"databag", "delete", ghost},
		{"databag", "item", "list", ghost},
		{"databag", "item", "show", ghost, "x"},
		{"databag", "item", "create", ghost, "x", "--file", file},
		{"databag", "item", "edit", ghost, "x", "--file", file},
		{"databag", "item", "delete", ghost, "x"},
		{"databag", "secret", "create", ghost, "x", "--file", file, "--secret-file", secret},
		{"databag", "secret", "show", ghost, "x", "--secret-file", secret},
		{"databag", "secret", "edit", ghost, "x", "--file", file, "--secret-file", secret},
	} {
		r := c.fail(args...)
		wantNotFound(t, r)
		if !strings.Contains(r.stderr, ghost) {
			t.Errorf("the not-found error should name the missing bag %s: %s", ghost, r)
		}
	}
	if slices.Contains(databagNames(c), ghost) {
		t.Errorf("a command against a missing bag created it")
	}
}

// testDatabagItemNotFound runs every command that names an item against one
// that does not exist in a bag that does.
func testDatabagItemNotFound(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	ghost := uniqueName(t, "ghost")
	secret := databagSecretFile(t, "open-sesame")
	for _, args := range [][]string{
		{"databag", "item", "show", bag, ghost},
		{"databag", "item", "delete", bag, ghost},
		{"databag", "secret", "show", bag, ghost, "--secret-file", secret},
		// Without --file, secret edit reads the item first.
		{"databag", "secret", "edit", bag, ghost, "--secret-file", secret},
	} {
		r := c.fail(args...)
		wantNotFound(t, r)
		if !strings.Contains(r.stderr, ghost) {
			t.Errorf("the not-found error should name the missing item %s: %s", ghost, r)
		}
	}
}

// testDatabagEditMissing checks that editing an item that does not exist
// fails and does not create it, for plain and encrypted items alike.
func testDatabagEditMissing(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	ghost := uniqueName(t, "ghost")
	file := writeJSON(t, map[string]any{"id": ghost, "v": "1"})
	wantNotFound(t, c.fail("databag", "item", "edit", bag, ghost, "--file", file))
	secret := databagSecretFile(t, "open-sesame")
	wantNotFound(t, c.fail("databag", "secret", "edit", bag, ghost, "--file", file, "--secret-file", secret))
	wantSlice(t, "items", databagItemIDs(c, bag), []string{})
}

// testDatabagDeleteWithItems checks that deleting a bag deletes its items:
// a bag recreated under the same name starts empty.
func testDatabagDeleteWithItems(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	databagItemCreate(c, bag, map[string]any{"id": "one"})
	databagItemCreate(c, bag, map[string]any{"id": "two"})
	c.run("databag", "delete", bag)
	wantNotFound(t, c.fail("databag", "item", "list", bag))
	wantNotFound(t, c.fail("databag", "item", "show", bag, "one"))

	c.run("databag", "create", bag)
	wantSlice(t, "items in the recreated bag", databagItemIDs(c, bag), []string{})
	wantNotFound(t, c.fail("databag", "item", "show", bag, "one"))
}

// testDatabagForbidden acts as a plain API client, which the org's default
// ACLs let read data bags but not create, change or delete them.
func testDatabagForbidden(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	databagItemCreate(c, bag, map[string]any{"id": "cfg", "v": "admin"})
	actor := databagClientActor(c)
	as := func(args ...string) []string { return append(args, "--profile", actor) }

	var names []string
	c.json(&names, as("databag", "list")...)
	if !slices.Contains(names, bag) {
		t.Errorf("a client should be able to list data bags; %s missing from %v", bag, names)
	}
	var ids []string
	c.json(&ids, as("databag", "show", bag)...)
	wantSlice(t, "databag show as a client", ids, []string{"cfg"})
	var item map[string]any
	c.json(&item, as("databag", "item", "show", bag, "cfg")...)
	wantEqual(t, "item read as a client", item["v"], any("admin"))

	newBag := uniqueName(t, "bag")
	c.cleanup("databag", "delete", newBag)
	file := writeJSON(t, map[string]any{"id": "cfg", "v": "client"})
	secret := databagSecretFile(t, "open-sesame")
	for _, args := range [][]string{
		{"databag", "create", newBag},
		{"databag", "delete", bag},
		{"databag", "item", "create", bag, "new", "--file", file},
		{"databag", "item", "edit", bag, "cfg", "--file", file},
		{"databag", "item", "delete", bag, "cfg"},
		{"databag", "secret", "create", bag, "sec", "--file", file, "--secret-file", secret},
	} {
		databagWantForbidden(t, c.fail(as(args...)...))
	}

	if slices.Contains(databagNames(c), newBag) {
		t.Errorf("a refused create made bag %s", newBag)
	}
	wantSlice(t, "items after refused changes", databagItemIDs(c, bag), []string{"cfg"})
	wantEqual(t, "item after refused changes", databagItemShow(c, bag, "cfg")["v"], any("admin"))
}

// databagSecretPlain is an item whose values cover every JSON type, since
// each is boxed as {"json_wrapper": value} before encryption.
func databagSecretPlain(id string) map[string]any {
	return map[string]any{
		"id":       id,
		"password": "hunter2",
		"port":     float64(5432),
		"enabled":  true,
		"nothing":  nil,
		"hosts":    []any{"db1", "db2"},
		"replicas": map[string]any{"primary": "db1", "standby": []any{"db2", "db3"}},
	}
}

func testDatabagSecretRoundTrip(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	secret := databagSecretFile(t, "open-sesame")
	plain := databagSecretPlain("db-password")

	out := c.run("databag", "secret", "create", bag, "db-password", "--file", writeJSON(t, plain), "--secret-file", secret)
	wantEqual(t, "secret create output", out, fmt.Sprintf("Created encrypted item %q in data bag %q\n", "db-password", bag))

	if got := databagSecretShow(c, nil, bag, "db-password", "--secret-file", secret); !reflect.DeepEqual(got, plain) {
		t.Errorf("secret show = %v\nwant exactly %v", got, plain)
	}
	human := c.run("databag", "secret", "show", bag, "db-password", "--secret-file", secret)
	if !strings.Contains(human, "hunter2") || !strings.Contains(human, "db-password") {
		t.Errorf("secret show (human) should print the decrypted item:\n%s", human)
	}

	// Encrypted items list like any other item.
	wantSlice(t, "item list", databagItemIDs(c, bag), []string{"db-password"})

	// Without the secret, item show prints what is at rest: the id in
	// cleartext and every other value an encrypted wrapper.
	// Short plaintexts are searched for with their JSON quotes, which base64
	// ciphertext can never contain, so a chance match cannot flake.
	atRest := c.run("databag", "item", "show", bag, "db-password", "--format", "json")
	for _, leak := range []string{"hunter2", `"db1"`, `"db2"`} {
		if strings.Contains(atRest, leak) {
			t.Errorf("plaintext %q leaked into the stored item:\n%s", leak, atRest)
		}
	}
	stored := databagItemShow(c, bag, "db-password")
	wantEqual(t, "stored id", stored["id"], any("db-password"))
	for k := range plain {
		if k == "id" {
			continue
		}
		w, ok := stored[k].(map[string]any)
		if !ok || w["encrypted_data"] == nil {
			t.Errorf("stored %q is not an encrypted wrapper: %v", k, stored[k])
		}
	}
	if len(stored) != len(plain) {
		t.Errorf("stored item has keys %v, want exactly the plaintext item's", stored)
	}
}

// testDatabagSecretChefCanDecrypt checks that what the CLI writes is Chef's
// version 3 format, by decrypting every stored value with an implementation
// of Chef's Version3Decryptor that shares no code with the CLI.
func testDatabagSecretChefCanDecrypt(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	const secret = "a-shared-secret-knife-also-has"
	plain := databagSecretPlain("interop")
	c.run("databag", "secret", "create", bag, "interop", "--file", writeJSON(t, plain),
		"--secret-file", databagSecretFile(t, secret))

	stored := databagItemShow(c, bag, "interop")
	for k, want := range plain {
		if k == "id" {
			wantEqual(t, "stored id", stored["id"], any("interop"))
			continue
		}
		got, err := databagChefDecryptV3(stored[k], secret)
		if err != nil {
			t.Errorf("Chef cannot decrypt %q: %v", k, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q decrypts to %v, want %v", k, got, want)
		}
	}
}

// testDatabagSecretReadsChefFormats stores values Chef itself encrypted in
// each of its three formats, reads them back with `databag secret show`, then
// rewrites the item with `databag secret edit`, which writes version 3.
func testDatabagSecretReadsChefFormats(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	secret := databagSecretFile(t, databagChefSecret)
	for _, version := range []string{"v1", "v2", "v3"} {
		id := "chef-" + version
		databagItemCreate(c, bag, map[string]any{"id": id, "greeting": databagChefFixture(t, version)})
		got := databagSecretShow(c, nil, bag, id, "--secret-file", secret)
		if !reflect.DeepEqual(got, map[string]any{"id": id, "greeting": "hello world"}) {
			t.Errorf("%s item decrypts to %v, want greeting \"hello world\"", version, got)
		}
	}
	// One item mixing all three formats, as a bag edited by several Chef
	// releases over the years can hold.
	databagItemCreate(c, bag, map[string]any{
		"id": "mixed",
		"a":  databagChefFixture(t, "v1"),
		"b":  databagChefFixture(t, "v2"),
		"c":  databagChefFixture(t, "v3"),
	})
	got := databagSecretShow(c, nil, bag, "mixed", "--secret-file", secret)
	if !reflect.DeepEqual(got, map[string]any{"id": "mixed", "a": "hello world", "b": "hello world", "c": "hello world"}) {
		t.Errorf("mixed item decrypts to %v", got)
	}

	c.run("databag", "secret", "edit", bag, "chef-v1", "--file", writeJSON(t, map[string]any{"id": "chef-v1", "greeting": "hello again"}),
		"--secret-file", secret)
	stored := databagItemShow(c, bag, "chef-v1")
	if w, _ := stored["greeting"].(map[string]any); w["version"] != float64(3) {
		t.Errorf("secret edit should rewrite the value as version 3: %v", stored["greeting"])
	}
	got = databagSecretShow(c, nil, bag, "chef-v1", "--secret-file", secret)
	wantEqual(t, "greeting after edit", got["greeting"], any("hello again"))
}

func testDatabagSecretEdit(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	secret := databagSecretFile(t, "open-sesame")
	c.run("databag", "secret", "create", bag, "api-key",
		"--file", writeJSON(t, map[string]any{"id": "api-key", "token": "v1-original"}), "--secret-file", secret)

	edited := map[string]any{"id": "api-key", "token": "v2-rotated", "expires": float64(2030)}
	out := c.run("databag", "secret", "edit", bag, "api-key", "--file", writeJSON(t, edited), "--secret-file", secret)
	wantEqual(t, "secret edit output", out, fmt.Sprintf("Updated encrypted item %q in bag %q\n", "api-key", bag))

	if got := databagSecretShow(c, nil, bag, "api-key", "--secret-file", secret); !reflect.DeepEqual(got, edited) {
		t.Errorf("after edit, secret show = %v, want %v", got, edited)
	}
	atRest := c.run("databag", "item", "show", bag, "api-key", "--format", "json")
	for _, leak := range []string{"v2-rotated", "v1-original"} {
		if strings.Contains(atRest, leak) {
			t.Errorf("plaintext %q leaked into the stored item:\n%s", leak, atRest)
		}
	}

	// A file naming another item is refused, and the item is left alone.
	databagWantStderr(t, c.fail("databag", "secret", "edit", bag, "api-key",
		"--file", writeJSON(t, map[string]any{"id": "other", "token": "v3"}), "--secret-file", secret), `"other"`, `"api-key"`)
	wantSlice(t, "items", databagItemIDs(c, bag), []string{"api-key"})
	wantEqual(t, "token", databagSecretShow(c, nil, bag, "api-key", "--secret-file", secret)["token"], any("v2-rotated"))
}

// testDatabagSecretWrongSecret checks that the wrong secret gives a clear
// message, from show and from the editor path of edit (which decrypts before
// it would open the editor), and leaves the item untouched.
func testDatabagSecretWrongSecret(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	right := databagSecretFile(t, "correct-horse")
	wrong := databagSecretFile(t, "battery-staple")
	c.run("databag", "secret", "create", bag, "db", "--file", writeJSON(t, map[string]any{"id": "db", "password": "hunter2"}),
		"--secret-file", right)

	for _, args := range [][]string{
		{"databag", "secret", "show", bag, "db", "--secret-file", wrong},
		{"databag", "secret", "edit", bag, "db", "--secret-file", wrong},
		{"databag", "secret", "show", bag, "db", "--secret", "battery-staple"},
	} {
		r := c.fail(args...)
		databagWantStderr(t, r, "couldn't decrypt", "wrong secret")
		if strings.Contains(r.stdout+r.stderr, "hunter2") {
			t.Errorf("a failed decrypt printed the plaintext: %s", r)
		}
	}
	wantEqual(t, "password", databagSecretShow(c, nil, bag, "db", "--secret-file", right)["password"], any("hunter2"))
}

// testDatabagSecretNotEncrypted checks that the secret commands point at the
// plain item commands when the item is not encrypted.
func testDatabagSecretNotEncrypted(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	databagItemCreate(c, bag, map[string]any{"id": "plain", "note": "cleartext"})
	secret := databagSecretFile(t, "open-sesame")
	for _, args := range [][]string{
		{"databag", "secret", "show", bag, "plain", "--secret-file", secret},
		{"databag", "secret", "edit", bag, "plain", "--secret-file", secret},
	} {
		databagWantStderr(t, c.fail(args...), "isn't encrypted", "cinc databag item show")
	}
	if got := databagItemShow(c, bag, "plain"); !reflect.DeepEqual(got, map[string]any{"id": "plain", "note": "cleartext"}) {
		t.Errorf("item changed: %v", got)
	}
}

// testDatabagSecretSources checks every place the secret can come from and
// their precedence: --secret, --secret-file, $CINC_SECRET_FILE,
// $CHEF_SECRET_FILE (the cinc variable wins when both are set), then the
// profile's secret_file key.
func testDatabagSecretSources(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	const secret = "the-real-secret"
	right := databagSecretFile(t, secret)
	wrong := databagSecretFile(t, "not-the-secret")
	c.run("databag", "secret", "create", bag, "item", "--file", writeJSON(t, map[string]any{"id": "item", "v": "ok"}),
		"--secret", secret)
	databagAddSecretProfile(c, "withsecret", right)
	databagAddSecretProfile(c, "wrongsecret", wrong)

	for _, tc := range []struct {
		what  string
		env   []string
		flags []string
	}{
		{"--secret", nil, []string{"--secret", secret}},
		{"--secret-file", nil, []string{"--secret-file", right}},
		{"$CINC_SECRET_FILE", []string{"CINC_SECRET_FILE=" + right}, nil},
		{"$CHEF_SECRET_FILE", []string{"CHEF_SECRET_FILE=" + right}, nil},
		{"$CINC_SECRET_FILE over $CHEF_SECRET_FILE", []string{"CINC_SECRET_FILE=" + right, "CHEF_SECRET_FILE=" + wrong}, nil},
		{"profile secret_file", nil, []string{"--profile", "withsecret"}},
		{"--secret-file over $CINC_SECRET_FILE", []string{"CINC_SECRET_FILE=" + wrong}, []string{"--secret-file", right}},
		{"$CINC_SECRET_FILE over profile secret_file", []string{"CINC_SECRET_FILE=" + right}, []string{"--profile", "wrongsecret"}},
		{"--secret over profile secret_file", nil, []string{"--secret", secret, "--profile", "wrongsecret"}},
	} {
		args := append([]string{"databag", "secret", "show", bag, "item", "--format", "json"}, tc.flags...)
		r := c.exec(runOpts{env: tc.env}, args...)
		var got map[string]any
		if r.exitCode != 0 || json.Unmarshal([]byte(r.stdout), &got) != nil || got["v"] != "ok" {
			t.Errorf("secret from %s: %s", tc.what, r)
		}
	}

	// The secret written through the profile decrypts with the flag.
	c.run("databag", "secret", "create", bag, "via-profile", "--file", writeJSON(t, map[string]any{"id": "via-profile", "v": "p"}),
		"--profile", "withsecret")
	wantEqual(t, "via-profile", databagSecretShow(c, nil, bag, "via-profile", "--secret", secret)["v"], any("p"))

	r := c.exec(runOpts{env: []string{"CHEF_SECRET_FILE=" + right}}, "databag", "secret", "show", bag, "item",
		"--secret", secret, "--secret-file", right)
	databagWantStderr(t, r, "--secret", "--secret-file")

	// With no secret anywhere, the CLI says where to put one and sends
	// nothing to the server.
	r = c.fail("databag", "secret", "create", bag, "nosecret", "--file", writeJSON(t, map[string]any{"id": "nosecret", "v": "x"}))
	databagWantStderr(t, r, "secret", "--secret-file", "CINC_SECRET_FILE")
	if slices.Contains(databagItemIDs(c, bag), "nosecret") {
		t.Errorf("secret create without a secret created the item")
	}
	databagWantStderr(t, c.fail("databag", "secret", "show", bag, "item"), "--secret-file")

	r = c.fail("databag", "secret", "show", bag, "item", "--secret-file", "/nonexistent/secret")
	databagWantStderr(t, r, "/nonexistent/secret")
}

// testDatabagSecretMissing covers the server errors a secret command can
// hit: an item already there, and a bag that is not.
func testDatabagSecretMissing(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	secret := databagSecretFile(t, "open-sesame")
	file := writeJSON(t, map[string]any{"id": "s", "v": "1"})
	c.run("databag", "secret", "create", bag, "s", "--file", file, "--secret-file", secret)
	databagWantConflict(t, c.fail("databag", "secret", "create", bag, "s", "--file", file, "--secret-file", secret))
	wantNotFound(t, c.fail("databag", "secret", "show", bag, "nope", "--secret-file", secret))
	wantNotFound(t, c.fail("databag", "secret", "edit", bag, "nope", "--secret-file", secret))
}

// testDatabagSecretFileWhitespace checks that a secret file is read the way
// Chef reads it: Chef::EncryptedDataBagItem.load_secret strips leading and
// trailing whitespace, so a file ending in a newline (what `echo` or most
// editors write) holds the same secret as one without. Items knife wrote
// with such a file must decrypt with it, and items the CLI writes with it
// must decrypt with the stripped secret.
func testDatabagSecretFileWhitespace(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	withNewline := databagSecretFile(t, databagChefSecret+"\n")
	databagItemCreate(c, bag, map[string]any{"id": "from-chef", "greeting": databagChefFixture(t, "v3")})
	got := databagSecretShow(c, nil, bag, "from-chef", "--secret-file", withNewline)
	wantEqual(t, "Chef item read with a newline-terminated secret file", got["greeting"], any("hello world"))

	c.run("databag", "secret", "create", bag, "from-cinc", "--file", writeJSON(t, map[string]any{"id": "from-cinc", "v": "x"}),
		"--secret-file", withNewline)
	stored := databagItemShow(c, bag, "from-cinc")
	if v, err := databagChefDecryptV3(stored["v"], databagChefSecret); err != nil || v != "x" {
		t.Errorf("an item written with a newline-terminated secret file does not decrypt with the stripped secret, as Chef reads it: %v, %v", v, err)
	}
}

// testDatabagSecretFileEmpty checks that an empty (or all-whitespace) secret
// file is refused, as Chef refuses a zero-length secret, instead of
// encrypting with an empty key.
func testDatabagSecretFileEmpty(t *testing.T, _ Target, c *cli) {
	bag := databagNew(c)
	for _, content := range []string{"", "\n"} {
		empty := databagSecretFile(t, content)
		r := c.fail("databag", "secret", "create", bag, "e", "--file", writeJSON(t, map[string]any{"id": "e", "v": "x"}),
			"--secret-file", empty)
		databagWantStderr(t, r, "empty")
		c.fail("databag", "secret", "show", bag, "e", "--secret-file", empty)
	}
	wantSlice(t, "items", databagItemIDs(c, bag), []string{})
}
