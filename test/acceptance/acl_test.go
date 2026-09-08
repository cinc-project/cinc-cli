//go:build acceptance

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// ACLs are exposed per-noun as an `acl` subgroup (e.g. `cinc node acl show`).
// The acceptance harness signs as the pivotal superuser, which holds grant on
// every object, so the read-modify-write flow is exercisable here against the
// seeded "acme" org.
//
// Where the pinned cinc-zero build doesn't implement an _acl endpoint, the
// test skips with a documented reason and the behavior stays covered by the
// unit tests in apps/cinc/cmd/acl_test.go (which assert the exact GET-then-PUT
// wiring, the merged ACE body, `grant all`, the no-op path, and the org/user
// special endpoints).

// TestNodeACLShowAgainstCincZero fetches the ACL of a seeded node and asserts
// it parses into the five-permission shape.
func TestNodeACLShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out, stderr, err := runCincRaw(env.binary, "node", "acl", "show", "web01",
		"--config", env.cfgPath, "--format", "json")
	if err != nil {
		t.Skipf("cinc-zero does not serve node ACLs; covered by unit tests. stderr: %s", stderr)
	}
	var acl cinc.ACL
	if err := json.Unmarshal([]byte(out), &acl); err != nil {
		t.Fatalf("node acl show (json) not valid JSON: %v\n%s", err, out)
	}
	// A real ACL always grants read to at least one group or actor.
	if len(acl.Read.Groups) == 0 && len(acl.Read.Actors) == 0 {
		t.Errorf("node web01 read ACE is empty, which is implausible for a seeded node:\n%s", out)
	}
}

// TestNodeACLGrantRevokeRoundTripAgainstCincZero grants the seeded "devs"
// group read on a node, confirms it via show, then revokes it and confirms it
// is gone. Skips if the pinned cinc-zero rejects the ACL GET or PUT.
func TestNodeACLGrantRevokeRoundTripAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	grant, stderr, err := runCincRaw(env.binary, "node", "acl", "grant", "read", "web01",
		"--group", "devs", "--config", env.cfgPath)
	if err != nil {
		t.Skipf("cinc-zero does not support setting node ACLs; covered by unit tests. stderr: %s", stderr)
	}
	if !strings.Contains(grant, "devs") {
		t.Errorf("node acl grant output should mention devs:\n%s", grant)
	}

	afterGrant := readNodeACL(t, env, "web01")
	if !slices.Contains(afterGrant.Read.Groups, "devs") {
		t.Errorf("after grant, read groups = %v, want it to contain devs", afterGrant.Read.Groups)
	}

	revoke := runCinc(t, env.binary, "node", "acl", "revoke", "read", "web01",
		"--group", "devs", "--config", env.cfgPath)
	if !strings.Contains(revoke, "devs") {
		t.Errorf("node acl revoke output should mention devs:\n%s", revoke)
	}

	afterRevoke := readNodeACL(t, env, "web01")
	if slices.Contains(afterRevoke.Read.Groups, "devs") {
		t.Errorf("after revoke, read groups = %v, want devs removed", afterRevoke.Read.Groups)
	}
}

// TestNodeACLGrantNoOpAgainstCincZero re-grants an existing member and expects
// the friendly no-op message rather than an error.
func TestNodeACLGrantNoOpAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	acl := readNodeACLOrSkip(t, env, "web01")
	if len(acl.Read.Groups) == 0 {
		t.Skip("seeded node has no read groups to re-grant; covered by unit tests")
	}
	existing := acl.Read.Groups[0]

	out := runCinc(t, env.binary, "node", "acl", "grant", "read", "web01",
		"--group", existing, "--config", env.cfgPath)
	if !strings.Contains(strings.ToLower(out), "no change") {
		t.Errorf("re-granting %q should be a friendly no-op:\n%s", existing, out)
	}
}

// TestOrgACLShowAgainstCincZero fetches the organization's own ACL (the
// nameless /organizations/acme/_acl endpoint).
func TestOrgACLShowAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	out, stderr, err := runCincRaw(env.binary, "org", "acl", "show",
		"--config", env.cfgPath, "--format", "json")
	if err != nil {
		t.Skipf("cinc-zero does not serve org ACLs; covered by unit tests. stderr: %s", stderr)
	}
	var acl cinc.ACL
	if err := json.Unmarshal([]byte(out), &acl); err != nil {
		t.Fatalf("org acl show (json) not valid JSON: %v\n%s", err, out)
	}
}

// TestObjectACLRoundTripAgainstCincZero exercises the read-modify-write ACL
// flow (`acl show` → `acl grant read` → `acl show` → `acl revoke read` →
// `acl show`) for every noun that exposes an `acl` subgroup, in one
// table-driven pass. It covers the normal org-scoped object ACLs
// (client/cookbook/databag/environment/group/policy/policy-group/role), the
// nameless organization ACL (`org acl`), and the global user ACL
// (`user acl`, served at /users/<name>/_acl).
//
// Each noun targets a seeded object: the harness seeds the chef-repo and the
// global users + "devs" group, so every object below already exists. Object
// and org ACLs grant the seeded org group "devs" (which lands in the ACE's
// group list); the global user ACL grants the global user "ben" (an actor,
// since the org-scoped group has no meaning at the server root).
//
// A noun whose _acl endpoint the pinned cinc-zero doesn't serve skips that
// subtest with a documented reason; the exact GET-then-PUT wiring stays
// covered by the unit tests in apps/cinc/cmd/acl_test.go.
func TestObjectACLRoundTripAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	cases := []struct {
		noun       string // command noun, e.g. "policy-group"
		objectName string // seeded object; "" for the nameless org ACL
		memberFlag string // "--group" or "--user"
		member     string // the member to grant then revoke
		inGroups   bool   // true if member lands in the ACE group list, false for actors
	}{
		{"client", "worker-01", "--group", "devs", true},
		{"cookbook", "webserver", "--group", "devs", true},
		{"databag", "users", "--group", "devs", true},
		{"environment", "prod", "--group", "devs", true},
		{"group", "devs", "--group", "devs", true},
		{"policy", "appserver", "--group", "devs", true},
		{"policy-group", "prod", "--group", "devs", true},
		{"role", "web", "--group", "devs", true},
		{"org", "", "--group", "devs", true},
		{"user", "anna", "--user", "ben", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.noun, func(t *testing.T) {
			// showArgs / changeArgs adapt to whether the scope takes a <name>.
			showArgs := []string{tc.noun, "acl", "show"}
			if tc.objectName != "" {
				showArgs = append(showArgs, tc.objectName)
			}
			showArgs = append(showArgs, "--config", env.cfgPath, "--format", "json")

			readACL := func() cinc.ACL {
				t.Helper()
				out := runCinc(t, env.binary, showArgs...)
				var acl cinc.ACL
				if err := json.Unmarshal([]byte(out), &acl); err != nil {
					t.Fatalf("%s acl show (json) not valid JSON: %v\n%s", tc.noun, err, out)
				}
				return acl
			}
			members := func(acl cinc.ACL) []string {
				if tc.inGroups {
					return acl.Read.Groups
				}
				return acl.Read.Actors
			}

			// Probe the endpoint with a raw show so an unsupported _acl path
			// skips this noun rather than failing the whole table.
			if _, stderr, err := runCincRaw(env.binary, showArgs...); err != nil {
				t.Skipf("cinc-zero does not serve %s ACLs; covered by unit tests. stderr: %s", tc.noun, stderr)
			}

			changeArgs := func(verb string) []string {
				args := []string{tc.noun, "acl", verb, "read"}
				if tc.objectName != "" {
					args = append(args, tc.objectName)
				}
				return append(args, tc.memberFlag, tc.member, "--config", env.cfgPath)
			}

			grant := runCinc(t, env.binary, changeArgs("grant")...)
			if !strings.Contains(grant, tc.member) {
				t.Errorf("%s acl grant output should mention %q:\n%s", tc.noun, tc.member, grant)
			}
			if !slices.Contains(members(readACL()), tc.member) {
				t.Errorf("after grant, %s read members = %v, want it to contain %q",
					tc.noun, members(readACL()), tc.member)
			}

			revoke := runCinc(t, env.binary, changeArgs("revoke")...)
			if !strings.Contains(revoke, tc.member) {
				t.Errorf("%s acl revoke output should mention %q:\n%s", tc.noun, tc.member, revoke)
			}
			if slices.Contains(members(readACL()), tc.member) {
				t.Errorf("after revoke, %s read members = %v, want %q removed",
					tc.noun, members(readACL()), tc.member)
			}
		})
	}
}

func readNodeACL(t *testing.T, env acceptanceEnv, name string) cinc.ACL {
	t.Helper()
	out := runCinc(t, env.binary, "node", "acl", "show", name, "--config", env.cfgPath, "--format", "json")
	var acl cinc.ACL
	if err := json.Unmarshal([]byte(out), &acl); err != nil {
		t.Fatalf("node acl show (json) not valid JSON: %v\n%s", err, out)
	}
	return acl
}

func readNodeACLOrSkip(t *testing.T, env acceptanceEnv, name string) cinc.ACL {
	t.Helper()
	out, stderr, err := runCincRaw(env.binary, "node", "acl", "show", name, "--config", env.cfgPath, "--format", "json")
	if err != nil {
		t.Skipf("cinc-zero does not serve node ACLs; covered by unit tests. stderr: %s", stderr)
	}
	var acl cinc.ACL
	if err := json.Unmarshal([]byte(out), &acl); err != nil {
		t.Fatalf("node acl show (json) not valid JSON: %v\n%s", err, out)
	}
	return acl
}
