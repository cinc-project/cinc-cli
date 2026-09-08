package explore

import (
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestEnvironmentSummaryFields(t *testing.T) {
	e := &cinc.Environment{
		Name:        "prod",
		Description: "production",
		CookbookVersions: map[string]string{
			"nginx": "= 1.0.0",
			"app":   "~> 2.0",
		},
	}
	got := map[string]string{}
	for _, f := range environmentSummaryFields(e) {
		got[f.Label] = f.Value
	}
	if got["Description"] != "production" {
		t.Errorf("Description = %q, want production", got["Description"])
	}
	if got["Cookbook Constraints"] != "2" {
		t.Errorf("Cookbook Constraints = %q, want 2", got["Cookbook Constraints"])
	}
}

func TestEnvironmentSummaryFieldsEmpty(t *testing.T) {
	got := map[string]string{}
	for _, f := range environmentSummaryFields(&cinc.Environment{Name: "bare"}) {
		got[f.Label] = f.Value
	}
	if got["Description"] != "—" {
		t.Errorf("empty Description = %q, want em dash", got["Description"])
	}
	if got["Cookbook Constraints"] != "0" {
		t.Errorf("empty Cookbook Constraints = %q, want 0", got["Cookbook Constraints"])
	}
}
