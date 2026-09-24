package suite

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

var roleFamily = family{cases: []testCase{
	{"roles/lifecycle", []string{"role create", "role show", "role list", "role edit", "role delete"}, testRoleLifecycle},
	{"roles/create-from-file", []string{"role create", "role show"}, testRoleCreateFromFile},
	{"roles/create-args-override-file", []string{"role create"}, testRoleCreateArgsOverrideFile},
	{"roles/edit-replaces", []string{"role edit"}, testRoleEditReplaces},
	{"roles/edit-pins-name", []string{"role edit"}, testRoleEditPinsName},
	{"roles/not-found", []string{"role show", "role delete"}, testRoleNotFound},
	{"roles/edit-missing", []string{"role edit"}, testRoleEditMissing},
	{"roles/already-exists", []string{"role create"}, testRoleAlreadyExists},
	{"roles/invalid-name", []string{"role create"}, testRoleInvalidName},
	{"roles/invalid-run-list", []string{"role create", "role edit"}, testRoleInvalidRunList},
	{"roles/bad-file", []string{"role create", "role edit"}, testRoleBadFile},
	{"roles/edit-needs-terminal", []string{"role edit"}, testRoleEditNeedsTerminal},
	{"roles/forbidden", []string{"role list", "role show", "role create", "role edit", "role delete"}, testRoleForbidden},
	{"roles/org-isolation", []string{"role list", "role show"}, testRoleOrgIsolation},
}}

func testRoleLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	out := c.run("role", "create", name, "--description", "App tier")
	c.cleanup("role", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created role %q\n", name))

	r := showRole(c, name)
	wantEqual(t, "name", r.Name, name)
	wantEqual(t, "description", r.Description, "App tier")
	wantSlice(t, "run list", r.RunList, []string{})

	// The human form of role show is the role as indented JSON.
	human := c.run("role", "show", name)
	for _, want := range []string{fmt.Sprintf("%q: %q", "name", name), `"description": "App tier"`} {
		if !strings.Contains(human, want) {
			t.Errorf("role show missing %s:\n%s", want, human)
		}
	}

	if list := strings.Fields(c.run("role", "list")); !slices.Contains(list, name) {
		t.Errorf("role list does not include %s: %v", name, list)
	}
	if !listed(c, name, "role", "list") {
		t.Errorf("role list --format json does not include %s", name)
	}

	// The name in the file is ignored: the argument names the role to edit.
	file := writeJSON(t, cinc.Role{Name: "ignored", Description: "edited via cinc", RunList: []string{"recipe[base]"}})
	wantEqual(t, "edit output", c.run("role", "edit", name, "--file", file), fmt.Sprintf("Updated role %q\n", name))
	r = showRole(c, name)
	wantEqual(t, "name after edit", r.Name, name)
	wantEqual(t, "description after edit", r.Description, "edited via cinc")
	wantSlice(t, "run list after edit", r.RunList, []string{"recipe[base]"})

	wantEqual(t, "delete output", c.run("role", "delete", name), fmt.Sprintf("Deleted role %q\n", name))
	wantNotFound(t, c.fail("role", "show", name))
	if listed(c, name, "role", "list") {
		t.Errorf("role list still includes deleted %s", name)
	}
}

// testRoleCreateFromFile creates a role from a full document, including the
// per-environment run lists and attributes the flags cannot set, and checks
// the server kept every part of it.
func testRoleCreateFromFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	env := uniqueName(t, "env")
	createEnvironment(c, env)
	createRoleFromFile(c, name, map[string]any{
		"name":                name,
		"description":         "from a file",
		"json_class":          "Chef::Role",
		"chef_type":           "role",
		"run_list":            []string{"recipe[base]", "role[common]"},
		"env_run_lists":       map[string][]string{env: {"recipe[base]", "recipe[prod::tuning]"}},
		"default_attributes":  map[string]any{"app": map[string]any{"port": 8080}},
		"override_attributes": map[string]any{"app": map[string]any{"debug": false}},
	})

	r := showRole(c, name)
	wantEqual(t, "description", r.Description, "from a file")
	wantSlice(t, "run list", r.RunList, []string{"recipe[base]", "role[common]"})
	wantSlice(t, "env run list", r.EnvRunLists[env], []string{"recipe[base]", "recipe[prod::tuning]"})
	app, _ := r.DefaultAttributes["app"].(map[string]any)
	if app["port"] != float64(8080) {
		t.Errorf("default_attributes.app.port = %v, want 8080 (role: %+v)", app["port"], r)
	}
	app, _ = r.OverrideAttributes["app"].(map[string]any)
	if v, ok := app["debug"]; !ok || v != false {
		t.Errorf("override_attributes.app.debug = %v, want false (role: %+v)", v, r)
	}
}

// testRoleCreateArgsOverrideFile checks that the positional name and
// --description win over what the file says, so `role create staging --file
// prod.json` can never land in the wrong slot.
func testRoleCreateArgsOverrideFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	other := uniqueName(t, "role")
	c.cleanup("role", "delete", other)
	file := writeJSON(t, cinc.Role{Name: other, Description: "from the file", RunList: []string{"recipe[base]"}})
	c.run("role", "create", name, "--file", file, "--description", "from the flag")
	c.cleanup("role", "delete", name)

	r := showRole(c, name)
	wantEqual(t, "description", r.Description, "from the flag")
	wantSlice(t, "run list", r.RunList, []string{"recipe[base]"})
	wantNotFound(t, c.fail("role", "show", other))
}

// testRoleEditReplaces checks that edit --file replaces the whole role, as a
// PUT does on erchef: parts the file leaves out are cleared, not kept.
func testRoleEditReplaces(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRoleFromFile(c, name, map[string]any{
		"name":               name,
		"description":        "before",
		"run_list":           []string{"recipe[base]"},
		"env_run_lists":      map[string][]string{"_default": {"recipe[base]"}},
		"default_attributes": map[string]any{"keep": "me"},
	})

	c.run("role", "edit", name, "--file", writeJSON(t, map[string]any{"description": "after"}))
	r := showRole(c, name)
	wantEqual(t, "description", r.Description, "after")
	wantSlice(t, "run list", r.RunList, []string{})
	if len(r.DefaultAttributes) != 0 || len(r.EnvRunLists) != 0 {
		t.Errorf("edit should replace the role, but kept default_attributes %v and env_run_lists %v",
			r.DefaultAttributes, r.EnvRunLists)
	}
}

// testRoleEditPinsName checks that a file naming a different role neither
// renames the edited role nor touches the other one.
func testRoleEditPinsName(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	other := uniqueName(t, "role")
	createRole(c, name)
	createRole(c, other, "--description", "untouched")

	c.run("role", "edit", name, "--file", writeJSON(t, cinc.Role{Name: other, Description: "edited"}))
	wantEqual(t, "edited role", showRole(c, name).Description, "edited")
	wantEqual(t, "other role", showRole(c, other).Description, "untouched")
}

func testRoleNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("role", "show", ghost))
	wantNotFound(t, c.fail("role", "show", ghost, "--format", "json"))
	wantNotFound(t, c.fail("role", "delete", ghost))
}

// testRoleEditMissing checks that editing a role that does not exist fails
// and does not create it.
func testRoleEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("role", "delete", ghost)
	wantNotFound(t, c.fail("role", "edit", ghost, "--file", writeJSON(t, cinc.Role{Description: "nope"})))
	wantNotFound(t, c.fail("role", "show", ghost))
}

// testRoleAlreadyExists checks the 409 and that the existing role is left as
// it was.
func testRoleAlreadyExists(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name, "--description", "original")
	wantConflict(t, c.fail("role", "create", name, "--description", "dup"))
	wantEqual(t, "description", showRole(c, name).Description, "original")
}

// testRoleInvalidName checks that a name erchef's name rule refuses (it
// allows letters, digits, _, - and . only) is reported back as a 400, and
// nothing is created.
func testRoleInvalidName(t *testing.T, _ Target, c *cli) {
	bad := uniqueName(t, "role") + "!bad"
	c.cleanup("role", "delete", bad)
	wantBadRequest(t, c.fail("role", "create", bad))
	if listed(c, bad, "role", "list") {
		t.Errorf("role list includes %s, which the server should have refused", bad)
	}
}

// testRoleInvalidRunList checks that erchef rejects a run list entry that is
// neither recipe[...] nor role[...], on create and on edit.
func testRoleInvalidRunList(t *testing.T, _ Target, c *cli) {
	bad := uniqueName(t, "role")
	c.cleanup("role", "delete", bad)
	wantBadRequest(t, c.fail("role", "create", bad, "--file",
		writeJSON(t, cinc.Role{RunList: []string{"not a run list item!"}})))
	wantNotFound(t, c.fail("role", "show", bad))

	name := uniqueName(t, "role")
	createRole(c, name, "--description", "valid")
	wantBadRequest(t, c.fail("role", "edit", name, "--file",
		writeJSON(t, cinc.Role{RunList: []string{"not a run list item!"}})))
	wantSlice(t, "run list after refused edit", showRole(c, name).RunList, []string{})
}

// testRoleBadFile checks that an unreadable or malformed --file fails before
// anything reaches the server.
func testRoleBadFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	c.cleanup("role", "delete", name)
	missing := filepath.Join(t.TempDir(), "missing.json")
	malformed := filepath.Join(t.TempDir(), "bad.json")
	writeFile(t, malformed, `{"name": `)

	wantStderr(t, c.fail("role", "create", name, "--file", missing), "missing.json")
	wantStderr(t, c.fail("role", "create", name, "--file", malformed), "bad.json")
	wantNotFound(t, c.fail("role", "show", name))

	createRole(c, name, "--description", "original")
	wantStderr(t, c.fail("role", "edit", name, "--file", malformed), "bad.json")
	wantEqual(t, "description", showRole(c, name).Description, "original")
}

// testRoleEditNeedsTerminal runs edit without --file where there is no
// terminal for the editor (a script, cron, CI): it must fail at once, say how
// to edit without one, and leave the role alone.
func testRoleEditNeedsTerminal(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name, "--description", "original")
	wantStderr(t, c.execDetached("role", "edit", name), "--file")
	wantEqual(t, "description", showRole(c, name).Description, "original")
}

// testRoleForbidden acts as an ordinary API client, which may read roles but
// not create, change or delete them.
func testRoleForbidden(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name, "--description", "original")
	addClientActor(c, "robot")
	as := func(args ...string) []string { return append(args, "--profile", "robot") }

	if !listed(c, name, as("role", "list")...) {
		t.Errorf("a client should be able to list roles and see %s", name)
	}
	var r cinc.Role
	c.json(&r, as("role", "show", name)...)
	wantEqual(t, "role shown to the client", r.Name, name)

	denied := uniqueName(t, "role")
	c.cleanup("role", "delete", denied)
	wantForbidden(t, c.fail(as("role", "create", denied)...))
	wantNotFound(t, c.fail("role", "show", denied))

	wantForbidden(t, c.fail(as("role", "edit", name, "--file", writeJSON(t, cinc.Role{Description: "hijacked"}))...))
	wantForbidden(t, c.fail(as("role", "delete", name)...))
	wantEqual(t, "description after refused edit and delete", showRole(c, name).Description, "original")
}

// testRoleOrgIsolation checks that a role lives in one organization only.
func testRoleOrgIsolation(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name)
	if listed(c, name, "role", "list", "--profile", "other") {
		t.Errorf("role %s created in %s is listed in %s", name, c.tgt.Org, c.tgt.OtherOrg)
	}
	wantNotFound(t, c.fail("role", "show", name, "--profile", "other"))
}
