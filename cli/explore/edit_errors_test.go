package explore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func wantMsg(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
}

// A wrong-typed field fails before anything reaches the server (the client
// is nil here), and the message names the field and says nothing changed.
func TestSaveExplainsAWrongType(t *testing.T) {
	err := newRoleKind().(Editable).Save(context.Background(), nil, "web", []byte(`{"name":"web","run_list":"recipe[nginx]"}`))
	wantMsg(t, err, "role", `"run_list" should be a list`, "Nothing changed")
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("error should still wrap *json.UnmarshalTypeError: %v", err)
	}
}

func TestCreateExplainsAWrongType(t *testing.T) {
	_, err := newRoleKind().(Creatable).Create(context.Background(), nil, []byte(`{"name":"web","run_list":"recipe[nginx]"}`))
	wantMsg(t, err, "role", `"run_list" should be a list`, "Nothing was created")
}

func TestDataBagItemSaveExplainsANonObject(t *testing.T) {
	err := dataBagItemsKind{bag: "apps"}.Save(context.Background(), nil, "web", []byte(`["web"]`))
	wantMsg(t, err, "data bag item", "should be an object", "Nothing changed")
	_, err = dataBagItemsKind{bag: "apps"}.Create(context.Background(), nil, []byte(`["web"]`))
	wantMsg(t, err, "data bag item", "should be an object", "Nothing was created")
}

func TestGroupSaveExplainsAWrongType(t *testing.T) {
	err := groupKind{}.Save(context.Background(), nil, "admins", []byte(`{"users":"alice"}`))
	wantMsg(t, err, "group", "should be a list", "Nothing changed")
}
