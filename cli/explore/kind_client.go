package explore

import (
	"context"

	cinc "github.com/cinc-project/cinc-api"
)

// newClientKind builds the Clients kind. Creating a client may return a
// one-time private key, surfaced through CreateResult.Secret.
func newClientKind() Kind {
	return editorKind[cinc.APIClient]{
		title:       "Clients",
		searchIndex: "client",
		summaryFn: func(_ context.Context, _ *cinc.Client, cl *cinc.APIClient) []summaryField {
			return clientSummaryFields(cl)
		},
		listFn: func(ctx context.Context, c *cinc.Client) (map[string]string, error) {
			index, _, err := c.Clients.List(ctx)
			return index, err
		},
		getFn: func(ctx context.Context, c *cinc.Client, name string) (*cinc.APIClient, error) {
			cl, _, err := c.Clients.Get(ctx, name)
			return cl, err
		},
		createFn: func(ctx context.Context, c *cinc.Client, cl *cinc.APIClient) (CreateResult, error) {
			// The response carries the generated key but not the name, so
			// take the name from the submitted document.
			created, _, err := c.Clients.Create(ctx, cl)
			if err != nil {
				return CreateResult{}, err
			}
			return CreateResult{Name: cl.Name, Secret: created.ChefKey.PrivateKey}, nil
		},
		updateFn: func(ctx context.Context, c *cinc.Client, name string, cl *cinc.APIClient) error {
			// Pin the selected client: a different name in the body would
			// rename it.
			cl.Name = name
			_, _, err := c.Clients.Update(ctx, name, cl)
			return err
		},
		deleteFn: func(ctx context.Context, c *cinc.Client, name string) error {
			_, err := c.Clients.Delete(ctx, name)
			return err
		},
		template: func() []byte {
			return []byte(`{
  "name": "new-client",
  "validator": false
}`)
		},
	}
}

// clientSummaryFields builds the curated facts panel for a client. The name
// is already the panel heading, so it leads with the client's role — whether
// it's a validator, the bootstrap identity nodes use to register. The client
// object carries no keys under server API v1; they live under its keys
// endpoint.
func clientSummaryFields(cl *cinc.APIClient) []summaryField {
	role := "Regular"
	if cl.Validator {
		role = "Validator"
	}
	return []summaryField{
		{"Type", role},
	}
}
