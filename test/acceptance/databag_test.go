//go:build acceptance

package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestDataBagListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "databag", "list", "--config", env.cfgPath)
	if human != "apps\nusers\n" {
		t.Errorf("databag list (human) = %q", human)
	}

	jsonOut := runCinc(t, env.binary, "databag", "list", "--config", env.cfgPath, "--format", "json")
	for _, name := range []string{"apps", "users"} {
		if !strings.Contains(jsonOut, name) {
			t.Errorf("databag list (json) missing %q\ngot: %s", name, jsonOut)
		}
	}
}

// TestDataBagItemListAgainstCincZero asserts the seeded "users" bag
// returns its two seed items in sorted order, and that an empty bag
// returns an empty list.
func TestDataBagItemListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "databag", "item", "list", "users", "--config", env.cfgPath)
	if human != "alice\nbob\n" {
		t.Errorf("databag item list users (human) = %q", human)
	}

	empty := runCinc(t, env.binary, "databag", "item", "list", "apps", "--config", env.cfgPath)
	if empty != "" {
		t.Errorf("databag item list apps (human) = %q, want empty", empty)
	}

	jsonOut := strings.TrimSpace(runCinc(t, env.binary, "databag", "item", "list", "apps", "--config", env.cfgPath, "--format", "json"))
	if jsonOut != "[]" {
		t.Errorf("databag item list apps (json) = %q, want \"[]\"", jsonOut)
	}
}

// TestDataBagItemListMissingBagAgainstCincZero exercises the error
// path when the bag itself does not exist on the server.
func TestDataBagItemListMissingBagAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	_, stderr, err := runCincRaw(env.binary, "databag", "item", "list", "ghosts", "--config", env.cfgPath)
	if err == nil {
		t.Fatalf("databag item list of missing bag unexpectedly succeeded")
	}
	if !strings.Contains(stderr, "404") && !strings.Contains(stderr, "not found") {
		t.Errorf("databag item list stderr does not mention 404/not found: %s", stderr)
	}
}

// TestDataBagItemEditAgainstCincZero exercises `cinc databag item
// edit` through its `--file` path. The seed populates `users`
// with an "alice" item; the test PUTs a modified version and verifies
// the command exits cleanly.
func TestDataBagItemEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	itemPath := filepath.Join(t.TempDir(), "alice.json")
	body, err := json.Marshal(cinc.DataBagItem{"id": "alice", "role": "editor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(itemPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runCinc(t, env.binary, "databag", "item", "edit", "users", "alice", "--file", itemPath, "--config", env.cfgPath)
	if out != "Updated item \"alice\" in bag \"users\"\n" {
		t.Errorf("databag item edit output = %q", out)
	}
}

func TestDataBagCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "databag", "create", "secrets", "--config", env.cfgPath)
	if out != "Created data bag \"secrets\"\n" {
		t.Errorf("databag create output = %q", out)
	}

	listed := runCinc(t, env.binary, "databag", "list", "--config", env.cfgPath)
	if !strings.Contains(listed, "secrets") {
		t.Errorf("databag list missing new bag:\n%s", listed)
	}
}

// TestDataBagCreateWithItemAgainstCincZero exercises the two-arg
// `databag create BAG ITEM` form through --file so it doesn't need
// a real terminal for the editor.
func TestDataBagCreateWithItemAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	itemPath := filepath.Join(t.TempDir(), "item.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "db-password", "password": "hunter2"})
	if err := os.WriteFile(itemPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runCinc(t, env.binary, "databag", "create", "secrets", "db-password", "--file", itemPath, "--config", env.cfgPath)
	if !strings.Contains(out, "Created data bag \"secrets\"") {
		t.Errorf("expected bag-create line:\n%s", out)
	}
	if !strings.Contains(out, "Created item \"db-password\" in data bag \"secrets\"") {
		t.Errorf("expected item-create line:\n%s", out)
	}
}

func TestDataBagDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "databag", "delete", "apps", "--config", env.cfgPath)
	if out != "Deleted data bag \"apps\"\n" {
		t.Errorf("databag delete output = %q", out)
	}

	after := runCinc(t, env.binary, "databag", "list", "--config", env.cfgPath)
	if after != "users\n" {
		t.Errorf("databag list after delete = %q, want apps absent", after)
	}
}

// TestDataBagShowAgainstCincZero asserts that showing a bag enumerates
// its item IDs (the seeded "users" bag holds alice and bob).
func TestDataBagShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "databag", "show", "users", "--config", env.cfgPath)
	if human != "alice\nbob\n" {
		t.Errorf("databag show users (human) = %q", human)
	}

	jsonOut := strings.TrimSpace(runCinc(t, env.binary, "databag", "show", "apps", "--config", env.cfgPath, "--format", "json"))
	if jsonOut != "[]" {
		t.Errorf("databag show apps (json) = %q, want \"[]\"", jsonOut)
	}
}

// TestDataBagItemShowAgainstCincZero fetches a single seeded item and
// asserts its document is returned in both formats.
func TestDataBagItemShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "databag", "item", "show", "users", "alice", "--config", env.cfgPath)
	if !strings.Contains(human, "alice") || !strings.Contains(human, "admin") {
		t.Errorf("databag item show (human) missing id/role:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "databag", "item", "show", "users", "alice", "--config", env.cfgPath, "--format", "json")
	var got cinc.DataBagItem
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("databag item show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got["id"] != "alice" || got["role"] != "admin" {
		t.Errorf("databag item show item = %+v, want id=alice role=admin", got)
	}
}

// TestDataBagItemDeleteAgainstCincZero deletes a single item and
// asserts it disappears while the bag and its sibling item remain.
func TestDataBagItemDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "databag", "item", "delete", "users", "alice", "--config", env.cfgPath)
	if out != "Deleted item \"alice\" from data bag \"users\"\n" {
		t.Errorf("databag item delete output = %q", out)
	}

	after := runCinc(t, env.binary, "databag", "item", "list", "users", "--config", env.cfgPath)
	if after != "bob\n" {
		t.Errorf("databag item list after delete = %q, want only bob", after)
	}
}

// TestDataBagItemCreateAgainstCincZero creates an item in the seeded "users"
// bag through --file and confirms it appears in the item index.
func TestDataBagItemCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	itemPath := filepath.Join(t.TempDir(), "carol.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "carol", "role": "admin"})
	if err := os.WriteFile(itemPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runCinc(t, env.binary, "databag", "item", "create", "users", "carol", "--file", itemPath, "--config", env.cfgPath)
	if out != "Created item \"carol\" in data bag \"users\"\n" {
		t.Errorf("databag item create output = %q", out)
	}

	listed := runCinc(t, env.binary, "databag", "item", "list", "users", "--config", env.cfgPath)
	if listed != "alice\nbob\ncarol\n" {
		t.Errorf("databag item list after create = %q, want carol present", listed)
	}
}
