package explore

import (
	"context"

	cinc "github.com/cinc-project/cinc-api"
)

// newRoleKind builds the Roles kind: full view/edit/create/delete.
func newRoleKind() Kind {
	return editorKind[cinc.Role]{
		title:       "Roles",
		searchIndex: "role",
		summaryFn: func(_ context.Context, _ *cinc.Client, r *cinc.Role) []summaryField {
			return roleSummaryFields(r)
		},
		listFn: func(ctx context.Context, c *cinc.Client) (map[string]string, error) {
			index, _, err := c.Roles.List(ctx)
			return index, err
		},
		getFn: func(ctx context.Context, c *cinc.Client, name string) (*cinc.Role, error) {
			r, _, err := c.Roles.Get(ctx, name)
			return r, err
		},
		createFn: func(ctx context.Context, c *cinc.Client, r *cinc.Role) (CreateResult, error) {
			// The create endpoint returns only a URI, so report the name
			// from the submitted document.
			if _, _, err := c.Roles.Create(ctx, r); err != nil {
				return CreateResult{}, err
			}
			return CreateResult{Name: r.Name}, nil
		},
		updateFn: func(ctx context.Context, c *cinc.Client, r *cinc.Role) error {
			_, _, err := c.Roles.Update(ctx, r)
			return err
		},
		deleteFn: func(ctx context.Context, c *cinc.Client, name string) error {
			_, err := c.Roles.Delete(ctx, name)
			return err
		},
		template: func() []byte {
			return []byte(`{
  "name": "new-role",
  "description": "",
  "run_list": []
}`)
		},
	}
}

// roleSummaryFields builds the curated facts panel for a role.
func roleSummaryFields(r *cinc.Role) []summaryField {
	return []summaryField{
		{"Description", orDash(r.Description)},
		{"Run List", list(r.RunList, 0)},
		{"Env Run Lists", count(len(r.EnvRunLists))},
	}
}
