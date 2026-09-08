# 0001: Resolve a user's admin status without a second `groups/admins` call

- **Status:** Proposed, not yet validated against server capabilities
- **Affects:** `cinc-api` (`UsersService`, `Group` model), Cinc/Chef Server
- **CLI surface that wants it:** the explorer user summary pane (`cinc explore` → Users)

## Summary

To show a user's access **Type** (Superuser / Administrator / User), the CLI
has to make **two** API calls per user it inspects: one to fetch the user, and
a second to fetch the org's `admins` group and test membership. The user object
itself carries no notion of role or admin status, so there's nothing to read.
We'd like the API to surface a user's effective admin status so a single fetch
answers the question.

## Where it bites

The explorer's user kind resolves the Type field in `userType`:

```go
// cli/explore/kind_user.go
func userType(ctx context.Context, c *cinc.Client, username string) string {
	if username == pivotalUser {
		return "Superuser"
	}
	group, _, err := c.Groups.Get(ctx, adminsGroup) // <-- second round trip
	if err != nil {
		return "Unknown"
	}
	if slices.Contains(group.Users, username) {
		return "Administrator"
	}
	return "User"
}
```

The user object has already been fetched by the kind's `getFn`
(`c.Users.Get`); the `c.Groups.Get(ctx, "admins")` call is purely to learn
something *about that user*. The two-call shape is the reason the user kind
needed the bespoke `summaryClientFn` seam on `editorKind` (most summary panes
render from the single already-fetched object).

The relevant `cinc-api` shapes today:

```go
// github.com/cinc-project/cinc-api
type User struct {
	UserName    string
	DisplayName string
	Email       string
	FirstName   string
	MiddleName  string
	LastName    string
	// ...no role/admin/membership field
}

type Group struct {
	Name    string
	Users   []string // direct members only
	Clients []string
	Groups  []string // nested groups are NOT expanded
}
```

## Why it matters

1. **Two round trips for one fact.** Every time the operator highlights a user,
   the CLI pays a second request. In a list-annotation future (showing Type in
   the user *list*, not just the detail pane) this would multiply per row unless
   we hand-roll caching.
2. **Needs read access to the group.** Determining admin status requires
   permission to read the `admins` group. A signed-in user who can list users
   but not read that group gets `Unknown`: the data is there, just gated behind
   a different object's ACL than the one being viewed.
3. **Direct membership only.** `Group.Users` is the literal member list. A user
   who is an admin *via a nested group* (the `admins` group containing another
   group that contains them) is not detected. Correct effective-membership
   resolution would have to expand `Group.Groups` recursively (more calls, more
   complexity), which the CLI does not attempt today.
4. **`admin` is org-scoped but `User` is global.** The global user object can't
   carry an org-specific admin flag; the answer only makes sense relative to the
   org the client is pointed at, which is exactly the context the explorer
   already has but the `/users/{name}` endpoint does not.

## Proposed improvements

In rough order of preference:

1. **Return effective roles on the org-scoped user association.** Have
   `GET /organizations/{org}/users/{name}` (the org-scoped view, which *does*
   know the org) include the user's effective roles (at minimum an `admin`
   boolean, ideally a small `roles`/`memberships` list) computed server-side
   with nested groups expanded. `cinc-api` would expose this as a field on the
   association result, and `userType` collapses to a single fetch with no group
   call and correct nested handling.

2. **A membership-test endpoint.** Something like
   `GET /organizations/{org}/groups/{group}/members/{actor}` returning a boolean
   (with nested expansion done server-side). One cheap, purpose-built call that
   respects the group's own ACL semantics and answers exactly the question,
   without pulling the whole member list. `cinc-api` adds e.g.
   `GroupsService.HasMember(ctx, group, actor) (bool, *Response, error)`.

3. **Server-side expanded group membership.** If the full group must be fetched,
   let the server optionally return *effective* membership (nested groups
   flattened): e.g. `GET .../groups/admins?expand=members` populating an
   `ExpandedUsers` field. This fixes correctness (#3 above) even if it doesn't
   remove the second call.

Option 1 is the most valuable: it removes the extra round trip, fixes nested
membership, and aligns the ACL with the object being viewed. Options 2 and 3 are
smaller, more local changes if a full role model is too big a lift.

## Workaround until then

Within the CLI, without any API change:

- **Cache the `admins` group for the session.** Fetch it once and reuse it
  across user summaries instead of re-fetching per selection. Cuts the calls to
  one per session but keeps the permission and nested-membership limitations.
- **Keep `Unknown` as the honest fallback** when the group can't be read, as the
  explorer does today: better than asserting `User` for someone we simply
  couldn't classify.

These are mitigations, not fixes; the underlying ask is for the API to make a
user's effective admin status readable in one call.
