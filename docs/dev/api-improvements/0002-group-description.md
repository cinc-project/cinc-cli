# 0002: Carry a human-readable description on groups

- **Status:** Proposed, needs validation against server capabilities (the
  Chef/Cinc Server may not persist a group description today)
- **Affects:** `cinc-api` (`Group` model), Cinc/Chef Server
- **CLI surface that wants it:** the explorer group summary pane
  (`cinc explore` → Groups), and the group create/edit flow

## Summary

A group has no place to say what it's *for*. The `Group` object is just a name
plus three membership lists, so the CLI can describe a group only by its
members, never by its purpose. Roles and environments both carry a free-form
`description`; groups are the odd one out. We'd like the API to carry a
`description` on the group object so the CLI can show it in the summary pane and
let operators edit it.

## Where it bites

The explorer's group summary can only report counts, because counts are all the
object holds:

```go
// cli/explore/kind_group.go
func (groupKind) Columns() []string { return []string{"NAME"} }
```

Compare the role and environment panes, which lead with the human description
their objects carry:

```go
// cli/explore/summary.go
func roleSummaryFields(r *cinc.Role) []summaryField {
	return []summaryField{
		{"Description", orDash(r.Description)}, // groups have no equivalent
		// ...
	}
}
```

The relevant `cinc-api` shapes today:

```go
// github.com/cinc-project/cinc-api
type Group struct {
	Name    string
	Users   []string // direct members only
	Clients []string
	Groups  []string // nested groups
	// ...no description / notes / metadata field
}

type Role struct {
	Name        string
	Description string // <-- groups want this
	// ...
}
```

A group fetched by the explorer's `getFn` (`c.Groups.Get`) therefore has nothing
to render beyond `len(Users)`, `len(Clients)`, and `len(Groups)`. The group
create/edit flow has nothing to prompt for either.

## Why it matters

1. **A group's purpose is invisible.** For `admins`, `deployers`, and
   `readonly-prod`, the intent lives only in the name and in tribal knowledge. An operator
   browsing groups in the explorer can't tell what a group is for without
   inspecting its membership and guessing.
2. **Inconsistent with peer objects.** Roles and environments both have a
   `description`. A group is just as much a first-class, human-managed object,
   but offers no field to explain itself, so the CLI's per-type summary is
   thinner for groups than for any other named object.
3. **Forces naming conventions to do a description's job.** Without a
   description field, teams encode meaning into group names
   (`team-payments-deploy-prod`), which is brittle and can't capture more than a
   slug's worth of intent.

## Proposed improvements

In rough order of preference:

1. **Add a `description` to the group object, end to end.** Have the server
   store and return a `description` on
   `GET/PUT /organizations/{org}/groups/{name}`, and expose it as
   `Group.Description string` in `cinc-api`. The CLI then renders it in the
   summary pane (leading the panel, as roles and environments do) and prompts
   for it on create/edit, with no other CLI changes needed. This is the clean fix
   and matches how `Role`/`Environment` already work.

2. **A general group metadata bag.** If a bare `description` is too narrow,
   a small server-side `metadata`/`attributes` map on the group would carry a
   description plus future fields (owner, contact, …). `cinc-api` exposes it as
   `Group.Metadata map[string]string`. More flexible, but heavier than the
   single field the CLI actually needs today.

Option 1 is strongly preferred: it's the smallest change, it mirrors an
existing, proven shape on sibling objects, and it drops straight into the CLI's
per-type summary contract.

## Workaround until then

There is **no** satisfying CLI-side workaround: the server has nowhere to
persist a description, so the CLI cannot store one on the operator's behalf. The
group summary pane shows membership counts only, and that is the honest ceiling
until the object itself can hold a description.

Before pursuing this, confirm whether the Cinc/Chef Server group object can
carry (or be extended to carry) a description at all; this entry assumes a
server change is in scope, not just a `cinc-api` model addition.
