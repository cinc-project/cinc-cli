//go:build acceptance

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestEnvironmentListAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	// cinc-zero auto-creates the "_default" environment in every org;
	// the seed adds "prod" and "staging" on top.
	human := runCinc(t, env.binary, "environment", "list", "--config", env.cfgPath)
	if human != "_default\nprod\nstaging\n" {
		t.Errorf("environment list (human) = %q", human)
	}

	jsonOut := runCinc(t, env.binary, "environment", "list", "--config", env.cfgPath, "--format", "json")
	for _, name := range []string{"_default", "prod", "staging"} {
		if !strings.Contains(jsonOut, name) {
			t.Errorf("environment list (json) missing %q\ngot: %s", name, jsonOut)
		}
	}
}

// TestEnvironmentShowAgainstCincZero fetches a seeded environment and
// asserts on both the default (pretty JSON) and `--format json` outputs.
func TestEnvironmentShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	human := runCinc(t, env.binary, "environment", "show", "prod", "--config", env.cfgPath)
	if !strings.Contains(human, "\"name\": \"prod\"") {
		t.Errorf("environment show (human) missing name field:\n%s", human)
	}

	jsonOut := runCinc(t, env.binary, "environment", "show", "prod", "--config", env.cfgPath, "--format", "json")
	var got cinc.Environment
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("environment show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "prod" {
		t.Errorf("environment show returned name=%q, want prod", got.Name)
	}
}

func TestEnvironmentCreateAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "environment", "create", "qa",
		"--description", "QA environment",
		"--config", env.cfgPath)
	if out != "Created environment \"qa\"\n" {
		t.Errorf("environment create output = %q", out)
	}

	after := runCinc(t, env.binary, "environment", "list", "--config", env.cfgPath)
	if after != "_default\nprod\nqa\nstaging\n" {
		t.Errorf("environment list after create = %q, want qa in the index", after)
	}
}

func TestEnvironmentDeleteAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out := runCinc(t, env.binary, "environment", "delete", "staging", "--config", env.cfgPath)
	if out != "Deleted environment \"staging\"\n" {
		t.Errorf("environment delete output = %q", out)
	}

	after := runCinc(t, env.binary, "environment", "list", "--config", env.cfgPath)
	if after != "_default\nprod\n" {
		t.Errorf("environment list after delete = %q, want staging absent", after)
	}
}

// TestEnvironmentEditAgainstCincZero edits a seeded environment through
// --file and confirms the change is persisted.
func TestEnvironmentEditAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	file := writeJSONFile(t, cinc.Environment{Name: "ignored", Description: "edited via cinc"})
	out := runCinc(t, env.binary, "environment", "edit", "staging", "--file", file, "--config", env.cfgPath)
	if out != "Updated environment \"staging\"\n" {
		t.Errorf("environment edit output = %q", out)
	}

	jsonOut := runCinc(t, env.binary, "environment", "show", "staging", "--config", env.cfgPath, "--format", "json")
	var got cinc.Environment
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("environment show (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if got.Name != "staging" || got.Description != "edited via cinc" {
		t.Errorf("edited environment = %+v, want name=staging description=edited via cinc", got)
	}
}
