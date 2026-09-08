// Package explore implements `cinc explore`, a k9s-style terminal UI
// for browsing and mutating every object type on a Cinc/Chef server.
//
// The shell (profile picker → kind menu → list → detail, plus modal
// overlays for editing, confirming, and prompting) is generic. Each
// object type plugs in through the Kind interface and a set of small,
// optional capability interfaces it opts into. The shell type-asserts
// those capabilities to decide which actions a kind supports — that is
// what makes the action bar change per resource.
package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	cinc "github.com/cinc-project/cinc-api"
)

// Row is one object in a kind's list. Name is the identifier passed to
// the capability methods (Describe, Save, Delete, …); Cells holds the
// display values aligned with the kind's Columns.
type Row struct {
	Name  string
	Cells []string
}

// CreateResult reports the outcome of a create. Secret, when non-empty,
// is a one-time credential (a generated private key) the shell shows in
// a result modal because the server never returns it again.
type CreateResult struct {
	Name   string
	Secret string
}

// Kind is one browsable object type. Every kind supports the read path:
// a titled, columned list of objects.
type Kind interface {
	Title() string
	Columns() []string
	List(ctx context.Context, c *cinc.Client) ([]Row, error)
}

// The capability interfaces below are optional. A kind advertises an
// action by implementing the matching interface; the shell gates the
// action's key on a runtime type assertion.

// Viewable kinds render a single object as pretty JSON in the detail
// pane.
type Viewable interface {
	Describe(ctx context.Context, c *cinc.Client, name string) (string, error)
}

// Editable kinds open an object in the JSON editor and save the result.
type Editable interface {
	Viewable
	Save(ctx context.Context, c *cinc.Client, name string, edited []byte) error
}

// Creatable kinds create a new object from a JSON document the user
// edits, seeded with NewTemplate.
type Creatable interface {
	NewTemplate() []byte
	Create(ctx context.Context, c *cinc.Client, doc []byte) (CreateResult, error)
}

// NamedCreatable kinds create a new object from just a name (no body),
// gathered through a name-prompt modal.
type NamedCreatable interface {
	CreateNamed(ctx context.Context, c *cinc.Client, name string) (CreateResult, error)
}

// Deletable kinds delete an object by name.
type Deletable interface {
	Delete(ctx context.Context, c *cinc.Client, name string) error
}

// Downloadable kinds write an object to a local directory and report
// the path written.
type Downloadable interface {
	Download(ctx context.Context, c *cinc.Client, name, destDir string) (string, error)
}

// DrillDown kinds contain children: selecting a row pushes the child
// kind, already scoped to the chosen parent.
type DrillDown interface {
	Child(parent string) Kind
}

// Summarizable kinds render a selected object as a curated, human-friendly
// facts panel in the split-screen summary pane. Every kind implements it (via
// the shared summarize helper), so hovering any object shows friendly values
// rather than raw JSON; the raw JSON stays one keypress away in the detail
// view.
type Summarizable interface {
	Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error)
}

// Searchable kinds can be queried server-side with a Solr/Lucene query.
// SearchIndex names the index to hit; an empty string means the kind is
// not search-indexed (the `s` key stays inert for it). Only node, role,
// environment, client, and data bag items live in the Chef search index.
type Searchable interface {
	SearchIndex() string
}

// searchIndexOf returns a kind's Solr index and whether it is searchable.
func searchIndexOf(k Kind) (string, bool) {
	s, ok := k.(Searchable)
	if !ok {
		return "", false
	}
	idx := s.SearchIndex()
	return idx, idx != ""
}

// searchableKinds returns the top-level kinds that can be searched, in
// registry order, for the index picker. Data bag items are searchable too
// but only reachable by drilling into a specific bag, so they are not
// offered as a standalone picker choice.
func searchableKinds() []Kind {
	var out []Kind
	for _, k := range registry() {
		if _, ok := searchIndexOf(k); ok {
			out = append(out, k)
		}
	}
	return out
}

// indexOfKind returns the position of the kind sharing target's search
// index within ks, or 0 when there's no match.
func indexOfKind(ks []Kind, target Kind) int {
	want, _ := searchIndexOf(target)
	for i, k := range ks {
		if idx, _ := searchIndexOf(k); idx == want {
			return i
		}
	}
	return 0
}

// registry returns the kinds shown in the top-level menu, in display
// order.
func registry() []Kind {
	return []Kind{
		newNodeKind(),
		newRoleKind(),
		newEnvironmentKind(),
		newClientKind(),
		newGroupKind(),
		newUserKind(),
		dataBagKind{},
		cookbookKind{},
		policyKind{},
		policyGroupKind{},
	}
}

// ----- editorKind: the uniform editor-backed CRUD nouns ----------------

// editorKind adapts an API service with the common shape — list by
// name, get/create/update a typed object, delete by name — into a Kind
// with full view/edit/create/delete. node, role, environment, client,
// and user are all built from it; their differences (columns, the
// create call, the one-time user key) live entirely in the injected
// closures.
type editorKind[T any] struct {
	title    string
	columns  []string
	listFn   func(ctx context.Context, c *cinc.Client) (map[string]string, error)
	rowsFn   func(map[string]string) []Row // optional; defaults to name-only rows
	getFn    func(ctx context.Context, c *cinc.Client, name string) (*T, error)
	createFn func(ctx context.Context, c *cinc.Client, obj *T) (CreateResult, error)
	updateFn func(ctx context.Context, c *cinc.Client, obj *T) error
	deleteFn func(ctx context.Context, c *cinc.Client, name string) error
	// summaryFn builds the curated facts panel for an object. It receives the
	// client and context so a kind can enrich the panel with data from extra
	// API calls (the user kind uses it to look up org-admin status); kinds
	// that don't need them just ignore them.
	summaryFn func(ctx context.Context, c *cinc.Client, obj *T) []summaryField
	// titleFn, when set, overrides the summary panel heading (otherwise the
	// bare object name); a node uses it to append its platform and version.
	titleFn func(*T) string
	// formFn, when set, supplies a typed modal form for edit/create instead
	// of the generic JSON editor; a node uses it for its human-fields form.
	formFn   func(action editAction, seed []byte) (subEditor, error)
	template func() []byte
	// searchIndex names this kind's Solr index when it is search-indexed
	// (node, role, environment, client); empty leaves the kind unsearchable.
	searchIndex string
}

func (k editorKind[T]) Title() string { return k.title }

func (k editorKind[T]) SearchIndex() string { return k.searchIndex }

func (k editorKind[T]) Columns() []string {
	if len(k.columns) == 0 {
		return []string{"NAME"}
	}
	return k.columns
}

func (k editorKind[T]) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, err := k.listFn(ctx, c)
	if err != nil {
		return nil, err
	}
	if k.rowsFn != nil {
		rows := k.rowsFn(index)
		sortRows(rows)
		return rows, nil
	}
	return nameRows(index), nil
}

func (k editorKind[T]) Describe(ctx context.Context, c *cinc.Client, name string) (string, error) {
	obj, err := k.getFn(ctx, c, name)
	if err != nil {
		return "", err
	}
	return prettyJSON(obj)
}

// Summary fetches the object and returns its curated facts panel. The full
// object's JSON rides along in the returned view so the model can cache it and
// serve the detail and edit views from this one fetch instead of issuing their
// own Get for the same object.
func (k editorKind[T]) Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error) {
	return summarize(ctx, c, name, k.getFn, k.titleFn, k.summaryFn)
}

func (k editorKind[T]) Save(ctx context.Context, c *cinc.Client, name string, edited []byte) error {
	var obj T
	if err := json.Unmarshal(edited, &obj); err != nil {
		return fmt.Errorf("parse edited %s: %w", k.title, err)
	}
	return k.updateFn(ctx, c, &obj)
}

func (k editorKind[T]) NewTemplate() []byte { return k.template() }

// NewForm returns this kind's typed modal form for the given action, or nil
// when the kind has none (the caller then falls back to the JSON editor).
func (k editorKind[T]) NewForm(action editAction, seed []byte) (subEditor, error) {
	if k.formFn == nil {
		return nil, nil
	}
	return k.formFn(action, seed)
}

func (k editorKind[T]) Create(ctx context.Context, c *cinc.Client, doc []byte) (CreateResult, error) {
	var obj T
	if err := json.Unmarshal(doc, &obj); err != nil {
		return CreateResult{}, fmt.Errorf("parse new %s: %w", k.title, err)
	}
	return k.createFn(ctx, c, &obj)
}

func (k editorKind[T]) Delete(ctx context.Context, c *cinc.Client, name string) error {
	return k.deleteFn(ctx, c, name)
}

// ----- shared helpers --------------------------------------------------

// nameRows turns a name→url index into single-column rows sorted by
// name.
func nameRows(index map[string]string) []Row {
	rows := make([]Row, 0, len(index))
	for name := range index {
		rows = append(rows, Row{Name: name, Cells: []string{name}})
	}
	sortRows(rows)
	return rows
}

func sortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
}

// prettyJSON marshals v as indented JSON for the detail pane.
func prettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
