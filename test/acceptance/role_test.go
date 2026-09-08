//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestRoleListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "role", "list", "--config", env.cfgPath)
	if human != "base\ndb\nweb\n" {
		t.Errorf("role list (human) = %q, want sorted role names", human)
	}

	jsonOut := runCinc(t, env.binary, "role", "list", "--config", env.cfgPath, "--format", "json")
	for _, name := range []string{"base", "db", "web"} {
		if !strings.Contains(jsonOut, name) {
			t.Errorf("role list (json) missing %q\ngot: %s", name, jsonOut)
		}
	}
}

// TestRoleShowAgainstCincZero fetches a seeded role and asserts on both
// the default (pretty JSON) and `--format json` outputs.
func TestRoleShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "role", "show", "web", "--config", env.cfgPath)
	if !strings.Contains(human, "\"name\": \"web\"") {
		t.Errorf("role show (human) missing name field:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "role", "show", "web", "--config", env.cfgPath, "--format", "json")
	var got cinc.Role
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("role show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "web" {
		t.Errorf("role show returned name=%q, want web", got.Name)
	}
}

func TestRoleDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "role", "delete", "web", "--config", env.cfgPath)
	if out != "Deleted role \"web\"\n" {
		t.Errorf("role delete output = %q", out)
	}

	after := runCinc(t, env.binary, "role", "list", "--config", env.cfgPath)
	if after != "base\ndb\n" {
		t.Errorf("role list after delete = %q, want web absent", after)
	}
}

// TestRoleCreateAgainstCincZero creates a role with a description and
// confirms it lands in the index with that description.
func TestRoleCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "role", "create", "app",
		"--description", "App tier", "--config", env.cfgPath)
	if out != "Created role \"app\"\n" {
		t.Errorf("role create output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "role", "show", "app", "--config", env.cfgPath, "--format", "json")
	var got cinc.Role
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("role show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "app" || got.Description != "App tier" {
		t.Errorf("created role = %+v, want name=app description=App tier", got)
	}
}

// TestRoleEditAgainstCincZero edits a seeded role through --file and
// confirms the change is persisted.
func TestRoleEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.Role{
		Name:        "ignored",
		Description: "edited via cinc",
		RunList:     []string{"recipe[base]"},
	})
	out := runCinc(t, env.binary, "role", "edit", "web", "--file", file, "--config", env.cfgPath)
	if out != "Updated role \"web\"\n" {
		t.Errorf("role edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "role", "show", "web", "--config", env.cfgPath, "--format", "json")
	var got cinc.Role
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("role show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "web" || got.Description != "edited via cinc" {
		t.Errorf("edited role = %+v, want name=web description=edited via cinc", got)
	}
}
