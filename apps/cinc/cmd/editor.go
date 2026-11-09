package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/jsoneditor"
	"github.com/cinc-project/cinc-cli/cli/nodeedit"
)

// strictJSON returns a save-validation func for the typed editors. It
// rejects malformed JSON and any key the model T does not recognize, so a
// key added in the editor surfaces as an "unknown field" error rather than
// being silently dropped on unmarshal, which would make a real edit look
// like no change at all and report the object "unchanged". It applies the
// same rules as readJSONFile, since both decode with decodeStrictJSON.
func strictJSON[T any]() func([]byte) error {
	return func(b []byte) error {
		_, err := decodeStrictJSON[T](b)
		var unknown *unknownFieldError
		if errors.As(err, &unknown) {
			return fmt.Errorf("we don't recognize the field %q. Check the spelling, or remove it.", unknown.key)
		}
		return err
	}
}

// unknownFieldError reports a top-level key the model type has no field for.
type unknownFieldError struct{ key string }

func (e *unknownFieldError) Error() string { return fmt.Sprintf("unknown field %q", e.key) }

// chefExportKeys are keys a server GET (and so `knife ... show -F json` or
// `knife download`) returns that the cinc-api types don't model. They carry
// nothing the CLI sends back, so a document holding them is accepted and the
// values dropped, which keeps an export from knife or chef loading.
// json_class and chef_type apply to every type; the rest to the type named.
var chefExportKeys = map[reflect.Type][]string{
	nil: {"json_class", "chef_type"},
	// erchef's client GET names the client twice and adds the org. Chef's
	// client JSON (Chef::ApiClient#to_json) can also carry create_key and
	// private_key; keys are managed through `cinc client key`, so an edit
	// drops them, as it drops public_key.
	reflect.TypeFor[cinc.APIClient](): {"clientname", "orgname", "create_key", "private_key"},
	// erchef's user GET passes these profile fields through.
	reflect.TypeFor[cinc.User](): {"city", "country", "twitter_account", "image_file_name",
		"recovery_authentication_enabled", "external_authentication_uid"},
	// A group GET adds a flat "actors" list; a PUT-shaped file nests members
	// under "actors". cinc.Group's UnmarshalJSON reads both.
	reflect.TypeFor[cinc.Group](): {"actors"},
}

// decodeStrictJSON decodes b into a T, rejecting any top-level key T has no
// field for (an *unknownFieldError) other than the chefExportKeys. Keys are
// matched case-insensitively, as encoding/json matches them. A T that isn't
// a struct, such as the free-form cinc.DataBagItem, is decoded as is.
//
// It checks the keys itself rather than using DisallowUnknownFields, because
// a type with its own UnmarshalJSON (cinc.Group) never sees that setting.
func decodeStrictJSON[T any](b []byte) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		return v, nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return v, err
	}
	known := append(jsonFieldNames(t), chefExportKeys[nil]...)
	known = append(known, chefExportKeys[t]...)
	var unknown []string
	for k := range keys {
		if !slices.ContainsFunc(known, func(name string) bool { return strings.EqualFold(name, k) }) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return v, &unknownFieldError{key: unknown[0]}
	}
	return v, nil
}

// jsonFieldNames lists the JSON keys encoding/json decodes into struct type
// t, following untagged embedded structs the way encoding/json does.
func jsonFieldNames(t reflect.Type) []string {
	var names []string
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			names = append(names, jsonFieldNames(ft)...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// editNodeForm presents a node in the dedicated node-edit form (human
// fields plus a JSON attributes editor) and reports whether it changed. It
// is a package variable so tests can override it without spawning a TUI.
var editNodeForm = nodeedit.Run

// editClient, editRole, and the rest present the supplied object as
// pretty-printed JSON in the shared editor and return the parsed result. Each
// is a package variable so tests can override it without spawning a TUI.
var (
	editClient      = openObjectJSONEditor[cinc.APIClient]
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
// Validation rejects malformed JSON, and an item whose "id" is missing or
// no longer the one it opened with (the id names the item, so changing it
// would rename or overwrite another). It is a package variable so tests can
// stub it.
var editDataBagItem = openDataBagItemJSONEditor

func openDataBagItemJSONEditor(in cinc.DataBagItem) (cinc.DataBagItem, error) {
	initial, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, err
	}
	edited, err := jsoneditor.Run(initial, dataBagItemValidator(in.ID()))
	if err != nil {
		return nil, err
	}
	var out cinc.DataBagItem
	if err := json.Unmarshal(edited, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// dataBagItemValidator returns the editor's save check for the item id: the
// saved JSON must be an item (see cinc.DataBagItem.Validate) that still
// carries that id.
func dataBagItemValidator(id string) func([]byte) error {
	return func(b []byte) error {
		var item cinc.DataBagItem
		if err := json.Unmarshal(b, &item); err != nil {
			return err
		}
		if item.Validate() != nil || item.ID() != id {
			return fmt.Errorf("the item's \"id\" has to stay %q, since it names the item", id)
		}
		return nil
	}
}
