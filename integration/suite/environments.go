package suite

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

var environmentFamily = family{cases: []testCase{
	{"environments/lifecycle", []string{"environment create", "environment show", "environment list", "environment edit", "environment delete"}, testEnvironmentLifecycle},
	{"environments/create-short-description", []string{"environment create"}, testEnvironmentCreateShortDescription},
	{"environments/create-from-file", []string{"environment create", "environment show"}, testEnvironmentCreateFromFile},
	{"environments/create-args-override-file", []string{"environment create"}, testEnvironmentCreateArgsOverrideFile},
	{"environments/edit-replaces", []string{"environment edit"}, testEnvironmentEditReplaces},
	{"environments/default", []string{"environment list", "environment show"}, testEnvironmentDefault},
	{"environments/default-read-only", []string{"environment edit", "environment delete"}, testEnvironmentDefaultReadOnly},
	{"environments/not-found", []string{"environment show", "environment delete"}, testEnvironmentNotFound},
	{"environments/edit-missing", []string{"environment edit"}, testEnvironmentEditMissing},
	{"environments/already-exists", []string{"environment create"}, testEnvironmentAlreadyExists},
	{"environments/invalid-name", []string{"environment create"}, testEnvironmentInvalidName},
	{"environments/invalid-cookbook-constraint", []string{"environment create", "environment edit"}, testEnvironmentInvalidConstraint},
	{"environments/bad-file", []string{"environment create", "environment edit"}, testEnvironmentBadFile},
	{"environments/edit-needs-terminal", []string{"environment edit"}, testEnvironmentEditNeedsTerminal},
	{"environments/forbidden", []string{"environment list", "environment show", "environment create", "environment edit", "environment delete"}, testEnvironmentForbidden},
	{"environments/org-isolation", []string{"environment create", "environment list"}, testEnvironmentOrgIsolation},
}}

func testEnvironmentLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	out := c.run("environment", "create", name, "--description", "QA environment")
	c.cleanup("environment", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created environment %q\n", name))

	e := showEnvironment(c, name)
	wantEqual(t, "name", e.Name, name)
	wantEqual(t, "description", e.Description, "QA environment")
	if len(e.CookbookVersions) != 0 {
		t.Errorf("new environment has cookbook_versions %v, want none", e.CookbookVersions)
	}

	// The human form of environment show is the environment as indented JSON.
	human := c.run("environment", "show", name)
	for _, want := range []string{fmt.Sprintf("%q: %q", "name", name), `"description": "QA environment"`} {
		if !strings.Contains(human, want) {
			t.Errorf("environment show missing %s:\n%s", want, human)
		}
	}

	if list := strings.Fields(c.run("environment", "list")); !slices.Contains(list, name) || !slices.Contains(list, "_default") {
		t.Errorf("environment list should include %s and _default: %v", name, list)
	}
	if !listed(c, name, "environment", "list") {
		t.Errorf("environment list --format json does not include %s", name)
	}

	file := writeJSON(t, cinc.Environment{
		Name:             "ignored",
		Description:      "edited via cinc",
		CookbookVersions: map[string]string{"apache2": "~> 1.2.0"},
	})
	wantEqual(t, "edit output", c.run("environment", "edit", name, "--file", file), fmt.Sprintf("Updated environment %q\n", name))
	e = showEnvironment(c, name)
	wantEqual(t, "name after edit", e.Name, name)
	wantEqual(t, "description after edit", e.Description, "edited via cinc")
	wantEqual(t, "apache2 constraint", e.CookbookVersions["apache2"], "~> 1.2.0")

	wantEqual(t, "delete output", c.run("environment", "delete", name), fmt.Sprintf("Deleted environment %q\n", name))
	wantNotFound(t, c.fail("environment", "show", name))
	if listed(c, name, "environment", "list") {
		t.Errorf("environment list still includes deleted %s", name)
	}
}

func testEnvironmentCreateShortDescription(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	c.run("environment", "create", name, "-d", "short flag")
	c.cleanup("environment", "delete", name)
	wantEqual(t, "description", showEnvironment(c, name).Description, "short flag")
}

// testEnvironmentCreateFromFile creates an environment with cookbook
// constraints in each operator form erchef accepts, plus default and
// override attributes, and checks the server kept them all.
func testEnvironmentCreateFromFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	constraints := map[string]string{
		"apache2": "~> 1.2.0",
		"nginx":   "= 2.0.0",
		"mysql":   ">= 5.0",
		"redis":   "< 3.0.0",
		"ntp":     "<= 1.0",
		"base":    "> 0.1",
	}
	createEnvironmentFromFile(c, name, map[string]any{
		"name":                name,
		"description":         "from a file",
		"json_class":          "Chef::Environment",
		"chef_type":           "environment",
		"cookbook_versions":   constraints,
		"default_attributes":  map[string]any{"app": map[string]any{"port": 8080}},
		"override_attributes": map[string]any{"app": map[string]any{"workers": 4}},
	})

	e := showEnvironment(c, name)
	wantEqual(t, "description", e.Description, "from a file")
	if !maps.Equal(e.CookbookVersions, constraints) {
		t.Errorf("cookbook_versions = %v, want %v", e.CookbookVersions, constraints)
	}
	app, _ := e.DefaultAttributes["app"].(map[string]any)
	if app["port"] != float64(8080) {
		t.Errorf("default_attributes.app.port = %v, want 8080 (environment: %+v)", app["port"], e)
	}
	app, _ = e.OverrideAttributes["app"].(map[string]any)
	if app["workers"] != float64(4) {
		t.Errorf("override_attributes.app.workers = %v, want 4 (environment: %+v)", app["workers"], e)
	}
}

// testEnvironmentCreateArgsOverrideFile checks that the positional name and
// --description win over what the file says.
func testEnvironmentCreateArgsOverrideFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	other := uniqueName(t, "env")
	c.cleanup("environment", "delete", other)
	file := writeJSON(t, cinc.Environment{Name: other, Description: "from the file",
		CookbookVersions: map[string]string{"nginx": "= 2.0.0"}})
	c.run("environment", "create", name, "--file", file, "--description", "from the flag")
	c.cleanup("environment", "delete", name)

	e := showEnvironment(c, name)
	wantEqual(t, "description", e.Description, "from the flag")
	wantEqual(t, "nginx constraint", e.CookbookVersions["nginx"], "= 2.0.0")
	wantNotFound(t, c.fail("environment", "show", other))
}

// testEnvironmentEditReplaces checks that edit --file replaces the whole
// environment: constraints and attributes the file leaves out are cleared.
func testEnvironmentEditReplaces(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	createEnvironmentFromFile(c, name, map[string]any{
		"name":               name,
		"description":        "before",
		"cookbook_versions":  map[string]string{"apache2": "~> 1.2.0"},
		"default_attributes": map[string]any{"keep": "me"},
	})

	c.run("environment", "edit", name, "--file", writeJSON(t, map[string]any{"description": "after"}))
	e := showEnvironment(c, name)
	wantEqual(t, "description", e.Description, "after")
	if len(e.CookbookVersions) != 0 || len(e.DefaultAttributes) != 0 {
		t.Errorf("edit should replace the environment, but kept cookbook_versions %v and default_attributes %v",
			e.CookbookVersions, e.DefaultAttributes)
	}
}

// testEnvironmentDefault checks the _default environment every organization
// starts with.
func testEnvironmentDefault(t *testing.T, _ Target, c *cli) {
	if !listed(c, "_default", "environment", "list") {
		t.Error("environment list does not include _default")
	}
	e := showEnvironment(c, "_default")
	wantEqual(t, "name", e.Name, "_default")
	wantEqual(t, "description", e.Description, "The default Chef environment")
	if len(e.CookbookVersions) != 0 {
		t.Errorf("_default has cookbook_versions %v, want none", e.CookbookVersions)
	}
}

// testEnvironmentDefaultReadOnly checks that erchef's refusal (405) to change
// or delete _default reaches the user with the server's reason, and that
// _default survives.
func testEnvironmentDefaultReadOnly(t *testing.T, _ Target, c *cli) {
	file := writeJSON(t, cinc.Environment{Description: "hijacked"})
	wantStderr(t, c.fail("environment", "edit", "_default", "--file", file), "405", "cannot be modified")
	wantStderr(t, c.fail("environment", "delete", "_default"), "405", "cannot be deleted")
	wantEqual(t, "description", showEnvironment(c, "_default").Description, "The default Chef environment")
}

func testEnvironmentNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("environment", "show", ghost))
	wantNotFound(t, c.fail("environment", "show", ghost, "--format", "json"))
	wantNotFound(t, c.fail("environment", "delete", ghost))
}

// testEnvironmentEditMissing checks that editing an environment that does not
// exist fails and does not create it.
func testEnvironmentEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("environment", "delete", ghost)
	wantNotFound(t, c.fail("environment", "edit", ghost, "--file", writeJSON(t, cinc.Environment{Description: "nope"})))
	wantNotFound(t, c.fail("environment", "show", ghost))
}

func testEnvironmentAlreadyExists(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	c.run("environment", "create", name, "--description", "original")
	c.cleanup("environment", "delete", name)
	wantConflict(t, c.fail("environment", "create", name, "--description", "dup"))
	wantEqual(t, "description", showEnvironment(c, name).Description, "original")
}

func testEnvironmentInvalidName(t *testing.T, _ Target, c *cli) {
	bad := uniqueName(t, "env") + "!bad"
	c.cleanup("environment", "delete", bad)
	wantBadRequest(t, c.fail("environment", "create", bad))
	if listed(c, bad, "environment", "list") {
		t.Errorf("environment list includes %s, which the server should have refused", bad)
	}
}

// testEnvironmentInvalidConstraint checks that erchef rejects a cookbook
// version constraint it cannot parse, on create and on edit.
func testEnvironmentInvalidConstraint(t *testing.T, _ Target, c *cli) {
	bogus := map[string]string{"apache2": "not a constraint"}
	bad := uniqueName(t, "env")
	c.cleanup("environment", "delete", bad)
	wantBadRequest(t, c.fail("environment", "create", bad, "--file",
		writeJSON(t, cinc.Environment{CookbookVersions: bogus})))
	wantNotFound(t, c.fail("environment", "show", bad))

	name := uniqueName(t, "env")
	createEnvironment(c, name)
	wantBadRequest(t, c.fail("environment", "edit", name, "--file",
		writeJSON(t, cinc.Environment{CookbookVersions: bogus})))
	if cv := showEnvironment(c, name).CookbookVersions; len(cv) != 0 {
		t.Errorf("cookbook_versions after refused edit = %v, want none", cv)
	}
}

// testEnvironmentBadFile checks that an unreadable or malformed --file fails
// before anything reaches the server.
func testEnvironmentBadFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	c.cleanup("environment", "delete", name)
	missing := filepath.Join(t.TempDir(), "missing.json")
	malformed := filepath.Join(t.TempDir(), "bad.json")
	writeFile(t, malformed, `{"name": `)

	wantStderr(t, c.fail("environment", "create", name, "--file", missing), "missing.json")
	wantStderr(t, c.fail("environment", "create", name, "--file", malformed), "bad.json")
	wantNotFound(t, c.fail("environment", "show", name))

	c.run("environment", "create", name, "--description", "original")
	wantStderr(t, c.fail("environment", "edit", name, "--file", malformed), "bad.json")
	wantEqual(t, "description", showEnvironment(c, name).Description, "original")
}

// testEnvironmentEditNeedsTerminal runs edit without --file where there is no
// terminal for the editor: it must fail at once, say how to edit without
// one, and leave the environment alone.
func testEnvironmentEditNeedsTerminal(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	c.run("environment", "create", name, "--description", "original")
	c.cleanup("environment", "delete", name)
	wantStderr(t, c.execDetached("environment", "edit", name), "--file")
	wantEqual(t, "description", showEnvironment(c, name).Description, "original")
}

// testEnvironmentForbidden acts as an ordinary API client, which may read
// environments but not create, change or delete them.
func testEnvironmentForbidden(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	c.run("environment", "create", name, "--description", "original")
	c.cleanup("environment", "delete", name)
	addClientActor(c, "robot")
	as := func(args ...string) []string { return append(args, "--profile", "robot") }

	if !listed(c, name, as("environment", "list")...) {
		t.Errorf("a client should be able to list environments and see %s", name)
	}
	var e cinc.Environment
	c.json(&e, as("environment", "show", name)...)
	wantEqual(t, "environment shown to the client", e.Name, name)

	denied := uniqueName(t, "env")
	c.cleanup("environment", "delete", denied)
	wantForbidden(t, c.fail(as("environment", "create", denied, "--description", "should be forbidden")...))
	wantNotFound(t, c.fail("environment", "show", denied))

	wantForbidden(t, c.fail(as("environment", "edit", name, "--file", writeJSON(t, cinc.Environment{Description: "hijacked"}))...))
	wantForbidden(t, c.fail(as("environment", "delete", name)...))
	wantEqual(t, "description after refused edit and delete", showEnvironment(c, name).Description, "original")
}

// testEnvironmentOrgIsolation creates an environment in one organization and
// checks the other cannot see it.
func testEnvironmentOrgIsolation(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	createEnvironment(c, name)
	if listed(c, name, "environment", "list", "--profile", "other") {
		t.Errorf("environment %s created in %s is listed in %s", name, c.tgt.Org, c.tgt.OtherOrg)
	}
	if !listed(c, "_default", "environment", "list", "--profile", "other") {
		t.Errorf("%s should still have its own _default environment", c.tgt.OtherOrg)
	}
	if !listed(c, name, "environment", "list") {
		t.Errorf("%s lost its own environment %s", c.tgt.Org, name)
	}
}
