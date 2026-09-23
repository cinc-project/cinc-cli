package suite

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

var groupFamily = family{cases: []testCase{
	{"groups/lifecycle", []string{"group create", "group show", "group list", "group edit", "group delete"}, testGroupLifecycle},
	{"groups/default-groups", []string{"group list", "group show"}, testGroupDefaultGroups},
	{"groups/members", []string{"group member add", "group member remove", "group show"}, testGroupMembers},
	{"groups/member-no-op", []string{"group member add", "group member remove"}, testGroupMemberNoOp},
	{"groups/member-bad-type", []string{"group member add", "group member remove"}, testGroupMemberBadType},
	{"groups/edit-replaces", []string{"group edit"}, testGroupEditReplaces},
	{"groups/edit-no-editor", []string{"group edit"}, testGroupEditNoEditor},
	{"groups/not-found", []string{"group show", "group delete", "group member add", "group member remove"}, testGroupNotFound},
	{"groups/edit-missing", []string{"group edit"}, testGroupEditMissing},
	{"groups/already-exists", []string{"group create"}, testGroupAlreadyExists},
	{"groups/invalid-name", []string{"group create"}, testGroupInvalidName},
	{"groups/forbidden", []string{"group create", "group delete", "group member add", "group edit"}, testGroupForbidden},
}}

func showGroup(c *cli, name string) cinc.Group {
	c.t.Helper()
	var g cinc.Group
	c.json(&g, "group", "show", name)
	return g
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

func testGroupLifecycle(t *testing.T, tgt Target, c *cli) {
	name := uniqueName(t, "group")
	out := c.run("group", "create", name)
	c.cleanup("group", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created group %q\n", name))

	g := showGroup(c, name)
	wantEqual(t, "groupname", g.GroupName, name)
	wantEqual(t, "orgname", g.OrgName, tgt.Org)
	if len(g.Users)+len(g.Clients)+len(g.Groups) != 0 {
		t.Errorf("a new group has members: %+v", g)
	}
	if human := c.run("group", "show", name); !strings.Contains(human, name) {
		t.Errorf("group show does not name the group:\n%s", human)
	}

	if list := strings.Fields(c.run("group", "list")); !slices.Contains(list, name) {
		t.Errorf("group list does not include %s: %v", name, list)
	}
	var names []string
	c.json(&names, "group", "list")
	if !slices.Contains(names, name) {
		t.Errorf("group list --format json does not include %s: %v", name, names)
	}

	client := newACLClient(c)
	inner := createACLGroup(c)
	// The name in the file is ignored: the argument names the group.
	file := writeJSON(t, cinc.Group{GroupName: "ignored", Users: []string{tgt.Admin}, Clients: []string{client.name}, Groups: []string{inner}})
	wantEqual(t, "edit output", c.run("group", "edit", name, "--file", file), fmt.Sprintf("Updated group %q\n", name))
	g = showGroup(c, name)
	wantSlice(t, "users after edit", g.Users, []string{tgt.Admin})
	wantSlice(t, "clients after edit", g.Clients, []string{client.name})
	wantSlice(t, "groups after edit", g.Groups, []string{inner})
	wantNotFound(t, c.fail("group", "show", "ignored"))

	wantEqual(t, "delete output", c.run("group", "delete", name), fmt.Sprintf("Deleted group %q\n", name))
	wantNotFound(t, c.fail("group", "show", name))
	if list := strings.Fields(c.run("group", "list")); slices.Contains(list, name) {
		t.Errorf("group list still includes deleted %s", name)
	}
	// Deleting a group that another group nests leaves the outer one intact.
	c.run("group", "delete", inner)
	if list := strings.Fields(c.run("group", "list")); slices.Contains(list, inner) {
		t.Errorf("group list still includes deleted %s", inner)
	}
}

// testGroupDefaultGroups checks the groups every organization is created
// with.
func testGroupDefaultGroups(t *testing.T, _ Target, c *cli) {
	var names []string
	c.json(&names, "group", "list")
	for _, want := range []string{"admins", "clients", "users"} {
		if !slices.Contains(names, want) {
			t.Errorf("group list = %v, want the default group %s", names, want)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("group list is not sorted: %v", names)
	}
	human := c.run("group", "list")
	for _, want := range []string{"admins", "clients", "users"} {
		if !slices.Contains(strings.Fields(human), want) {
			t.Errorf("group list (human) missing %s:\n%s", want, human)
		}
	}
	wantEqual(t, "admins groupname", showGroup(c, "admins").GroupName, "admins")
	// A client created in the org joins its clients group.
	client := newACLClient(c)
	if g := showGroup(c, "clients"); !slices.Contains(g.Clients, client.name) {
		t.Errorf("clients group = %v, want the new client %s", g.Clients, client.name)
	}
}

// testGroupMembers adds and removes each kind of member: users, clients and
// nested groups.
func testGroupMembers(t *testing.T, tgt Target, c *cli) {
	group := createACLGroup(c)
	a, b := newACLClient(c), newACLClient(c)
	nested := createACLGroup(c)

	out := c.run("group", "member", "add", group, a.name, b.name, "--type", "client")
	wantEqual(t, "add clients output", out, fmt.Sprintf("Added %s, %s to group %q\n", a.name, b.name, group))
	c.run("group", "member", "add", group, tgt.Admin)
	c.run("group", "member", "add", group, nested, "--type", "group")
	g := showGroup(c, group)
	wantSlice(t, "clients", sorted(g.Clients), sorted([]string{a.name, b.name}))
	wantSlice(t, "users", g.Users, []string{tgt.Admin})
	wantSlice(t, "groups", g.Groups, []string{nested})
	show := c.run("group", "show", group)
	for _, want := range []string{a.name, b.name, tgt.Admin, nested} {
		if !strings.Contains(show, want) {
			t.Errorf("group show missing %s:\n%s", want, show)
		}
	}

	out = c.run("group", "member", "remove", group, b.name, "--type", "client")
	wantEqual(t, "remove output", out, fmt.Sprintf("Removed %s from group %q\n", b.name, group))
	g = showGroup(c, group)
	wantSlice(t, "clients after remove", g.Clients, []string{a.name})
	wantSlice(t, "users after removing a client", g.Users, []string{tgt.Admin})
	wantSlice(t, "groups after removing a client", g.Groups, []string{nested})

	c.run("group", "member", "remove", group, nested, "--type", "group")
	c.run("group", "member", "remove", group, tgt.Admin, "--type", "user")
	g = showGroup(c, group)
	if len(g.Users) != 0 || len(g.Groups) != 0 {
		t.Errorf("after removing the user and the group: %+v", g)
	}
	wantSlice(t, "clients left", g.Clients, []string{a.name})
}

// testGroupMemberNoOp adds a member that is already there and removes one
// that is not. Neither changes the group, and the output says so rather than
// claiming a change.
func testGroupMemberNoOp(t *testing.T, _ Target, c *cli) {
	group := createACLGroup(c)
	a := newACLClient(c)
	c.run("group", "member", "add", group, a.name, "--type", "client")

	out := c.run("group", "member", "add", group, a.name, "--type", "client")
	if !strings.Contains(out, "No change") || !strings.Contains(out, a.name) {
		t.Errorf("adding an existing member = %q, want a friendly no-op", out)
	}
	ghost := uniqueName(t, "ghost")
	out = c.run("group", "member", "remove", group, ghost, "--type", "client")
	if !strings.Contains(out, "No change") || !strings.Contains(out, ghost) {
		t.Errorf("removing a non-member = %q, want a friendly no-op", out)
	}
	wantSlice(t, "clients", showGroup(c, group).Clients, []string{a.name})
}

// testGroupMemberBadType checks that an unknown --type is rejected before
// anything is sent: against a group that does not exist, the error is about
// the flag, not a not-found.
func testGroupMemberBadType(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	for _, verb := range []string{"add", "remove"} {
		r := c.fail("group", "member", verb, ghost, "someone", "--type", "robot")
		if !strings.Contains(r.stderr, `"robot"`) || !strings.Contains(r.stderr, "user, client, or group") {
			t.Errorf("group member %s --type robot should name the valid types: %s", verb, r)
		}
		if isNotFound(r) {
			t.Errorf("group member %s validated --type only after asking the server: %s", verb, r)
		}
	}
}

// testGroupEditReplaces checks that edit --file replaces the membership
// wholesale: kinds the file leaves out are emptied.
func testGroupEditReplaces(t *testing.T, tgt Target, c *cli) {
	group := createACLGroup(c)
	a := newACLClient(c)
	c.run("group", "member", "add", group, a.name, "--type", "client")
	c.run("group", "member", "add", group, tgt.Admin)

	file := writeJSON(t, map[string]any{"users": []string{tgt.Admin}})
	c.run("group", "edit", group, "--file", file)
	g := showGroup(c, group)
	wantSlice(t, "users", g.Users, []string{tgt.Admin})
	if len(g.Clients) != 0 {
		t.Errorf("clients after an edit that lists none = %v, want none", g.Clients)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	writeFile(t, bad, "{not json")
	r := c.fail("group", "edit", group, "--file", bad)
	if !strings.Contains(r.stderr, bad) {
		t.Errorf("a malformed --file should name the file: %s", r)
	}
	wantSlice(t, "users after a failed edit", showGroup(c, group).Users, []string{tgt.Admin})
}

// testGroupEditNoEditor runs edit without --file and without a terminal, as
// a script would: it must fail promptly, point at --file, and leave the
// group alone.
func testGroupEditNoEditor(t *testing.T, _ Target, c *cli) {
	group := createACLGroup(c)
	c.run("group", "member", "add", group, c.tgt.Admin)
	r := execWithoutTerminal(c, "group", "edit", group)
	if r.exitCode == 0 || !strings.Contains(r.stderr, "--file") {
		t.Errorf("edit without a terminal should fail and suggest --file: %s", r)
	}
	wantSlice(t, "users", showGroup(c, group).Users, []string{c.tgt.Admin})
}

func testGroupNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("group", "show", ghost))
	wantNotFound(t, c.fail("group", "delete", ghost))
	wantNotFound(t, c.fail("group", "member", "add", ghost, "someone"))
	wantNotFound(t, c.fail("group", "member", "remove", ghost, "someone"))
	if list := strings.Fields(c.run("group", "list")); slices.Contains(list, ghost) {
		t.Errorf("a failed member change created %s", ghost)
	}
}

// testGroupEditMissing checks that editing a group that does not exist
// fails and does not create it.
func testGroupEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("group", "delete", ghost)
	file := writeJSON(t, cinc.Group{GroupName: ghost})
	wantNotFound(t, c.fail("group", "edit", ghost, "--file", file))
	wantNotFound(t, c.fail("group", "show", ghost))
}

func testGroupAlreadyExists(t *testing.T, _ Target, c *cli) {
	group := createACLGroup(c)
	r := c.fail("group", "create", group)
	if !strings.Contains(strings.ToLower(r.stderr), "already exists") {
		t.Errorf("creating an existing group should say it already exists: %s", r)
	}
	// The built-in groups are no different.
	r = c.fail("group", "create", "admins")
	if !strings.Contains(strings.ToLower(r.stderr), "already exists") {
		t.Errorf("creating admins should say it already exists: %s", r)
	}
}

// testGroupInvalidName checks that the server refuses a group name outside
// Chef's name characters (letters, digits, "_", "-", "."), which erchef
// answers with a 400.
func testGroupInvalidName(t *testing.T, _ Target, c *cli) {
	name := "t group " + randomHex(t, 4)
	c.cleanup("group", "delete", name)
	c.fail("group", "create", name)
	var names []string
	c.json(&names, "group", "list")
	if slices.Contains(names, name) {
		t.Errorf("the refused group %q was created anyway", name)
	}
}

// testGroupForbidden checks that a plain client, which the default ACLs give
// no rights on groups beyond reading, cannot create, change or delete one.
func testGroupForbidden(t *testing.T, _ Target, c *cli) {
	group := createACLGroup(c)
	who := newACLClient(c)

	name := uniqueName(t, "group")
	c.cleanup("group", "delete", name)
	wantDenied(t, who.as(c, "group", "create", name))
	wantNotFound(t, c.fail("group", "show", name))

	wantDenied(t, who.as(c, "group", "member", "add", group, who.name, "--type", "client"))
	file := writeJSON(t, cinc.Group{Clients: []string{who.name}})
	wantDenied(t, who.as(c, "group", "edit", group, "--file", file))
	if g := showGroup(c, group); len(g.Clients) != 0 {
		t.Errorf("a refused change added members: %+v", g)
	}
	wantDenied(t, who.as(c, "group", "delete", group))
	wantEqual(t, "groupname after a refused delete", showGroup(c, group).GroupName, group)

	// A client cannot put itself into admins.
	wantDenied(t, who.as(c, "group", "member", "add", "admins", who.name, "--type", "client"))
	if g := showGroup(c, "admins"); slices.Contains(g.Clients, who.name) {
		t.Fatalf("a client added itself to admins: %+v", g)
	}
}
