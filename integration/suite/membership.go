package suite

import (
	"sync"
	"testing"
)

// orgMembership serializes, across every case in the process, the steps that
// change who belongs to an org's groups.
//
// erchef does not add or remove one member of an org's "users" group: it
// reads the group, edits the member list and writes the whole list back to
// bifrost (oc_chef_group:update). When a parallel case deletes a member
// between that read and write, bifrost rejects the stale list and erchef
// answers 500 ({oc_chef_group,update,error_in_bifrost,[{error,"400",...}]}).
// Holding this lock around each such step keeps two of them from
// interleaving. Reads (member list, show) do not need it.
//
// Hold it around:
//   - accepting an invitation (the user joins the "users" group),
//   - `org member add` and `org member remove`,
//   - `user delete` of a user who is, or may be, an org member,
//   - `group member add/remove` and `group edit` on a group whose members
//     are users (admins, users, or a group containing them).
//
// Hold it only around the step itself, never for a whole case: every case
// that changes membership would otherwise run one at a time.
var orgMembership sync.Mutex

// lockOrgMembership takes orgMembership and returns the function that
// releases it. Call it as `defer lockOrgMembership(t)()` around one step.
func lockOrgMembership(t *testing.T) func() {
	t.Helper()
	orgMembership.Lock()
	return orgMembership.Unlock
}

// runMembership is run holding orgMembership.
func (c *cli) runMembership(args ...string) string {
	c.t.Helper()
	defer lockOrgMembership(c.t)()
	return c.run(args...)
}

// execMembership is exec holding orgMembership, for a step that may fail.
func (c *cli) execMembership(args ...string) result {
	c.t.Helper()
	defer lockOrgMembership(c.t)()
	return c.exec(runOpts{}, args...)
}

// failMembership is fail holding orgMembership.
func (c *cli) failMembership(args ...string) result {
	c.t.Helper()
	defer lockOrgMembership(c.t)()
	return c.fail(args...)
}

// cleanupMembership is cleanup whose delete runs holding orgMembership: a
// member's removal, or the deletion of a user who may still be a member.
func (c *cli) cleanupMembership(args ...string) {
	c.t.Helper()
	c.t.Cleanup(func() {
		r := c.execMembership(args...)
		if r.exitCode != 0 && !isNotFound(r) {
			c.t.Errorf("cleanup failed: %s", r)
		}
	})
}
