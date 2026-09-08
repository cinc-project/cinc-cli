//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestGroupListAgainstCincZero verifies that `cinc group list` against
// a real cinc-zero server returns the default per-org groups in both
// human and JSON output formats.
func TestGroupListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "group", "list", "--config", env.cfgPath)
	for _, name := range []string{"admins", "clients", "users"} {
		if !strings.Contains(human, name) {
			t.Errorf("group list (human) missing %q\ngot: %s", name, human)
		}
	}

	jsonOut := runCinc(t, env.binary, "group", "list", "--config", env.cfgPath, "--format", "json")
	if !strings.Contains(jsonOut, "admins") {
		t.Errorf("group list (json) missing admins\ngot: %s", jsonOut)
	}
}

// TestGroupCreateAgainstCincZero creates a group and confirms it shows
// up in a follow-up list.
func TestGroupCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "group", "create", "team", "--config", env.cfgPath)
	if out != "Created group \"team\"\n" {
		t.Errorf("group create output = %q", out)
	}

	list := runCinc(t, env.binary, "group", "list", "--config", env.cfgPath)
	if !strings.Contains(list, "team") {
		t.Errorf("group list after create missing team:\n%s", list)
	}
}

// TestGroupDeleteAgainstCincZero deletes the seeded "devs" group and
// confirms a follow-up list no longer includes it.
func TestGroupDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	del := runCinc(t, env.binary, "group", "delete", "devs", "--config", env.cfgPath)
	if del != "Deleted group \"devs\"\n" {
		t.Errorf("group delete output = %q", del)
	}

	after := runCinc(t, env.binary, "group", "list", "--config", env.cfgPath)
	if strings.Contains(after, "devs") {
		t.Errorf("group list after delete still has devs:\n%s", after)
	}
}

// TestGroupMemberAgainstCincZero adds two seeded users to the seeded
// "devs" group, removes one, and asserts the membership at each step.
func TestGroupMemberAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	add := runCinc(t, env.binary, "group", "member", "add", "devs", "anna", "ben", "--config", env.cfgPath)
	if add != "Added anna, ben to group \"devs\"\n" {
		t.Errorf("group member add output = %q", add)
	}

	show := runCinc(t, env.binary, "group", "show", "devs", "--config", env.cfgPath)
	for _, name := range []string{"anna", "ben"} {
		if !strings.Contains(show, name) {
			t.Errorf("group show after add missing %q:\n%s", name, show)
		}
	}

	rm := runCinc(t, env.binary, "group", "member", "remove", "devs", "ben", "--config", env.cfgPath)
	if rm != "Removed ben from group \"devs\"\n" {
		t.Errorf("group member remove output = %q", rm)
	}

	after := runCinc(t, env.binary, "group", "show", "devs", "--config", env.cfgPath)
	if !strings.Contains(after, "anna") {
		t.Errorf("group show after remove dropped anna:\n%s", after)
	}
	if strings.Contains(after, "ben") {
		t.Errorf("group show after remove still has ben:\n%s", after)
	}
}

// TestGroupShowAgainstCincZero fetches the default "admins" group and
// asserts on both the default (pretty JSON) and `--format json`
// outputs.
func TestGroupShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "group", "show", "admins", "--config", env.cfgPath)
	if !strings.Contains(human, "admins") {
		t.Errorf("group show (human) missing groupname:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "group", "show", "admins", "--config", env.cfgPath, "--format", "json")
	var got cinc.Group
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("group show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.GroupName != "admins" {
		t.Errorf("group show returned groupname=%q, want admins", got.GroupName)
	}
}

// TestGroupEditAgainstCincZero edits the seeded "devs" group through
// --file, setting its membership, and confirms it is persisted.
func TestGroupEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.Group{GroupName: "ignored", Users: []string{"anna"}})
	out := runCinc(t, env.binary, "group", "edit", "devs", "--file", file, "--config", env.cfgPath)
	if out != "Updated group \"devs\"\n" {
		t.Errorf("group edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "group", "show", "devs", "--config", env.cfgPath, "--format", "json")
	var got cinc.Group
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("group show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if !contains(got.Users, "anna") {
		t.Errorf("edited group users = %v, want anna present", got.Users)
	}
}
