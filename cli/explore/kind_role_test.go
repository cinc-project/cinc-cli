package explore

import (
	"reflect"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestRoleSummaryFields(t *testing.T) {
	r := &cinc.Role{
		Name:        "web",
		Description: "web tier",
		RunList:     []string{"recipe[nginx]", "recipe[app]"},
		EnvRunLists: map[string][]string{"prod": {"recipe[nginx]"}},
	}
	var order []string
	got := map[string]string{}
	for _, f := range roleSummaryFields(r) {
		got[f.Label] = f.Value
		order = append(order, f.Label)
	}
	want := map[string]string{
		"Description":   "web tier",
		"Run List":      "recipe[nginx], recipe[app]",
		"Env Run Lists": "1",
	}
	for label, val := range want {
		if got[label] != val {
			t.Errorf("field %q = %q, want %q", label, got[label], val)
		}
	}
	if !reflect.DeepEqual(order, []string{"Description", "Run List", "Env Run Lists"}) {
		t.Errorf("field order = %v", order)
	}
}

func TestRoleSummaryFieldsEmpty(t *testing.T) {
	got := map[string]string{}
	for _, f := range roleSummaryFields(&cinc.Role{Name: "bare"}) {
		got[f.Label] = f.Value
	}
	if got["Description"] != "—" {
		t.Errorf("empty Description = %q, want em dash", got["Description"])
	}
	if got["Run List"] != "—" {
		t.Errorf("empty Run List = %q, want em dash", got["Run List"])
	}
	if got["Env Run Lists"] != "0" {
		t.Errorf("empty Env Run Lists = %q, want 0", got["Env Run Lists"])
	}
}
