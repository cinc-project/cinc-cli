package suite

import (
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// Helpers shared by the role, environment and search families.

// createRole creates a role with the given extra flags and registers its
// cleanup.
func createRole(c *cli, name string, flags ...string) {
	c.t.Helper()
	c.run(append([]string{"role", "create", name}, flags...)...)
	c.cleanup("role", "delete", name)
}

// createRoleFromFile creates a role from a JSON document and registers its
// cleanup.
func createRoleFromFile(c *cli, name string, doc any) {
	c.t.Helper()
	c.run("role", "create", name, "--file", writeJSON(c.t, doc))
	c.cleanup("role", "delete", name)
}

func showRole(c *cli, name string) cinc.Role {
	c.t.Helper()
	var r cinc.Role
	c.json(&r, "role", "show", name)
	return r
}

// createEnvironmentFromFile creates an environment from a JSON document and
// registers its cleanup.
func createEnvironmentFromFile(c *cli, name string, doc any) {
	c.t.Helper()
	c.run("environment", "create", name, "--file", writeJSON(c.t, doc))
	c.cleanup("environment", "delete", name)
}

func showEnvironment(c *cli, name string) cinc.Environment {
	c.t.Helper()
	var e cinc.Environment
	c.json(&e, "environment", "show", name)
	return e
}

// addClientActor creates an ordinary (non-admin, non-validator) API client,
// registers its cleanup, and adds a credentials profile named profile that
// signs as it. On erchef a new client joins the org's clients group, which
// may read roles, environments and nodes but may not create, change or
// delete roles or environments.
func addClientActor(c *cli, profile string) string {
	c.t.Helper()
	name := uniqueName(c.t, "client")
	keyPath := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("client", "create", name, "--key-file", keyPath)
	c.cleanup("client", "delete", name)
	c.addProfile(profile, c.tgt.Org, name, keyPath)
	return name
}

// isForbidden reports whether a failed run is the server refusing the
// actor: a 403.
func isForbidden(r result) bool {
	low := strings.ToLower(r.stderr)
	return strings.Contains(r.stderr, "403") || strings.Contains(low, "forbidden") ||
		strings.Contains(low, "permission")
}

// wantForbidden fails the case unless r is a 403 refusal.
func wantForbidden(t *testing.T, r result) {
	t.Helper()
	if r.exitCode == 0 || !isForbidden(r) {
		t.Fatalf("want a 403 forbidden error: %s", r)
	}
}

// wantConflict fails the case unless r is the CLI's already-exists error.
func wantConflict(t *testing.T, r result) {
	t.Helper()
	low := strings.ToLower(r.stderr)
	conflict := strings.Contains(low, "already exists") || strings.Contains(r.stderr, "409")
	if r.exitCode == 0 || !conflict {
		t.Fatalf("want an already-exists error: %s", r)
	}
}

// wantBadRequest fails the case unless r is the server rejecting the
// request body or query: a 400.
func wantBadRequest(t *testing.T, r result) {
	t.Helper()
	if r.exitCode == 0 || !strings.Contains(r.stderr, "400") {
		t.Fatalf("want a 400 bad request error: %s", r)
	}
}

// wantStderr fails the case unless r failed and its stderr contains every
// one of want, compared case-insensitively.
func wantStderr(t *testing.T, r result, want ...string) {
	t.Helper()
	low := strings.ToLower(r.stderr)
	for _, w := range want {
		if r.exitCode == 0 || !strings.Contains(low, strings.ToLower(w)) {
			t.Fatalf("want a failure mentioning %q: %s", w, r)
		}
	}
}

// listed runs a list command with --format json and reports whether name is
// in the result.
func listed(c *cli, name string, args ...string) bool {
	c.t.Helper()
	var names []string
	c.json(&names, args...)
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
