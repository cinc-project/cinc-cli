package explore

import (
	"context"
	"fmt"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// newNodeKind builds the Nodes kind: full view/edit/create/delete.
func newNodeKind() Kind {
	return editorKind[cinc.Node]{
		title:       "Nodes",
		searchIndex: "node",
		summaryFn: func(_ context.Context, _ *cinc.Client, n *cinc.Node) []summaryField {
			return nodeSummaryFields(n, time.Now())
		},
		titleFn: nodeTitle,
		formFn:  newNodeForm,
		listFn: func(ctx context.Context, c *cinc.Client) (map[string]string, error) {
			index, _, err := c.Nodes.List(ctx)
			return index, err
		},
		getFn: func(ctx context.Context, c *cinc.Client, name string) (*cinc.Node, error) {
			n, _, err := c.Nodes.Get(ctx, name)
			return n, err
		},
		createFn: func(ctx context.Context, c *cinc.Client, n *cinc.Node) (CreateResult, error) {
			// The create endpoint returns only a URI, not the object, so
			// report the name from the document the user submitted.
			if _, _, err := c.Nodes.Create(ctx, n); err != nil {
				return CreateResult{}, err
			}
			return CreateResult{Name: n.Name}, nil
		},
		updateFn: func(ctx context.Context, c *cinc.Client, n *cinc.Node) error {
			_, _, err := c.Nodes.Update(ctx, n)
			return err
		},
		deleteFn: func(ctx context.Context, c *cinc.Client, name string) error {
			_, err := c.Nodes.Delete(ctx, name)
			return err
		},
		template: func() []byte {
			return []byte(`{
  "name": "new-node",
  "chef_environment": "_default",
  "run_list": []
}`)
		},
	}
}

// nodeTitle is the heading for a node's summary panel: its name, plus the
// platform and version when ohai has reported them — e.g. "web1 - ubuntu
// 24.04". A node we've never scanned has no platform, so it shows the bare
// name.
func nodeTitle(n *cinc.Node) string {
	platform := n.Automatic.GetString("platform")
	if platform == "" {
		return n.Name
	}
	if version := n.Automatic.GetString("platform_version"); version != "" {
		return fmt.Sprintf("%s - %s %s", n.Name, platform, version)
	}
	return fmt.Sprintf("%s - %s", n.Name, platform)
}

// nodeSummaryFields builds the curated facts panel for a node: the bits an
// operator scanning a fleet wants at a glance.
func nodeSummaryFields(n *cinc.Node, now time.Time) []summaryField {
	var ohai float64
	if v, ok := n.Automatic.Dig("ohai_time"); ok {
		if f, ok := v.(float64); ok {
			ohai = f
		}
	}
	return []summaryField{
		{"Environment", orDash(n.Environment)},
		{"Policy Group", orDash(n.PolicyGroup)},
		{"Run List", list(n.RunList, 0)},
		{"Client Version", orDash(n.Automatic.GetString("chef_packages", "chef", "version"))},
		{"Last Scan", relativeTime(ohai, now)},
	}
}
