package cmd

import (
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// A key added at the top level of a typed object has no home in the model
// and would be silently dropped on unmarshal, making the edit look like no
// change. strictJSON surfaces it as an error so the editor keeps the user
// editing instead of reporting the object "unchanged".
func TestStrictJSONRejectsUnknownTopLevelKey(t *testing.T) {
	validate := strictJSON[cinc.Node]()
	err := validate([]byte(`{"name":"db01","run_list":[],"newKey":null}`))
	if err == nil {
		t.Fatal("expected an unknown-field error for a new top-level key")
	}
	if !strings.Contains(err.Error(), "newKey") {
		t.Errorf("error should name the unknown field, got %v", err)
	}
}

func TestStrictJSONAcceptsKnownFieldsAndFreeFormAttributes(t *testing.T) {
	validate := strictJSON[cinc.Node]()
	// Attribute bags are maps, so arbitrary keys *inside* them are fine.
	in := `{"name":"db01","run_list":["recipe[nginx]"],"normal":{"anything":1}}`
	if err := validate([]byte(in)); err != nil {
		t.Errorf("valid node JSON should pass: %v", err)
	}
}

func TestStrictJSONRejectsMalformed(t *testing.T) {
	if err := strictJSON[cinc.Node]()([]byte(`{`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}
