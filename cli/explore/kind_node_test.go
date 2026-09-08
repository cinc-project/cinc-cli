package explore

import (
	"reflect"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

func TestNodeSummaryFields(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	n := &cinc.Node{
		Name:        "web01",
		Environment: "production",
		PolicyGroup: "web",
		RunList:     []string{"role[base]", "recipe[nginx]"},
		Automatic: cinc.Attributes{
			"ohai_time": float64(now.Add(-2 * time.Hour).Unix()),
			"chef_packages": map[string]any{
				"chef": map[string]any{"version": "18.4.2"},
			},
		},
	}
	fields := nodeSummaryFields(n, now)

	want := map[string]string{
		"Environment":    "production",
		"Policy Group":   "web",
		"Client Version": "18.4.2",
		"Last Scan":      "2h ago",
		"Run List":       "role[base], recipe[nginx]",
	}
	got := map[string]string{}
	for _, f := range fields {
		got[f.Label] = f.Value
	}
	for label, val := range want {
		if got[label] != val {
			t.Errorf("field %q = %q, want %q", label, got[label], val)
		}
	}

	var order []string
	for _, f := range fields {
		order = append(order, f.Label)
	}
	wantOrder := []string{"Environment", "Policy Group", "Run List", "Client Version", "Last Scan"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("field order = %v, want %v", order, wantOrder)
	}
}

func TestNodeSummaryFieldsEmpty(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	n := &cinc.Node{Name: "bare"}
	got := map[string]string{}
	for _, f := range nodeSummaryFields(n, now) {
		got[f.Label] = f.Value
	}
	if got["Environment"] != "—" {
		t.Errorf("empty Environment = %q, want em dash", got["Environment"])
	}
	if got["Last Scan"] != "never" {
		t.Errorf("empty Last Scan = %q, want never", got["Last Scan"])
	}
	if got["Run List"] != "—" {
		t.Errorf("empty Run List = %q, want em dash", got["Run List"])
	}
}

func TestNodeTitle(t *testing.T) {
	tests := []struct {
		name string
		node *cinc.Node
		want string
	}{
		{
			name: "platform and version",
			node: &cinc.Node{Name: "web1", Automatic: cinc.Attributes{
				"platform":         "ubuntu",
				"platform_version": "24.04",
			}},
			want: "web1 - ubuntu 24.04",
		},
		{
			name: "platform only",
			node: &cinc.Node{Name: "web1", Automatic: cinc.Attributes{
				"platform": "ubuntu",
			}},
			want: "web1 - ubuntu",
		},
		{
			name: "no platform",
			node: &cinc.Node{Name: "web1"},
			want: "web1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeTitle(tc.node); got != tc.want {
				t.Errorf("nodeTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}
