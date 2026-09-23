package suite

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// The ACL family drives `<noun> acl show|grant|revoke` for every noun that
// has one. ACL enforcement is on for every target, so each case proves a
// grant is effective, not just stored: a fresh non-admin actor is refused,
// then allowed once granted, then refused again once revoked. The permission
// used for that is "grant", because reading or changing an object's ACL
// requires it, no default ACL gives it to anyone outside the admins group,
// and `<noun> acl show` is a request every noun can make with it.
var aclFamily = family{cases: []testCase{
	{"acls/client", aclCovers("client"), aclRoundTrip(aclNouns["client"])},
	{"acls/cookbook", aclCovers("cookbook"), aclRoundTrip(aclNouns["cookbook"])},
	{"acls/databag", aclCovers("databag"), aclRoundTrip(aclNouns["databag"])},
	{"acls/environment", aclCovers("environment"), aclRoundTrip(aclNouns["environment"])},
	{"acls/group", aclCovers("group"), aclRoundTrip(aclNouns["group"])},
	{"acls/node", aclCovers("node"), aclRoundTrip(aclNouns["node"])},
	{"acls/org", aclCovers("org"), aclRoundTrip(aclNouns["org"])},
	{"acls/policy", aclCovers("policy"), aclRoundTrip(aclNouns["policy"])},
	{"acls/policy-group", aclCovers("policy-group"), aclRoundTrip(aclNouns["policy-group"])},
	{"acls/role", aclCovers("role"), aclRoundTrip(aclNouns["role"])},
	{"acls/user", aclCovers("user"), aclRoundTrip(aclNouns["user"])},
	{"acls/show-shape", []string{"node acl show"}, testACLShowShape},
	{"acls/node-read", []string{"node acl revoke", "node acl grant"}, testACLNodeRead},
	{"acls/node-update", []string{"node acl grant", "node acl revoke"}, testACLNodeUpdate},
	{"acls/grant-all", []string{"role acl grant", "role acl revoke", "role acl show"}, testACLGrantAll},
	{"acls/multiple-members", []string{"environment acl grant", "environment acl revoke"}, testACLMultipleMembers},
	{"acls/no-op", []string{"node acl grant", "node acl revoke"}, testACLNoOp},
	{"acls/via-group", []string{"role acl grant", "group member add", "group member remove"}, testACLViaGroup},
	{"acls/via-nested-group", []string{"databag acl grant", "group member add", "group member remove"}, testACLViaNestedGroup},
	{"acls/bad-input", []string{"node acl grant", "node acl revoke", "org acl grant"}, testACLBadInput},
	{"acls/forbidden-message", []string{"node acl show", "node acl grant", "node acl revoke"}, testACLForbiddenMessage},
	{"acls/missing-object", []string{"node acl show", "node acl grant", "role acl show", "databag acl show"}, testACLMissingObject},
	{"acls/unknown-member", []string{"node acl grant"}, testACLUnknownMember},
}}

// aclCovers is the three acl leaves of one noun.
func aclCovers(noun string) []string {
	return []string{noun + " acl show", noun + " acl grant", noun + " acl revoke"}
}

// aclNoun describes one noun's ACL: how to make an object whose ACL a case
// can change, and which kind of actor to grant on it.
type aclNoun struct {
	noun string
	// create makes a fresh object and registers its cleanup, returning its
	// name, or "" for the organization's own (nameless) ACL.
	create func(c *cli) string
	// user grants to a user (with --user) rather than a client. User ACLs
	// are global, and a client belongs to one org, so a user's ACL names
	// users.
	user bool
}

var aclNouns = map[string]aclNoun{
	"client":   {noun: "client", create: func(c *cli) string { return newACLClient(c).name }},
	"cookbook": {noun: "cookbook", create: uploadACLCookbook},
	"databag": {noun: "databag", create: func(c *cli) string {
		name := uniqueName(c.t, "bag")
		c.run("databag", "create", name)
		c.cleanup("databag", "delete", name)
		return name
	}},
	"environment": {noun: "environment", create: func(c *cli) string {
		name := uniqueName(c.t, "env")
		createEnvironment(c, name)
		return name
	}},
	"group": {noun: "group", create: func(c *cli) string { return createACLGroup(c) }},
	"node": {noun: "node", create: func(c *cli) string {
		name := uniqueName(c.t, "node")
		createNode(c, name)
		return name
	}},
	"org":    {noun: "org", create: func(*cli) string { return "" }},
	"policy": {noun: "policy", create: func(c *cli) string { policy, _ := pushACLPolicy(c); return policy }},
	"policy-group": {noun: "policy-group", create: func(c *cli) string {
		_, group := pushACLPolicy(c)
		return group
	}},
	"role": {noun: "role", create: createACLRole},
	"user": {noun: "user", user: true, create: func(c *cli) string { return newACLUser(c).name }},
}

// aclActor is a non-admin user or client with its own credentials profile, so a
// case can act as it.
type aclActor struct {
	name string
	user bool
}

// flag is the acl grant/revoke member flag naming this actor.
func (a aclActor) flag() []string {
	if a.user {
		return []string{"--user", a.name}
	}
	return []string{"--client", a.name}
}

// as runs the binary signed as a, returning whatever happened.
func (a aclActor) as(c *cli, args ...string) result {
	c.t.Helper()
	return c.exec(runOpts{}, append(args, "--profile", a.name)...)
}

// newACLClient creates a client in the target org, with no rights beyond
// the org's "clients" group, and a profile that signs as it.
func newACLClient(c *cli) aclActor {
	c.t.Helper()
	name := uniqueName(c.t, "client")
	key := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("client", "create", name, "--key-file", key)
	c.cleanup("client", "delete", name)
	c.addProfile(name, c.tgt.Org, name, key)
	return aclActor{name: name}
}

// newACLUser creates a user that belongs to no organization, and a profile
// that signs as it against the target org's URL.
func newACLUser(c *cli) aclActor {
	c.t.Helper()
	name := uniqueName(c.t, "user")
	key := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("user", "create", name, "--email", name+"@example.test",
		"--first-name", "Test", "--last-name", "User", "--display-name", name,
		"--password", "pw-"+randomHex(c.t, 8), "--key-file", key)
	c.cleanup("user", "delete", name)
	c.addProfile(name, c.tgt.Org, name, key)
	return aclActor{name: name, user: true}
}

// createACLGroup creates an empty group and registers its cleanup.
func createACLGroup(c *cli) string {
	c.t.Helper()
	name := uniqueName(c.t, "group")
	c.run("group", "create", name)
	c.cleanup("group", "delete", name)
	return name
}

// createACLRole creates an empty role and registers its cleanup.
func createACLRole(c *cli) string {
	c.t.Helper()
	name := uniqueName(c.t, "role")
	c.run("role", "create", name)
	c.cleanup("role", "delete", name)
	return name
}

// uploadACLCookbook uploads a one-recipe cookbook and registers its cleanup.
func uploadACLCookbook(c *cli) string {
	c.t.Helper()
	name := "t_cb_" + randomHex(c.t, 4)
	dir := c.t.TempDir()
	writeFile(c.t, filepath.Join(dir, name, "metadata.rb"), fmt.Sprintf("name '%s'\nversion '0.1.0'\n", name))
	writeFile(c.t, filepath.Join(dir, name, "recipes", "default.rb"), "log 'hello'\n")
	c.run("cookbook", "upload", name, "--cookbook-path", dir)
	c.cleanup("cookbook", "delete", name, "0.1.0")
	return name
}

// pushACLPolicy pushes a one-cookbook policy lock to a fresh policy group,
// which creates both the policy and the group, and registers their cleanup.
// The cookbook artifact it uploads has a unique name and stays behind: no
// command deletes one artifact, and `policy clean-cookbooks` would race the
// other cases' pushes.
func pushACLPolicy(c *cli) (policy, group string) {
	c.t.Helper()
	policy, group = uniqueName(c.t, "policy"), uniqueName(c.t, "pg")
	cookbook := "t_pcb_" + randomHex(c.t, 4)
	identifier := randomHex(c.t, 20)
	dir := c.t.TempDir()
	writeFile(c.t, filepath.Join(dir, "cookbooks", cookbook, "metadata.rb"), fmt.Sprintf("name '%s'\nversion '1.0.0'\n", cookbook))
	writeFile(c.t, filepath.Join(dir, "cookbooks", cookbook, "recipes", "default.rb"), "log 'deployed'\n")
	lock, err := json.MarshalIndent(map[string]any{
		"name":        policy,
		"revision_id": randomHex(c.t, 32),
		"run_list":    []string{"recipe[" + cookbook + "::default]"},
		"cookbook_locks": map[string]any{
			cookbook: map[string]any{
				"version":                   "1.0.0",
				"identifier":                identifier,
				"dotted_decimal_identifier": "1.0.0",
				"source_options":            map[string]any{"path": "cookbooks/" + cookbook},
			},
		},
	}, "", "  ")
	if err != nil {
		c.t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "Policyfile.lock.json")
	writeFile(c.t, lockPath, string(lock))
	c.run("policy", "push", group, lockPath)
	c.cleanup("policy-group", "delete", group)
	c.cleanup("policy", "delete", policy)
	return policy, group
}

// aclArgs builds `<noun> acl <verb> [perm] [name]` for a named or nameless
// scope.
func aclArgs(noun, verb, perm, name string) []string {
	args := []string{noun, "acl", verb}
	if perm != "" {
		args = append(args, perm)
	}
	if name != "" {
		args = append(args, name)
	}
	return args
}

// showACL reads an ACL as the admin through --format json.
func showACL(c *cli, noun, name string) cinc.ACL {
	c.t.Helper()
	var acl cinc.ACL
	c.json(&acl, aclArgs(noun, "show", "", name)...)
	return acl
}

// aclTarget is how the CLI names the object in grant and revoke output.
func aclTarget(c *cli, noun, name string) string {
	if noun == "org" {
		return fmt.Sprintf("organization %q", c.tgt.Org)
	}
	return fmt.Sprintf("%s %q", noun, name)
}

// isDenied reports whether a failed run is a permission refusal.
func isDenied(r result) bool {
	low := strings.ToLower(r.stderr)
	return strings.Contains(low, "403") || strings.Contains(low, "forbidden") ||
		strings.Contains(low, "permission")
}

// wantDenied fails the case unless r is a permission refusal.
func wantDenied(t *testing.T, r result) {
	t.Helper()
	if r.exitCode == 0 || !isDenied(r) {
		t.Fatalf("want a permission error: %s", r)
	}
}

// aclRoundTrip is the full ACL flow on one noun: show the new object's ACL,
// prove a fresh actor may not read it, grant it "grant", prove it now can,
// re-grant (a no-op), revoke, and prove it is refused again.
func aclRoundTrip(n aclNoun) func(*testing.T, Target, *cli) {
	return func(t *testing.T, _ Target, c *cli) {
		name := n.create(c)
		var who aclActor
		if n.user {
			who = newACLUser(c)
		} else {
			who = newACLClient(c)
		}

		acl := showACL(c, n.noun, name)
		for _, perm := range cinc.ACLPerms {
			ace, _ := acl.ACEFor(perm)
			if len(ace.Actors) == 0 && len(ace.Groups) == 0 {
				t.Errorf("%s ACE of a new %s is empty: %+v", perm, n.noun, acl)
			}
		}
		if slices.Contains(acl.Grant.Actors, who.name) {
			t.Fatalf("a fresh actor already holds grant: %+v", acl.Grant)
		}
		human := c.run(aclArgs(n.noun, "show", "", name)...)
		for _, perm := range cinc.ACLPerms {
			if !strings.Contains(human, perm) {
				t.Errorf("%s acl show is missing the %s permission:\n%s", n.noun, perm, human)
			}
		}

		showAs := aclArgs(n.noun, "show", "", name)
		wantDenied(t, who.as(c, showAs...))

		target := aclTarget(c, n.noun, name)
		grant := append(aclArgs(n.noun, "grant", "grant", name), who.flag()...)
		revoke := append(aclArgs(n.noun, "revoke", "grant", name), who.flag()...)
		if n.noun == "org" {
			// The organization outlives the case, so undo the grant even
			// when the case fails half way.
			t.Cleanup(func() { c.exec(runOpts{}, revoke...) })
		}
		wantEqual(t, "grant output", c.run(grant...), fmt.Sprintf("Granted grant on %s to %s\n", target, who.name))
		if acl := showACL(c, n.noun, name); !slices.Contains(acl.Grant.Actors, who.name) {
			t.Fatalf("after grant, grant actors = %v, want %s", acl.Grant.Actors, who.name)
		}
		// Only the grant ACE changed.
		if acl := showACL(c, n.noun, name); slices.Contains(acl.Read.Actors, who.name) {
			t.Errorf("granting grant also changed read: %+v", acl.Read)
		}

		r := who.as(c, append(showAs, "--format", "json")...)
		if r.exitCode != 0 {
			t.Fatalf("after grant, the actor still cannot read the ACL: %s", r)
		}
		var seen cinc.ACL
		if err := json.Unmarshal([]byte(r.stdout), &seen); err != nil || !slices.Contains(seen.Grant.Actors, who.name) {
			t.Errorf("the actor's own view of the ACL = %q (%v), want it to list the actor", r.stdout, err)
		}

		if again := c.run(grant...); !strings.Contains(again, "No change") || !strings.Contains(again, who.name) {
			t.Errorf("re-granting should be a friendly no-op, got %q", again)
		}

		wantEqual(t, "revoke output", c.run(revoke...), fmt.Sprintf("Revoked grant on %s from %s\n", target, who.name))
		if acl := showACL(c, n.noun, name); slices.Contains(acl.Grant.Actors, who.name) {
			t.Errorf("after revoke, grant actors = %v, want %s gone", acl.Grant.Actors, who.name)
		}
		wantDenied(t, who.as(c, showAs...))
	}
}

// testACLShowShape checks the JSON shape of an ACL: exactly the five
// permissions, each with actors and groups arrays (never null), and the
// creating admin in control of the new object, directly or through the
// admins group.
func testACLShowShape(t *testing.T, tgt Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	var raw map[string]map[string]any
	c.json(&raw, "node", "acl", "show", name)
	for _, perm := range cinc.ACLPerms {
		ace, ok := raw[perm]
		if !ok {
			t.Errorf("acl show is missing %q: %v", perm, raw)
			continue
		}
		for _, key := range []string{"actors", "groups"} {
			if _, ok := ace[key].([]any); !ok {
				t.Errorf("%s.%s = %#v, want a JSON array", perm, key, ace[key])
			}
		}
	}
	if len(raw) != len(cinc.ACLPerms) {
		t.Errorf("acl show has %d permissions, want %d: %v", len(raw), len(cinc.ACLPerms), raw)
	}
	acl := showACL(c, "node", name)
	if !slices.Contains(acl.Grant.Actors, tgt.Admin) && !slices.Contains(acl.Grant.Groups, "admins") {
		t.Errorf("grant ACE of a node the admin created = %+v, want the admin or the admins group", acl.Grant)
	}
	// A client reads nodes through the clients group: that is how
	// chef-client reads the nodes it searches for.
	if !slices.Contains(acl.Read.Groups, "clients") {
		t.Errorf("read groups of a new node = %v, want clients", acl.Read.Groups)
	}
}

// testACLNodeRead takes read away from the clients group on one node, so a
// client that could read it no longer can, then gives read back to that one
// client directly.
func testACLNodeRead(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	who := newACLClient(c)
	if r := who.as(c, "node", "show", name); r.exitCode != 0 {
		t.Fatalf("a client should read a node through the clients group: %s", r)
	}

	c.run("node", "acl", "revoke", "read", name, "--group", "clients")
	if acl := showACL(c, "node", name); slices.Contains(acl.Read.Groups, "clients") {
		t.Fatalf("after revoke, read groups = %v", acl.Read.Groups)
	}
	wantDenied(t, who.as(c, "node", "show", name))

	c.run(append([]string{"node", "acl", "grant", "read", name}, who.flag()...)...)
	if r := who.as(c, "node", "show", name); r.exitCode != 0 {
		t.Fatalf("after a direct read grant, the client still cannot read the node: %s", r)
	}
	// Other nodes keep their default ACL.
	other := uniqueName(t, "node")
	createNode(c, other)
	if r := who.as(c, "node", "show", other); r.exitCode != 0 {
		t.Errorf("revoking on one node changed another: %s", r)
	}
}

// testACLNodeUpdate grants a client update on one node, which the default
// ACL gives only to users and admins, and checks that edit follows it.
func testACLNodeUpdate(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	who := newACLClient(c)
	file := writeJSON(t, cinc.Node{Name: name, Environment: "_default", RunList: []string{"recipe[granted]"}})

	wantDenied(t, who.as(c, "node", "edit", name, "--file", file))
	if rl := showNode(c, name).RunList; len(rl) != 0 {
		t.Fatalf("a refused edit changed the node: run list %v", rl)
	}

	c.run(append([]string{"node", "acl", "grant", "update", name}, who.flag()...)...)
	if r := who.as(c, "node", "edit", name, "--file", file); r.exitCode != 0 {
		t.Fatalf("after granting update, the client still cannot edit the node: %s", r)
	}
	wantSlice(t, "run list after the client's edit", showNode(c, name).RunList, []string{"recipe[granted]"})
	// update is not delete.
	wantDenied(t, who.as(c, "node", "delete", name))

	c.run(append([]string{"node", "acl", "revoke", "update", name}, who.flag()...)...)
	wantDenied(t, who.as(c, "node", "edit", name, "--file", file))
}

// testACLGrantAll grants and revokes the "all" pseudo-permission, which
// expands to the five real ones.
func testACLGrantAll(t *testing.T, _ Target, c *cli) {
	role := createACLRole(c)
	group := createACLGroup(c)

	out := c.run("role", "acl", "grant", "all", role, "--group", group)
	wantEqual(t, "grant all output", out,
		fmt.Sprintf("Granted create, read, update, delete, grant on role %q to %s\n", role, group))
	acl := showACL(c, "role", role)
	for _, perm := range cinc.ACLPerms {
		if ace, _ := acl.ACEFor(perm); !slices.Contains(ace.Groups, group) {
			t.Errorf("after grant all, %s groups = %v, want %s", perm, ace.Groups, group)
		}
	}

	// Revoking one permission leaves the other four.
	c.run("role", "acl", "revoke", "delete", role, "--group", group)
	acl = showACL(c, "role", role)
	if slices.Contains(acl.Delete.Groups, group) || !slices.Contains(acl.Update.Groups, group) {
		t.Errorf("after revoking delete: delete %v, update %v", acl.Delete.Groups, acl.Update.Groups)
	}

	// revoke all reports only the permissions that changed.
	out = c.run("role", "acl", "revoke", "all", role, "--group", group)
	wantEqual(t, "revoke all output", out,
		fmt.Sprintf("Revoked create, read, update, grant on role %q from %s\n", role, group))
	acl = showACL(c, "role", role)
	for _, perm := range cinc.ACLPerms {
		if ace, _ := acl.ACEFor(perm); slices.Contains(ace.Groups, group) {
			t.Errorf("after revoke all, %s groups still has %s", perm, group)
		}
	}
}

// testACLMultipleMembers grants two clients and a group in one call, then
// revokes them together.
func testACLMultipleMembers(t *testing.T, _ Target, c *cli) {
	env := uniqueName(t, "env")
	createEnvironment(c, env)
	a, b := newACLClient(c), newACLClient(c)
	group := createACLGroup(c)

	members := []string{"--client", a.name, "--client", b.name, "--group", group}
	out := c.run(append([]string{"environment", "acl", "grant", "grant", env}, members...)...)
	wantEqual(t, "grant output", out,
		fmt.Sprintf("Granted grant on environment %q to %s, %s, %s\n", env, a.name, b.name, group))
	acl := showACL(c, "environment", env)
	for _, n := range []string{a.name, b.name} {
		if !slices.Contains(acl.Grant.Actors, n) {
			t.Errorf("grant actors = %v, want %s", acl.Grant.Actors, n)
		}
	}
	if !slices.Contains(acl.Grant.Groups, group) {
		t.Errorf("grant groups = %v, want %s", acl.Grant.Groups, group)
	}
	for _, who := range []aclActor{a, b} {
		if r := who.as(c, "environment", "acl", "show", env); r.exitCode != 0 {
			t.Errorf("%s was granted but cannot read the ACL: %s", who.name, r)
		}
	}

	c.run(append([]string{"environment", "acl", "revoke", "grant", env}, members...)...)
	acl = showACL(c, "environment", env)
	if slices.Contains(acl.Grant.Actors, a.name) || slices.Contains(acl.Grant.Actors, b.name) ||
		slices.Contains(acl.Grant.Groups, group) {
		t.Errorf("after revoke, grant = %+v", acl.Grant)
	}
}

// testACLNoOp re-grants a member that is already there and revokes one that
// is not: both are friendly no-ops that leave the ACL as it was.
func testACLNoOp(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	before := showACL(c, "node", name)

	out := c.run("node", "acl", "grant", "read", name, "--group", "clients")
	if !strings.Contains(out, "No change") || !strings.Contains(out, "clients") {
		t.Errorf("re-granting read to clients = %q, want a friendly no-op", out)
	}
	group := createACLGroup(c)
	out = c.run("node", "acl", "revoke", "update", name, "--group", group)
	if !strings.Contains(out, "No change") || !strings.Contains(out, group) {
		t.Errorf("revoking a group that holds nothing = %q, want a friendly no-op", out)
	}
	after := showACL(c, "node", name)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("a no-op changed the ACL:\nbefore %+v\nafter  %+v", before, after)
	}
}

// testACLViaGroup grants a group, and checks that the grant reaches a client
// only while it is a member.
func testACLViaGroup(t *testing.T, _ Target, c *cli) {
	role := createACLRole(c)
	group := createACLGroup(c)
	who := newACLClient(c)

	c.run("role", "acl", "grant", "grant", role, "--group", group)
	wantDenied(t, who.as(c, "role", "acl", "show", role))

	c.run("group", "member", "add", group, who.name, "--type", "client")
	if r := who.as(c, "role", "acl", "show", role); r.exitCode != 0 {
		t.Fatalf("a member of a granted group cannot read the ACL: %s", r)
	}

	c.run("group", "member", "remove", group, who.name, "--type", "client")
	wantDenied(t, who.as(c, "role", "acl", "show", role))
}

// testACLViaNestedGroup grants an outer group that contains an inner group
// that contains a client: membership is transitive.
func testACLViaNestedGroup(t *testing.T, _ Target, c *cli) {
	bag := aclNouns["databag"].create(c)
	outer, inner := createACLGroup(c), createACLGroup(c)
	who := newACLClient(c)

	c.run("group", "member", "add", inner, who.name, "--type", "client")
	c.run("group", "member", "add", outer, inner, "--type", "group")
	c.run("databag", "acl", "grant", "grant", bag, "--group", outer)
	if r := who.as(c, "databag", "acl", "show", bag); r.exitCode != 0 {
		t.Fatalf("a member of a group nested in a granted group cannot read the ACL: %s", r)
	}

	// Cutting the nesting cuts the grant, though the client is still in the
	// inner group.
	c.run("group", "member", "remove", outer, inner, "--type", "group")
	wantDenied(t, who.as(c, "databag", "acl", "show", bag))
}

// testACLBadInput checks the errors the CLI raises before it sends anything:
// an unknown permission and a grant that names no member. Both are checked
// against an object that does not exist, so a request would fail with a
// not-found instead.
func testACLBadInput(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	r := c.fail("node", "acl", "grant", "execute", ghost, "--group", "admins")
	if !strings.Contains(r.stderr, `"execute"`) || !strings.Contains(r.stderr, "create, read, update, delete, grant") || isNotFound(r) {
		t.Errorf("an unknown permission should name it and list the valid ones: %s", r)
	}
	r = c.fail("node", "acl", "revoke", "read", ghost)
	if !strings.Contains(r.stderr, "--user, --client, or --group") || isNotFound(r) {
		t.Errorf("a revoke without members should say which flags to pass: %s", r)
	}
	r = c.fail("org", "acl", "grant", "all")
	if !strings.Contains(r.stderr, "--user, --client, or --group") {
		t.Errorf("an org grant without members should say which flags to pass: %s", r)
	}
	// A wrong argument count is a usage error, not a request.
	r = c.fail("node", "acl", "grant", "read")
	if !strings.Contains(r.stderr, "arg") {
		t.Errorf("grant without an object name should be a usage error: %s", r)
	}
}

// testACLForbiddenMessage checks that an actor without grant on an object
// gets an error that says what it lacks and on what, rather than a raw
// request line.
func testACLForbiddenMessage(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	who := newACLClient(c)
	for _, args := range [][]string{
		{"node", "acl", "show", name},
		{"node", "acl", "grant", "read", name, "--client", who.name},
		{"node", "acl", "revoke", "read", name, "--group", "clients"},
	} {
		r := who.as(c, args...)
		wantDenied(t, r)
		if !strings.Contains(r.stderr, fmt.Sprintf("grant permission on node %q", name)) {
			t.Errorf("cinc %s: want an error naming the missing grant permission and the node: %s",
				strings.Join(args, " "), r)
		}
		if strings.Contains(r.stderr, "/organizations/") {
			t.Errorf("cinc %s: the error should not be a raw request line: %s", strings.Join(args, " "), r)
		}
	}
	// The refused revoke changed nothing.
	if acl := showACL(c, "node", name); !slices.Contains(acl.Read.Groups, "clients") {
		t.Errorf("a refused revoke changed the ACL: %+v", acl.Read)
	}
}

// testACLMissingObject checks that the ACL of an object that does not exist
// is a not-found, for show and for grant. erchef looks the object up before
// its ACL.
func testACLMissingObject(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("node", "acl", "show", ghost))
	wantNotFound(t, c.fail("node", "acl", "grant", "read", ghost, "--group", "admins"))
	wantNotFound(t, c.fail("role", "acl", "show", ghost))
	wantNotFound(t, c.fail("databag", "acl", "show", ghost))
}

// testACLUnknownMember grants to a client and to a group that do not exist.
// erchef resolves every name in an ACE and refuses the write with a 400; the
// CLI must fail and leave the ACL as it was.
func testACLUnknownMember(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	before := showACL(c, "node", name)

	ghostClient := uniqueName(t, "ghost")
	r := c.fail("node", "acl", "grant", "read", name, "--client", ghostClient)
	if !strings.Contains(r.stderr, ghostClient) && !strings.Contains(r.stderr, "exist") {
		t.Errorf("granting to an unknown client should say it does not exist: %s", r)
	}
	ghostGroup := uniqueName(t, "ghost")
	r = c.fail("node", "acl", "grant", "read", name, "--group", ghostGroup)
	if !strings.Contains(r.stderr, ghostGroup) && !strings.Contains(r.stderr, "exist") {
		t.Errorf("granting to an unknown group should say it does not exist: %s", r)
	}
	if after := showACL(c, "node", name); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("a refused grant changed the ACL:\nbefore %+v\nafter  %+v", before, after)
	}
}
