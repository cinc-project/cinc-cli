package explore

import (
	"context"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
)

// pivotalUser is the Cinc/Chef Server's bootstrap superuser, and
// adminsGroup is the per-org group whose members hold admin rights.
// userType uses both to classify a user for the summary pane.
const (
	pivotalUser = "pivotal"
	adminsGroup = "admins"
)

// newUserKind builds the Users kind. Creating a user with create_key
// returns a one-time private key, surfaced through CreateResult.Secret.
func newUserKind() Kind {
	return editorKind[cinc.User]{
		title: "Users",
		summaryFn: func(ctx context.Context, c *cinc.Client, u *cinc.User) []summaryField {
			return userSummaryFields(u, userType(ctx, c, u.UserName))
		},
		listFn: func(ctx context.Context, c *cinc.Client) (map[string]string, error) {
			index, _, err := c.Users.List(ctx)
			return index, err
		},
		getFn: func(ctx context.Context, c *cinc.Client, name string) (*cinc.User, error) {
			u, _, err := c.Users.Get(ctx, name)
			return u, err
		},
		createFn: func(ctx context.Context, c *cinc.Client, u *cinc.User) (CreateResult, error) {
			result, _, err := c.Users.Create(ctx, u)
			if err != nil {
				return CreateResult{}, err
			}
			return CreateResult{Name: u.UserName, Secret: result.ChefKey.PrivateKey}, nil
		},
		updateFn: func(ctx context.Context, c *cinc.Client, u *cinc.User) error {
			_, _, err := c.Users.Update(ctx, u)
			return err
		},
		deleteFn: func(ctx context.Context, c *cinc.Client, name string) error {
			_, err := c.Users.Delete(ctx, name)
			return err
		},
		template: func() []byte {
			return []byte(`{
  "username": "new-user",
  "display_name": "New User",
  "email": "new-user@example.com",
  "first_name": "New",
  "last_name": "User",
  "password": "change-me-please",
  "create_key": true
}`)
		},
	}
}

// userType classifies a user for the summary pane. The pivotal bootstrap
// account is the server's Superuser, decided by name alone. Otherwise we
// look up the current org's admins group: a member is an Administrator,
// anyone else a plain User. If that lookup fails — there's no org, or the
// signed-in user can't read the group — we report Unknown rather than
// guess. Membership is the group's direct user list, matching what
// `cinc group show admins` reports.
func userType(ctx context.Context, c *cinc.Client, username string) string {
	if username == pivotalUser {
		return "Superuser"
	}
	group, _, err := c.Groups.Get(ctx, adminsGroup)
	if err != nil {
		return "Unknown"
	}
	if slices.Contains(group.Users, username) {
		return "Administrator"
	}
	return "User"
}

// userSummaryFields builds the curated facts panel for a user. The username
// is already the panel heading, so it leads with the user's Type — the access
// classification resolved by userType — followed by the human details an
// operator scans for.
func userSummaryFields(u *cinc.User, userType string) []summaryField {
	return []summaryField{
		{"Type", userType},
		{"Display Name", orDash(u.DisplayName)},
		{"Email", orDash(u.Email)},
		{"First Name", orDash(u.FirstName)},
		{"Last Name", orDash(u.LastName)},
	}
}
