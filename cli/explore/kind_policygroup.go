package explore

import (
	"context"
	"fmt"
	"sort"

	cinc "github.com/cinc-project/cinc-api"
)

// policyGroupKind is the top-level Policy Groups kind. A policy group
// maps policy names to the revision active in that group; you drill in
// to see the member policies. The group itself can be deleted.
type policyGroupKind struct{}

func (policyGroupKind) Title() string     { return "Policy Groups" }
func (policyGroupKind) Columns() []string { return []string{"NAME", "POLICIES"} }

func (policyGroupKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.PolicyGroups.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(index))
	for name, pg := range index {
		rows = append(rows, Row{
			Name:  name,
			Cells: []string{name, fmt.Sprintf("%d", len(pg.Policies))},
		})
	}
	sortRows(rows)
	return rows, nil
}

func (policyGroupKind) Delete(ctx context.Context, c *cinc.Client, name string) error {
	_, err := c.PolicyGroups.Delete(ctx, name)
	return err
}

func (policyGroupKind) Child(parent string) Kind { return pgPoliciesKind{group: parent} }

// Summary shows how many policies a group pins — the membership you're about
// to drill into.
func (policyGroupKind) Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error) {
	return summarize(ctx, c, name,
		func(ctx context.Context, c *cinc.Client, n string) (*cinc.PolicyGroup, error) {
			pg, _, err := c.PolicyGroups.Get(ctx, n)
			return pg, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, pg *cinc.PolicyGroup) []summaryField {
			return []summaryField{{"Policies", count(len(pg.Policies))}}
		})
}

// pgPoliciesKind lists the policies bound into one policy group, each
// with the revision active in that group. A binding can be viewed (the
// pinned revision) or deleted (removing it from the group).
type pgPoliciesKind struct{ group string }

func (k pgPoliciesKind) Title() string   { return k.group }
func (pgPoliciesKind) Columns() []string { return []string{"POLICY", "REVISION"} }

func (k pgPoliciesKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	pg, _, err := c.PolicyGroups.Get(ctx, k.group)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(pg.Policies))
	for name, assignment := range pg.Policies {
		rows = append(rows, Row{Name: name, Cells: []string{name, assignment.RevisionID}})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func (k pgPoliciesKind) Describe(ctx context.Context, c *cinc.Client, policy string) (string, error) {
	rev, _, err := c.PolicyGroups.GetPolicy(ctx, k.group, policy)
	if err != nil {
		return "", err
	}
	return prettyJSON(rev)
}

// Summary shows the revision this group pins the policy to and what that
// revision runs.
func (k pgPoliciesKind) Summary(ctx context.Context, c *cinc.Client, policy string) (summaryView, error) {
	return summarize(ctx, c, policy,
		func(ctx context.Context, c *cinc.Client, p string) (*cinc.PolicyRevision, error) {
			rev, _, err := c.PolicyGroups.GetPolicy(ctx, k.group, p)
			return rev, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, rev *cinc.PolicyRevision) []summaryField {
			return []summaryField{
				{"Revision", orDash(rev.RevisionID)},
				{"Run List", list(rev.RunList, 0)},
			}
		})
}

func (k pgPoliciesKind) Delete(ctx context.Context, c *cinc.Client, policy string) error {
	_, err := c.PolicyGroups.DeletePolicy(ctx, k.group, policy)
	return err
}
