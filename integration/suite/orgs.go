package suite

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// The org family covers two scopes. list/show/create/edit/delete talk to the
// server root (/organizations); member and invite act on the org the
// profile's server URL points at.
//
// On erchef some of these are reserved to the pivotal superuser: creating an
// org, and adding a member without an invitation (POST
// /organizations/O/users is superuser_only in oc_chef_wm_org_associations).
// The cinc-server-erlang target's admin is a server-admin, not pivotal, so
// those operations live in cases of their own that a gap can skip narrowly.
// Everything else gets a user into an org the way a non-pivotal admin does:
// an invitation, accepted by the invitee.
//
// The CLI has no command to accept an invitation (that is the invitee's
// side, /users/U/association_requests). Cases that need a member accept
// through cinc-api directly, as that user; the acceptance itself is not a CLI
// feature under test.
var orgFamily = family{cases: []testCase{
	{"orgs/show", []string{"org show"}, testOrgShow},
	{"orgs/list", []string{"org list"}, testOrgList},
	{"orgs/lifecycle", []string{"org create", "org show", "org list", "org edit", "org delete"}, testOrgLifecycle},
	{"orgs/create-stdout", []string{"org create", "org delete"}, testOrgCreateStdout},
	{"orgs/already-exists", []string{"org create"}, testOrgAlreadyExists},
	{"orgs/create-invalid-name", []string{"org create"}, testOrgCreateInvalidName},
	{"orgs/edit", []string{"org edit", "org show"}, testOrgEdit},
	{"orgs/not-found", []string{"org show", "org delete", "org edit"}, testOrgNotFound},
	{"orgs/member-list", []string{"org member list"}, testOrgMemberList},
	{"orgs/member-add", []string{"org member add", "org member list", "org member remove"}, testOrgMemberAdd},
	{"orgs/member-add-errors", []string{"org member add"}, testOrgMemberAddErrors},
	{"orgs/member-remove", []string{"org member remove", "org member list"}, testOrgMemberRemove},
	{"orgs/member-remove-admin", []string{"org member remove"}, testOrgMemberRemoveAdmin},
	{"orgs/member-add-forbidden", []string{"org member add"}, testOrgMemberAddForbidden},
	{"orgs/member-remove-forbidden", []string{"org member remove"}, testOrgMemberRemoveForbidden},
	{"orgs/member-list-outsider", []string{"org member list"}, testOrgMemberListOutsider},
	{"orgs/invite", []string{"org invite create", "org invite list", "org invite rescind"}, testOrgInvite},
	{"orgs/invite-errors", []string{"org invite create", "org invite rescind"}, testOrgInviteErrors},
	{"orgs/invite-accept", []string{"org invite create", "org invite list", "org member list"}, testOrgInviteAccept},
	{"orgs/invite-other-org", []string{"org invite create", "org invite list", "org invite rescind"}, testOrgInviteOtherOrg},
	{"orgs/invite-create-forbidden", []string{"org invite create"}, testOrgInviteCreateForbidden},
	{"orgs/invite-rescind-forbidden", []string{"org invite rescind"}, testOrgInviteRescindForbidden},
}}

// orgMembers returns `org member list --format json` for the profile flags.
func orgMembers(c *cli, flags ...string) []string {
	c.t.Helper()
	var names []string
	c.json(&names, append([]string{"org", "member", "list"}, flags...)...)
	return names
}

// orgInvites returns `org invite list --format json` for the profile flags.
func orgInvites(c *cli, flags ...string) []cinc.Invitation {
	c.t.Helper()
	var invites []cinc.Invitation
	c.json(&invites, append([]string{"org", "invite", "list"}, flags...)...)
	return invites
}

// findInvite returns the pending invitation for user in the profile's org.
func findInvite(c *cli, user string, flags ...string) (cinc.Invitation, bool) {
	c.t.Helper()
	invites := orgInvites(c, flags...)
	i := slices.IndexFunc(invites, func(inv cinc.Invitation) bool { return inv.Username == user })
	if i < 0 {
		return cinc.Invitation{}, false
	}
	return invites[i], true
}

// inviteUser invites user to the profile's org and registers a rescind, so
// an invitation a case leaves pending does not outlive it.
func inviteUser(c *cli, user string, flags ...string) cinc.Invitation {
	c.t.Helper()
	c.run(append([]string{"org", "invite", "create", user}, flags...)...)
	inv, ok := findInvite(c, user, flags...)
	if !ok {
		c.t.Fatalf("org invite list does not show the invitation for %s", user)
	}
	c.cleanup(append([]string{"org", "invite", "rescind", inv.ID}, flags...)...)
	return inv
}

// joinOrg makes u a member of tgt.Org the way any org admin can: an
// invitation, accepted by u. It registers the member's removal.
func joinOrg(c *cli, u testUser) {
	c.t.Helper()
	inviteUser(c, u.name)
	acceptInvite(c, u, c.tgt.Org)
	c.cleanup("org", "member", "remove", u.name)
}

// acceptInvite accepts u's pending invitation to org, signing as u through
// cinc-api: the CLI has no command for the invitee's side of the flow.
func acceptInvite(c *cli, u testUser, org string) {
	c.t.Helper()
	api := apiClientAs(c, u)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	invites, _, err := api.Associations.ListUserInvites(ctx, u.name)
	if err != nil {
		c.t.Fatalf("list %s's invitations: %v", u.name, err)
	}
	i := slices.IndexFunc(invites, func(inv cinc.Invitation) bool { return inv.OrgName == org })
	if i < 0 {
		c.t.Fatalf("%s has no invitation to %s: %+v", u.name, org, invites)
	}
	if _, err := api.Associations.RespondInvite(ctx, u.name, invites[i].ID, true); err != nil {
		c.t.Fatalf("%s accepting the invitation to %s: %v", u.name, org, err)
	}
}

// apiClientAs is a cinc-api client signing as u against tgt.Org, trusting
// the target's CA when it has one.
func apiClientAs(c *cli, u testUser) *cinc.Client {
	c.t.Helper()
	key, err := cinc.LoadKeyFile(u.keyPath)
	if err != nil {
		c.t.Fatal(err)
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if c.tgt.CACertPath != "" {
		pemBytes, err := os.ReadFile(c.tgt.CACertPath)
		if err != nil {
			c.t.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			c.t.Fatalf("no certificates in %s", c.tgt.CACertPath)
		}
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	api, err := cinc.NewClient(cinc.Config{
		ServerURL: c.tgt.ServerURL, Org: c.tgt.Org, ClientName: u.name, Key: key,
	}, cinc.WithHTTPClient(hc))
	if err != nil {
		c.t.Fatal(err)
	}
	return api
}

// wantSignsInOrg fails the case unless u can read the org's nodes, which
// needs u to be a member.
func wantSignsInOrg(c *cli, u testUser) {
	c.t.Helper()
	c.run(append([]string{"node", "list"}, actAs(c, u)...)...)
}

// wantNotInOrg fails the case unless u is refused the org's nodes.
func wantNotInOrg(c *cli, u testUser) {
	c.t.Helper()
	wantForbidden(c.t, c.fail(append([]string{"node", "list"}, actAs(c, u)...)...))
}

// createOrg creates an org with its validator key written to a temp file and
// registers its cleanup.
func createOrg(c *cli, name, fullName string) (keyPath, out string) {
	c.t.Helper()
	keyPath = filepath.Join(c.t.TempDir(), name+"-validator.pem")
	c.cleanup("org", "delete", name)
	out = c.run("org", "create", name, fullName, "--filename", keyPath)
	return keyPath, out
}

var guidPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// testOrgShow checks org show against erchef's shape: exactly name,
// full_name and guid, the guid being 32 hex characters.
func testOrgShow(t *testing.T, tgt Target, c *cli) {
	var raw map[string]any
	c.json(&raw, "org", "show", tgt.Org)
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	wantSlice(t, "org show keys", keys, []string{"full_name", "guid", "name"})
	wantEqual(t, "name", raw["name"], any(tgt.Org))
	if fn, _ := raw["full_name"].(string); fn == "" {
		t.Errorf("full_name is empty: %v", raw)
	}
	if guid, _ := raw["guid"].(string); !guidPattern.MatchString(guid) {
		t.Errorf("guid = %q, want 32 hex characters", guid)
	}

	human := c.run("org", "show", tgt.Org)
	if !strings.Contains(human, tgt.Org) {
		t.Errorf("org show missing the org name:\n%s", human)
	}

	var other cinc.Org
	c.json(&other, "org", "show", tgt.OtherOrg)
	wantEqual(t, "other org name", other.Name, tgt.OtherOrg)
	if other.GUID == raw["guid"] {
		t.Errorf("two orgs share guid %s", other.GUID)
	}
}

func testOrgList(t *testing.T, tgt Target, c *cli) {
	var names []string
	c.json(&names, "org", "list")
	for _, want := range []string{tgt.Org, tgt.OtherOrg} {
		if !slices.Contains(names, want) {
			t.Errorf("org list --format json = %v, want it to include %s", names, want)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("org list --format json is not sorted: %v", names)
	}
	human := strings.Fields(c.run("org", "list"))
	for _, want := range []string{tgt.Org, tgt.OtherOrg} {
		if !slices.Contains(human, want) {
			t.Errorf("org list = %v, want it to include %s", human, want)
		}
	}
}

// testOrgLifecycle creates an org, proves its validator key signs as the
// validator (which may register clients), edits and deletes it. Creating an
// org is pivotal-only on erchef.
func testOrgLifecycle(t *testing.T, tgt Target, c *cli) {
	name := uniqueName(t, "org")
	keyPath, out := createOrg(c, name, "Widgets Inc")
	wantEqual(t, "create output", out, fmt.Sprintf("Created organization %q (validator key written to %s)\n", name, keyPath))
	if _, err := cinc.LoadKeyFile(keyPath); err != nil {
		t.Fatalf("org create --filename wrote an unusable key: %v", err)
	}

	var org cinc.Org
	c.json(&org, "org", "show", name)
	wantEqual(t, "name", org.Name, name)
	wantEqual(t, "full_name", org.FullName, "Widgets Inc")
	if !guidPattern.MatchString(org.GUID) {
		t.Errorf("guid = %q, want 32 hex characters", org.GUID)
	}
	var names []string
	c.json(&names, "org", "list")
	if !slices.Contains(names, name) {
		t.Errorf("org list does not include %s: %v", name, names)
	}

	// The validator may create clients in its org and nothing else.
	c.addProfile("validator", name, name+"-validator", keyPath)
	client := uniqueName(t, "client")
	c.run("client", "create", client, "--profile", "validator", "--key-file", filepath.Join(t.TempDir(), "client.pem"))
	c.addProfile("new-org", name, tgt.Admin, tgt.KeyPath)
	var clients []string
	c.json(&clients, "client", "list", "--profile", "new-org")
	if !slices.Contains(clients, client) || !slices.Contains(clients, name+"-validator") {
		t.Errorf("client list in %s = %v, want %s and the validator", name, clients, client)
	}

	file := writeJSON(t, cinc.Org{Name: "ignored", FullName: "Widgets International"})
	wantEqual(t, "edit output", c.run("org", "edit", name, "--file", file), fmt.Sprintf("Updated organization %q\n", name))
	c.json(&org, "org", "show", name)
	wantEqual(t, "name after edit", org.Name, name)
	wantEqual(t, "full_name after edit", org.FullName, "Widgets International")

	wantEqual(t, "delete output", c.run("org", "delete", name), fmt.Sprintf("Deleted organization %q\n", name))
	wantNotFound(t, c.fail("org", "show", name))
	c.json(&names, "org", "list")
	if slices.Contains(names, name) {
		t.Errorf("org list still includes deleted %s", name)
	}
	// The org's objects went with it.
	wantNotFound(t, c.fail("client", "list", "--profile", "new-org"))
}

// testOrgCreateStdout streams the validator key to stdout, with the
// can't-see-it-again warning on stderr so stdout stays pipeable.
func testOrgCreateStdout(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "org")
	c.cleanup("org", "delete", name)
	r := c.exec(runOpts{}, "org", "create", name, "Streamed Key Inc")
	if r.exitCode != 0 {
		t.Fatalf("org create: %s", r)
	}
	if !strings.HasPrefix(r.stdout, "-----BEGIN") || !strings.Contains(r.stdout, "PRIVATE KEY") {
		t.Errorf("stdout should be exactly the validator key: %s", r)
	}
	if _, err := cinc.ParseKey([]byte(r.stdout)); err != nil {
		t.Errorf("stdout is not a usable key: %v", err)
	}
	if !strings.Contains(r.stderr, fmt.Sprintf("Created organization %q", name)) || !strings.Contains(r.stderr, "again") {
		t.Errorf("stderr should confirm and warn that the key is shown once: %s", r)
	}
	wantEqual(t, "delete output", c.run("org", "delete", name), fmt.Sprintf("Deleted organization %q\n", name))
}

func testOrgAlreadyExists(t *testing.T, tgt Target, c *cli) {
	r := c.fail("org", "create", tgt.Org, "Duplicate Inc", "--filename", filepath.Join(t.TempDir(), "v.pem"))
	wantFailure(t, r, "already exists", "409", "conflict")
}

// testOrgCreateInvalidName checks erchef's org name rule,
// ^[a-z0-9][a-z0-9_-]{0,254}$: upper case, spaces and a leading hyphen are
// a 400, and nothing is created.
func testOrgCreateInvalidName(t *testing.T, _ Target, c *cli) {
	for _, name := range []string{
		"T-Org-" + randomHex(t, 4),
		"t_org " + randomHex(t, 4),
		"-t-org-" + randomHex(t, 4),
	} {
		// "--" so the leading-hyphen name reaches the server as a name.
		r := c.exec(runOpts{}, "org", "create", "--filename", filepath.Join(t.TempDir(), "v.pem"), "--", name, "Bad Name Inc")
		if r.exitCode == 0 {
			c.cleanup("org", "delete", "--", name)
			t.Errorf("org create %q succeeded, want a 400: %s", name, r)
			continue
		}
		wantFailure(t, r, "malformed org name", "400", "invalid")
	}
}

// testOrgEdit changes the second org's full name and puts it back. An org
// admin may update the org on erchef, so this needs no superuser.
func testOrgEdit(t *testing.T, tgt Target, c *cli) {
	var before cinc.Org
	c.json(&before, "org", "show", tgt.OtherOrg)
	restore := writeJSON(t, cinc.Org{Name: tgt.OtherOrg, FullName: before.FullName})
	c.t.Cleanup(func() {
		if r := c.exec(runOpts{}, "org", "edit", tgt.OtherOrg, "--file", restore); r.exitCode != 0 {
			t.Errorf("restoring %s's full name: %s", tgt.OtherOrg, r)
		}
	})

	full := "Other Org " + randomHex(t, 4)
	file := writeJSON(t, cinc.Org{FullName: full})
	wantEqual(t, "edit output", c.run("org", "edit", tgt.OtherOrg, "--file", file),
		fmt.Sprintf("Updated organization %q\n", tgt.OtherOrg))
	var after cinc.Org
	c.json(&after, "org", "show", tgt.OtherOrg)
	wantEqual(t, "full_name", after.FullName, full)
	wantEqual(t, "name", after.Name, tgt.OtherOrg)
	wantEqual(t, "guid survives an edit", after.GUID, before.GUID)
}

func testOrgNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("org", "delete", ghost)
	wantNotFound(t, c.fail("org", "show", ghost))
	wantNotFound(t, c.fail("org", "delete", ghost))
	file := writeJSON(t, cinc.Org{FullName: "Ghost Inc"})
	wantNotFound(t, c.fail("org", "edit", ghost, "--file", file))
	wantNotFound(t, c.fail("org", "show", ghost))
}

// testOrgMemberList lists both orgs' members. The --profile switch is how
// the CLI picks the org, and a member of one org is not listed in the other.
// (The admin is not asserted: pivotal is a superuser, not a member, on
// cinc-server-ng.)
func testOrgMemberList(t *testing.T, tgt Target, c *cli) {
	here := createUser(c)
	joinOrg(c, here)
	there := createUser(c)
	inviteUser(c, there.name, "--profile", "other")
	acceptInvite(c, there, tgt.OtherOrg)
	c.cleanup("org", "member", "remove", there.name, "--profile", "other")

	members := orgMembers(c)
	if !slices.Contains(members, here.name) || slices.Contains(members, there.name) {
		t.Errorf("org member list = %v, want %s and not %s", members, here.name, there.name)
	}
	if !slices.IsSorted(members) {
		t.Errorf("org member list is not sorted: %v", members)
	}
	others := orgMembers(c, "--profile", "other")
	if !slices.Contains(others, there.name) || slices.Contains(others, here.name) {
		t.Errorf("org member list --profile other = %v, want %s and not %s", others, there.name, here.name)
	}
	human := strings.Fields(c.run("org", "member", "list"))
	if !slices.Contains(human, here.name) {
		t.Errorf("org member list = %v, want it to include %s", human, here.name)
	}
}

// testOrgMemberAdd adds a member without an invitation, which erchef allows
// only the pivotal superuser.
func testOrgMemberAdd(t *testing.T, tgt Target, c *cli) {
	u := createUser(c)
	wantNotInOrg(c, u)

	c.cleanup("org", "member", "remove", u.name)
	wantEqual(t, "add output", c.run("org", "member", "add", u.name),
		fmt.Sprintf("Added %q to organization %q\n", u.name, tgt.Org))
	if !slices.Contains(orgMembers(c), u.name) {
		t.Errorf("org member list does not include %s after add", u.name)
	}
	wantSignsInOrg(c, u)
	if slices.Contains(orgMembers(c, "--profile", "other"), u.name) {
		t.Errorf("adding %s to %s also added them to %s", u.name, tgt.Org, tgt.OtherOrg)
	}

	wantFailure(t, c.fail("org", "member", "add", u.name), "already exists", "409", "conflict")

	wantEqual(t, "remove output", c.run("org", "member", "remove", u.name),
		fmt.Sprintf("Removed %q from organization %q\n", u.name, tgt.Org))
	wantNotInOrg(c, u)
}

// testOrgMemberAddErrors adds a user that does not exist, which is a 404
// whoever asks.
func testOrgMemberAddErrors(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("org", "member", "add", ghost))
}

// testOrgMemberRemove removes a member who joined by invitation, so it runs
// without pivotal.
func testOrgMemberRemove(t *testing.T, tgt Target, c *cli) {
	u := createUser(c)
	joinOrg(c, u)
	wantSignsInOrg(c, u)

	wantEqual(t, "remove output", c.run("org", "member", "remove", u.name),
		fmt.Sprintf("Removed %q from organization %q\n", u.name, tgt.Org))
	if members := orgMembers(c); slices.Contains(members, u.name) {
		t.Errorf("org member list still includes %s after remove: %v", u.name, members)
	}
	// Removal deprovisions the user: their key no longer opens the org.
	wantNotInOrg(c, u)

	wantNotFound(t, c.fail("org", "member", "remove", u.name))
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("org", "member", "remove", ghost))
}

// testOrgMemberRemoveAdmin checks erchef's rule that a member of the org's
// admins group cannot be removed from the org until they leave that group
// (a 403, "Please remove U from this organization's admins group before
// removing him or her from the organization.").
func testOrgMemberRemoveAdmin(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	joinOrg(c, u)
	c.run("group", "member", "add", "admins", u.name)
	c.cleanup("group", "member", "remove", "admins", u.name)

	r := c.fail("org", "member", "remove", u.name)
	wantFailure(t, r, "admins group")
	if !slices.Contains(orgMembers(c), u.name) {
		t.Errorf("a refused remove still removed %s", u.name)
	}

	c.run("group", "member", "remove", "admins", u.name)
	c.run("org", "member", "remove", u.name)
	if slices.Contains(orgMembers(c), u.name) {
		t.Errorf("%s is still a member after leaving admins and the org", u.name)
	}
}

// testOrgMemberAddForbidden: adding a member without an invitation is
// superuser_only on erchef, so an ordinary member is refused.
func testOrgMemberAddForbidden(t *testing.T, _ Target, c *cli) {
	member := createUser(c)
	joinOrg(c, member)
	outsider := createUser(c)
	c.cleanup("org", "member", "remove", outsider.name)

	wantForbidden(t, c.fail(append([]string{"org", "member", "add", outsider.name}, actAs(c, member)...)...))
	if slices.Contains(orgMembers(c), outsider.name) {
		t.Errorf("a refused add made %s a member", outsider.name)
	}
}

// testOrgMemberRemoveForbidden: removing someone else needs update on the
// org, which an ordinary member lacks (erchef gives the users group only
// read on the org and its groups container).
func testOrgMemberRemoveForbidden(t *testing.T, _ Target, c *cli) {
	member := createUser(c)
	joinOrg(c, member)
	other := createUser(c)
	joinOrg(c, other)

	wantForbidden(t, c.fail(append([]string{"org", "member", "remove", other.name}, actAs(c, member)...)...))
	if !slices.Contains(orgMembers(c), other.name) {
		t.Errorf("a refused remove took %s out of the org", other.name)
	}
}

// testOrgMemberListOutsider: a member may list the org's members, a user
// outside the org may not.
func testOrgMemberListOutsider(t *testing.T, _ Target, c *cli) {
	member := createUser(c)
	joinOrg(c, member)
	outsider := createUser(c)

	if members := orgMembers(c, actAs(c, member)...); !slices.Contains(members, member.name) {
		t.Errorf("a member's org member list = %v, want it to include %s", members, member.name)
	}
	wantForbidden(t, c.fail(append([]string{"org", "member", "list"}, actAs(c, outsider)...)...))
}

func testOrgInvite(t *testing.T, tgt Target, c *cli) {
	u := createUser(c)
	wantEqual(t, "invite output", c.run("org", "invite", "create", u.name),
		fmt.Sprintf("Invited %q to organization %q\n", u.name, tgt.Org))
	inv, ok := findInvite(c, u.name)
	if !ok {
		t.Fatalf("org invite list does not include %s: %+v", u.name, orgInvites(c))
	}
	c.cleanup("org", "invite", "rescind", inv.ID)
	if inv.ID == "" {
		t.Fatalf("invitation for %s has no id: %+v", u.name, inv)
	}

	// The org-side listing is [{"id", "username"}] on erchef.
	var raw []map[string]any
	c.json(&raw, "org", "invite", "list")
	for _, entry := range raw {
		if entry["username"] != u.name {
			continue
		}
		keys := make([]string, 0, len(entry))
		for k := range entry {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		wantSlice(t, "invitation keys", keys, []string{"id", "username"})
	}
	if human := c.run("org", "invite", "list"); !strings.Contains(human, inv.ID) || !strings.Contains(human, u.name) {
		t.Errorf("org invite list should show the id and user:\n%s", human)
	}

	wantFailure(t, c.fail("org", "invite", "create", u.name), "already", "409", "conflict")

	wantEqual(t, "rescind output", c.run("org", "invite", "rescind", inv.ID),
		fmt.Sprintf("Rescinded invitation %q\n", inv.ID))
	if _, ok := findInvite(c, u.name); ok {
		t.Errorf("org invite list still includes %s after rescind", u.name)
	}
	if slices.Contains(orgMembers(c), u.name) {
		t.Errorf("rescinding an invitation made %s a member", u.name)
	}
	wantNotFound(t, c.fail("org", "invite", "rescind", inv.ID))
}

// testOrgInviteErrors: an unknown user is a 404, an existing member is a
// 409, an unknown invitation id is a 404.
func testOrgInviteErrors(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("org", "invite", "create", ghost))

	member := createUser(c)
	joinOrg(c, member)
	wantFailure(t, c.fail("org", "invite", "create", member.name), "already", "409", "conflict")
	if inv, ok := findInvite(c, member.name); ok {
		c.cleanup("org", "invite", "rescind", inv.ID)
		t.Errorf("a refused invite for member %s is pending anyway", member.name)
	}

	wantNotFound(t, c.fail("org", "invite", "rescind", randomHex(t, 16)))
}

// testOrgInviteAccept follows an invitation through to membership: the
// accepted invite leaves the list and the user's key opens the org.
func testOrgInviteAccept(t *testing.T, tgt Target, c *cli) {
	u := createUser(c)
	wantNotInOrg(c, u)
	inviteUser(c, u.name)
	acceptInvite(c, u, tgt.Org)
	c.cleanup("org", "member", "remove", u.name)

	if _, ok := findInvite(c, u.name); ok {
		t.Errorf("an accepted invitation is still pending for %s", u.name)
	}
	if !slices.Contains(orgMembers(c), u.name) {
		t.Errorf("org member list does not include %s after accepting", u.name)
	}
	wantSignsInOrg(c, u)
}

// testOrgInviteOtherOrg keeps invitations per org: one sent in the default
// org is not listed in the other, and the other org cannot rescind it (erchef
// answers 400, "Organization O does not match P in association request").
func testOrgInviteOtherOrg(t *testing.T, _ Target, c *cli) {
	u := createUser(c)
	inv := inviteUser(c, u.name)
	if _, ok := findInvite(c, u.name, "--profile", "other"); ok {
		t.Errorf("an invitation to the default org is listed in the other org")
	}
	c.fail("org", "invite", "rescind", inv.ID, "--profile", "other")
	if _, ok := findInvite(c, u.name); !ok {
		t.Errorf("rescinding through the other org removed the invitation")
	}

	other := inviteUser(c, u.name, "--profile", "other")
	if other.ID == inv.ID {
		t.Errorf("invitations to two orgs share id %s", inv.ID)
	}
	c.run("org", "invite", "rescind", other.ID, "--profile", "other")
	if _, ok := findInvite(c, u.name); !ok {
		t.Errorf("rescinding in the other org removed the default org's invitation")
	}
}

// testOrgInviteCreateForbidden: inviting needs update on the org, which an
// ordinary member lacks.
func testOrgInviteCreateForbidden(t *testing.T, _ Target, c *cli) {
	member := createUser(c)
	joinOrg(c, member)
	invitee := createUser(c)

	r := c.exec(runOpts{}, append([]string{"org", "invite", "create", invitee.name}, actAs(c, member)...)...)
	if inv, ok := findInvite(c, invitee.name); ok {
		c.cleanup("org", "invite", "rescind", inv.ID)
		t.Errorf("a member invited %s", invitee.name)
	}
	wantForbidden(t, r)
}

// testOrgInviteRescindForbidden: rescinding needs update on the org too.
func testOrgInviteRescindForbidden(t *testing.T, _ Target, c *cli) {
	member := createUser(c)
	joinOrg(c, member)
	invitee := createUser(c)
	inv := inviteUser(c, invitee.name)

	wantForbidden(t, c.fail(append([]string{"org", "invite", "rescind", inv.ID}, actAs(c, member)...)...))
	if _, ok := findInvite(c, invitee.name); !ok {
		t.Errorf("a member rescinded %s's invitation", invitee.name)
	}
}
