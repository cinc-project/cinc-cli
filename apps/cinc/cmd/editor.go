package cmd

import (
	"bytes"
	"encoding/json"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/jsoneditor"
	"github.com/cinc-project/cinc-cli/cli/nodeedit"
)

// strictJSON returns a save-validation func for the typed editors. It
// rejects malformed JSON and any key the model T does not recognize, so a
// key added in the editor surfaces as an "unknown field" error rather than
// being silently dropped on unmarshal — which would make a real edit look
// like no change at all and report the object "unchanged".
func strictJSON[T any]() func([]byte) error {
	return func(b []byte) error {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		var v T
		return dec.Decode(&v)
	}
}

// editClient presents the supplied client as pretty-printed JSON in the
// shared JSON editor and returns the parsed result. It is a package
// variable so tests can override it without spawning a TUI.
var editClient = openClientJSONEditor

func openClientJSONEditor(in *cinc.APIClient) (*cinc.APIClient, error) {
	initial, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, err
	}
	edited, err := jsoneditor.Run(initial, strictJSON[cinc.APIClient]())
	if err != nil {
		return nil, err
	}
	var out cinc.APIClient
	if err := json.Unmarshal(edited, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// editRole, editEnvironment, editUser, and editGroup present the supplied
// object as pretty-printed JSON in the shared editor and return the parsed
// result. Each is a package variable so tests can override it without
// spawning a TUI, mirroring editClient.
// editNodeForm presents a node in the dedicated node-edit form (human
// fields plus a JSON attributes editor) and reports whether it changed. It
// is a package variable so tests can override it without spawning a TUI.
var editNodeForm = nodeedit.Run

var (
	editRole        = openObjectJSONEditor[cinc.Role]
	editEnvironment = openObjectJSONEditor[cinc.Environment]
	editUser        = openObjectJSONEditor[cinc.User]
	editGroup       = openObjectJSONEditor[cinc.Group]
	editKey         = openObjectJSONEditor[cinc.Key]
	editOrg         = openObjectJSONEditor[cinc.Org]
)

// openObjectJSONEditor is the generic edit-as-JSON flow shared by the simple
// server-object nouns: marshal in, edit with round-trip validation, unmarshal
// out.
func openObjectJSONEditor[T any](in *T) (*T, error) {
	initial, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, err
	}
	edited, err := jsoneditor.Run(initial, strictJSON[T]())
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(edited, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// editDataBagItem opens a data bag item in the shared JSON editor.
// Validation rejects malformed JSON and items missing an "id" key. It
// is a package variable so tests can stub it.
var editDataBagItem = openDataBagItemJSONEditor

func openDataBagItemJSONEditor(in cinc.DataBagItem) (cinc.DataBagItem, error) {
	initial, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, err
	}
	edited, err := jsoneditor.Run(initial, validateDataBagItem)
	if err != nil {
		return nil, err
	}
	var out cinc.DataBagItem
	if err := json.Unmarshal(edited, &out); err != nil {
		return nil, err
	}
	return out, nil
}
