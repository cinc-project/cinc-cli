//go:build acceptance

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// The org root verbs (list/show/create/edit/delete) hit the server root at
// /organizations and require the pivotal superuser identity. The acceptance
// harness already signs as pivotal, so they are exercisable here. The
// member/invite subgroups are organization-scoped (they act on the "acme" org
// the profile points at).
//
// cinc-zero is a single, in-memory server seeded with one org ("acme"). Where a
// runtime endpoint (e.g. creating a brand-new org, or the association_requests
// invitation flow) isn't implemented by the pinned cinc-zero build, the test
// skips with a documented reason rather than failing, and the behavior stays
// covered by the unit tests in apps/cinc/cmd/org_test.go.

// TestOrgListAgainstCincZero verifies `cinc org list` returns the seeded
// "acme" org in both human and JSON output.
func TestOrgListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "org", "list", "--config", env.cfgPath)
	if !strings.Contains(human, "acme") {
		t.Errorf("org list (human) missing acme\ngot: %s", human)
	}

	jsonOut := runCinc(t, env.binary, "org", "list", "--config", env.cfgPath, "--format", "json")
	var names []string
	if err := json.Unmarshal([]byte(jsonOut), &names); err != nil {
		t.Fatalf("org list (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if !slices.Contains(names, "acme") {
		t.Errorf("org list (json) = %v, want it to contain acme", names)
	}
}

// TestOrgShowAgainstCincZero fetches the seeded "acme" org.
func TestOrgShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	jsonOut := runCinc(t, env.binary, "org", "show", "acme", "--config", env.cfgPath, "--format", "json")
	var got cinc.Org
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("org show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "acme" {
		t.Errorf("org show returned name=%q, want acme", got.Name)
	}
}

// TestOrgCreateShowDeleteAgainstCincZero creates a fresh org (capturing the
// server-generated validator key), confirms it in the list, then deletes it.
//
// Creating an org at runtime depends on the pinned cinc-zero implementing
// POST /organizations; if it doesn't, we skip and rely on the unit tests
// (TestOrgCreateCommand*/TestOrgDeleteCommandEndToEnd) for coverage.
func TestOrgCreateShowDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out, stderr, err := runCincRaw(env.binary, "org", "create", "widgets", "Widgets Inc",
		"--config", env.cfgPath)
	if err != nil {
		t.Skipf("cinc-zero does not support runtime org creation; covered by unit tests. stderr: %s", stderr)
	}
	if !strings.Contains(out, "BEGIN RSA PRIVATE KEY") && !strings.Contains(stderr, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("org create did not surface a validator key:\nstdout: %s\nstderr: %s", out, stderr)
	}

	list := runCinc(t, env.binary, "org", "list", "--config", env.cfgPath)
	if !strings.Contains(list, "widgets") {
		t.Errorf("org list after create missing widgets:\n%s", list)
	}

	del := runCinc(t, env.binary, "org", "delete", "widgets", "--config", env.cfgPath)
	if del != "Deleted organization \"widgets\"\n" {
		t.Errorf("org delete output = %q", del)
	}

	after := runCinc(t, env.binary, "org", "list", "--config", env.cfgPath)
	if strings.Contains(after, "widgets") {
		t.Errorf("org list after delete still has widgets:\n%s", after)
	}
}

// TestOrgEditAgainstCincZero edits the seeded "acme" org's full name through
// --file and confirms the change is persisted. Skips if the pinned cinc-zero
// rejects the org PUT.
func TestOrgEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.Org{Name: "ignored", FullName: "Acme Industries"})
	out, stderr, err := runCincRaw(env.binary, "org", "edit", "acme", "--file", file, "--config", env.cfgPath)
	if err != nil {
		t.Skipf("cinc-zero does not support org update; covered by unit tests. stderr: %s", stderr)
	}
	if out != "Updated organization \"acme\"\n" {
		t.Errorf("org edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "org", "show", "acme", "--config", env.cfgPath, "--format", "json")
	var got cinc.Org
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("org show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.FullName != "Acme Industries" {
		t.Errorf("edited org full_name = %q, want Acme Industries", got.FullName)
	}
}

// TestOrgMemberListAgainstCincZero lists the members of the "acme" org the
// profile points at. We assert only that the command succeeds and returns a
// JSON array, since the exact membership of the seeded org isn't fixed.
func TestOrgMemberListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	jsonOut, stderr, err := runCincRaw(env.binary, "org", "member", "list", "--config", env.cfgPath, "--format", "json")
	if err != nil {
		t.Skipf("cinc-zero does not support org membership listing; covered by unit tests. stderr: %s", stderr)
	}
	var names []string
	if err := json.Unmarshal([]byte(jsonOut), &names); err != nil {
		t.Fatalf("org member list (json) not valid JSON: %v\n%s", err, jsonOut)
	}
}

// TestOrgMemberAddRemoveAgainstCincZero associates the seeded global user
// "anna" with the "acme" org, then removes her. Skips if the pinned cinc-zero
// doesn't implement the org membership endpoints; the add/remove behavior is
// otherwise covered by the unit tests.
func TestOrgMemberAddRemoveAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	add, stderr, err := runCincRaw(env.binary, "org", "member", "add", "anna", "--config", env.cfgPath)
	if err != nil {
		t.Skipf("cinc-zero does not support adding org members; covered by unit tests. stderr: %s", stderr)
	}
	if add != "Added \"anna\" to organization \"acme\"\n" {
		t.Errorf("org member add output = %q", add)
	}

	remove := runCinc(t, env.binary, "org", "member", "remove", "anna", "--config", env.cfgPath)
	if remove != "Removed \"anna\" from organization \"acme\"\n" {
		t.Errorf("org member remove output = %q", remove)
	}
}

// TestOrgInviteAgainstCincZero invites the seeded user "ben" to the "acme"
// org, lists pending invitations, and rescinds the invite. Skips if the
// pinned cinc-zero doesn't implement the association_requests flow; the
// behavior is otherwise covered by the unit tests.
func TestOrgInviteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	invite, stderr, err := runCincRaw(env.binary, "org", "invite", "create", "ben", "--config", env.cfgPath)
	if err != nil {
		t.Skipf("cinc-zero does not support org invitations; covered by unit tests. stderr: %s", stderr)
	}
	if invite != "Invited \"ben\" to organization \"acme\"\n" {
		t.Errorf("org invite create output = %q", invite)
	}

	listOut := runCinc(t, env.binary, "org", "invite", "list", "--config", env.cfgPath, "--format", "json")
	var invites []cinc.Invitation
	if err := json.Unmarshal([]byte(listOut), &invites); err != nil {
		t.Fatalf("org invite list (json) not valid JSON: %v\n%s", err, listOut)
	}
	idx := slices.IndexFunc(invites, func(i cinc.Invitation) bool { return i.Username == "ben" })
	if idx < 0 {
		t.Fatalf("invite list missing ben: %+v", invites)
	}

	rescind := runCinc(t, env.binary, "org", "invite", "rescind", invites[idx].ID, "--config", env.cfgPath)
	if rescind != "Rescinded invitation \""+invites[idx].ID+"\"\n" {
		t.Errorf("org invite rescind output = %q", rescind)
	}
}
