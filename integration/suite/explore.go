package suite

import (
	"fmt"
	"strings"
	"testing"
)

// The explore cases drive the real TUI on a pseudo-terminal: they type the
// keys a person would, wait for what the screen should show, and check the
// server through the CLI afterwards.

// startExplore launches explore for the default profile and waits for the
// object-type menu.
func startExplore(c *cli) *tui {
	c.t.Helper()
	u := c.startTUI("explore", "--profile", "default")
	u.waitFor("Object types")
	u.waitFor(selected("Nodes"))
	return u
}

// openRole moves from the object-type menu to the role list, filters it to
// name, and waits for that row to be selected.
func openRole(u *tui, name string) {
	u.t.Helper()
	u.send("j")
	u.waitFor(selected("Roles"))
	u.send("\r")
	u.send("/", name, "\r")
	u.waitFor(selected(name))
}

func testExploreHelp(t *testing.T, _ Target, c *cli) {
	if root := c.run("--help"); !strings.Contains(root, "explore") {
		t.Errorf("root help does not list explore:\n%s", root)
	}
	wantContains(t, "explore help", c.run("explore", "--help"), "terminal UI", "object type")
}

// testExploreRequiresTTY checks that explore refuses to start when its output
// is not a terminal, instead of drawing a TUI into a pipe.
func testExploreRequiresTTY(t *testing.T, _ Target, c *cli) {
	wantStderr(t, c.fail("explore", "--profile", "default"), "interactive terminal")
}

// testExploreBrowse opens a role from the server: its row, its summary and
// its JSON detail all come from the server's copy.
func testExploreBrowse(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	desc := "explored " + randomHex(t, 4)
	createRole(c, name, "--description", desc)

	u := startExplore(c)
	openRole(u, name)
	u.waitFor(desc) // the summary pane
	u.send("\r")
	u.waitFor(fmt.Sprintf("%q: %q", "description", desc)) // the JSON detail
	u.quit()
}

// testExploreSearch runs a server-side search from the object-type menu:
// the list narrows to exactly the one matching role.
func testExploreSearch(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	desc := "searched " + randomHex(t, 4)
	createRole(c, name, "--description", desc)
	c.awaitSearch(1, "role", "name:"+name)

	u := startExplore(c)
	u.send("j")
	u.waitFor(selected("Roles"))
	u.send("s")
	u.waitFor("Search Roles")
	u.send("name:"+name, "\r")
	u.waitFor("name:" + name + "  (1)")
	u.waitFor(selected(name))
	u.waitFor(desc)
	u.quit()
}

// testExploreDelete deletes a role through the TUI and checks the server no
// longer has it.
func testExploreDelete(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name)

	u := startExplore(c)
	openRole(u, name)
	u.send("d")
	u.waitFor("Delete " + name + "? (y/N)")
	u.send("y")
	u.waitFor("Deleted " + name)
	u.quit()
	wantNotFound(t, c.fail("role", "show", name))
}

// testExploreDataBagSearch searches inside a data bag. erchef returns data
// bag items from search wrapped, so explore must match them by the item's
// id, not the wrapper's name.
func testExploreDataBagSearch(t *testing.T, _ Target, c *cli) {
	bag := createSearchBag(c, map[string]map[string]any{
		"alice": {"shell": "zsh"},
		"bob":   {"shell": "bash"},
	})
	c.awaitSearch(1, bag, "id:bob")

	u := startExplore(c)
	u.send("j", "j", "j", "j", "j", "j")
	u.waitFor(selected("Data Bags"))
	u.send("\r")
	u.send("/", bag, "\r")
	u.waitFor(selected(bag))
	u.send("\r")
	u.waitFor("Data Bags › " + bag)
	u.waitFor(selected("alice"))
	u.send("s")
	u.waitFor("Search " + bag)
	u.send("id:bob", "\r")
	u.waitFor("id:bob  (1)")
	u.waitFor(selected("bob"))
	u.quit()
}
