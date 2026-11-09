package explore

import (
	"context"
	"encoding/json"
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
)

// groupKind is the Groups kind. Groups are created by name only (a
// NamedCreatable), then edited to add members. admins is the session's
// shared admins-group memo, dropped whenever this kind writes the admins
// group so user summaries don't report stale admin status.
type groupKind struct {
	admins *adminsCache
}

// invalidateAdmins drops the admins memo when name is the admins group.
func (k groupKind) invalidateAdmins(c *cinc.Client, name string) {
	if name == adminsGroup {
		k.admins.invalidate(c)
	}
}

func newGroupKind(admins *adminsCache) Kind { return groupKind{admins: admins} }

func (groupKind) Title() string     { return "Groups" }
func (groupKind) Columns() []string { return []string{"NAME"} }

func (groupKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.Groups.List(ctx)
	if err != nil {
		return nil, err
	}
	return nameRows(index), nil
}

func (groupKind) Describe(ctx context.Context, c *cinc.Client, name string) (string, error) {
	g, _, err := c.Groups.Get(ctx, name)
	if err != nil {
		return "", err
	}
	return prettyJSON(g)
}

// Summary shows the group's membership at a glance — how many users,
// clients, and nested groups it pulls in.
func (groupKind) Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error) {
	return summarize(ctx, c, name,
		func(ctx context.Context, c *cinc.Client, n string) (*cinc.Group, error) {
			g, _, err := c.Groups.Get(ctx, n)
			return g, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, g *cinc.Group) []summaryField {
			return groupSummaryFields(g)
		})
}

// groupSummaryFields builds the curated facts panel for a group.
func groupSummaryFields(g *cinc.Group) []summaryField {
	return []summaryField{
		{"Users", count(len(g.Users))},
		{"Clients", count(len(g.Clients))},
		{"Nested Groups", count(len(g.Groups))},
	}
}

func (k groupKind) Save(ctx context.Context, c *cinc.Client, name string, edited []byte) error {
	var g cinc.Group
	if err := json.Unmarshal(edited, &g); err != nil {
		return fmt.Errorf("parse edited group: %w", err)
	}
	// Pin the selected group, so an edited name in the JSON can't send
	// the update to a different one.
	g.Name = name
	_, _, err := c.Groups.Update(ctx, &g)
	k.invalidateAdmins(c, name)
	return err
}

func (k groupKind) CreateNamed(ctx context.Context, c *cinc.Client, name string) (CreateResult, error) {
	_, err := c.Groups.Create(ctx, name)
	k.invalidateAdmins(c, name)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Name: name}, nil
}

func (k groupKind) Delete(ctx context.Context, c *cinc.Client, name string) error {
	_, err := c.Groups.Delete(ctx, name)
	k.invalidateAdmins(c, name)
	return err
}
