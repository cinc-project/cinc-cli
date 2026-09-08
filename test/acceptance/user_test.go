//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestUserListAgainstCincZero verifies that `cinc user list` against a
// real cinc-zero server returns the seeded global users (alongside the
// default "pivotal" superuser) in both human and JSON output formats.
func TestUserListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "user", "list", "--config", env.cfgPath)
	for _, name := range []string{"anna", "ben"} {
		if !strings.Contains(human, name) {
			t.Errorf("user list (human) missing %q\ngot: %s", name, human)
		}
	}

	jsonOut := runCinc(t, env.binary, "user", "list", "--config", env.cfgPath, "--format", "json")
	for _, name := range []string{"anna", "ben"} {
		if !strings.Contains(jsonOut, name) {
			t.Errorf("user list (json) missing %q\ngot: %s", name, jsonOut)
		}
	}
}

// TestUserCreateDeleteAgainstCincZero creates a user (capturing the
// server-generated key), confirms it in the list, then deletes it.
func TestUserCreateDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "user", "create", "carol",
		"--email", "carol@example.test", "--display-name", "Carol Carter",
		"--first-name", "Carol", "--last-name", "Carter",
		"--config", env.cfgPath)
	if !strings.Contains(out, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("user create did not stream a private key:\n%s", out)
	}

	list := runCinc(t, env.binary, "user", "list", "--config", env.cfgPath)
	if !strings.Contains(list, "carol") {
		t.Errorf("user list after create missing carol:\n%s", list)
	}

	del := runCinc(t, env.binary, "user", "delete", "carol", "--config", env.cfgPath)
	if del != "Deleted user \"carol\"\n" {
		t.Errorf("user delete output = %q", del)
	}

	after := runCinc(t, env.binary, "user", "list", "--config", env.cfgPath)
	if strings.Contains(after, "carol") {
		t.Errorf("user list after delete still has carol:\n%s", after)
	}
}

// TestUserDeletePivotalDeclinedAgainstCincZero confirms that deleting the
// "pivotal" superuser warns and, with no answer on stdin (the No default),
// aborts cleanly without touching the server.
//
// We exercise only the decline path here: actually deleting pivotal would
// remove the very superuser this harness signs its requests as, breaking
// the rest of the session. The --yes happy path is covered by the unit
// test TestUserDeletePivotalForced.
func TestUserDeletePivotalDeclinedAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	stdout, stderr, err := runCincRaw(env.binary, "user", "delete", "pivotal", "--config", env.cfgPath)
	if err != nil {
		t.Fatalf("declining the pivotal delete should exit cleanly, got %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "superuser") {
		t.Errorf("expected a superuser warning on stderr, got %q", stderr)
	}
	if stdout != "" {
		t.Errorf("expected no success output when declining, got %q", stdout)
	}

	list := runCinc(t, env.binary, "user", "list", "--config", env.cfgPath)
	if !strings.Contains(list, "pivotal") {
		t.Errorf("pivotal should still exist after declining the delete:\n%s", list)
	}
}

// TestUserPasswordAgainstCincZero sets a seeded user's password.
func TestUserPasswordAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "user", "password", "anna", "--password", "n3w-s3cret!", "--config", env.cfgPath)
	if out != "Updated password for user \"anna\"\n" {
		t.Errorf("user password output = %q", out)
	}
}

// TestUserShowAgainstCincZero fetches a seeded user and asserts on both
// the default (pretty JSON) and `--format json` outputs.
func TestUserShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "user", "show", "anna", "--config", env.cfgPath)
	if !strings.Contains(human, "anna") {
		t.Errorf("user show (human) missing username:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "user", "show", "anna", "--config", env.cfgPath, "--format", "json")
	var got cinc.User
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("user show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.UserName != "anna" {
		t.Errorf("user show returned username=%q, want anna", got.UserName)
	}
}

// TestUserEditAgainstCincZero edits a seeded global user through --file
// and confirms the change is persisted.
func TestUserEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.User{
		UserName:    "ignored",
		DisplayName: "Anna Operator",
		Email:       "anna@example.test",
		FirstName:   "Anna",
		LastName:    "Admin",
	})
	out := runCinc(t, env.binary, "user", "edit", "anna", "--file", file, "--config", env.cfgPath)
	if out != "Updated user \"anna\"\n" {
		t.Errorf("user edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "user", "show", "anna", "--config", env.cfgPath, "--format", "json")
	var got cinc.User
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("user show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.UserName != "anna" || got.DisplayName != "Anna Operator" {
		t.Errorf("edited user = %+v, want username=anna display_name=Anna Operator", got)
	}
}
