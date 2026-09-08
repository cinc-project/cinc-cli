package nodeedit

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/jsoneditor"
)

func keyType(m Model, t tea.KeyType) Model { m, _ = m.Update(tea.KeyMsg{Type: t}); return m }
func ctrlD(m Model) Model                  { return keyType(m, tea.KeyCtrlD) }
func ctrlC(m Model) Model                  { return keyType(m, tea.KeyCtrlC) }
func down(m Model) Model                   { return keyType(m, tea.KeyDown) }
func up(m Model) Model                     { return keyType(m, tea.KeyUp) }
func enter(m Model) Model                  { return keyType(m, tea.KeyEnter) }
func esc(m Model) Model                    { return keyType(m, tea.KeyEsc) }

func typeRunes(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func newForm(t *testing.T, n *cinc.Node) Model {
	t.Helper()
	m, err := New(n)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestFormSavesEditedEnvironment(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01", Environment: "prod"})
	m.env.SetValue("staging")
	m = ctrlD(m)
	if !m.Finished() || m.Aborted() {
		t.Fatalf("expected a saved edit, finished=%v aborted=%v", m.Finished(), m.Aborted())
	}
	if !m.Changed() {
		t.Error("an environment change should be reported as changed")
	}
	got := m.Result()
	if got.Environment != "staging" {
		t.Errorf("environment = %q, want staging", got.Environment)
	}
	if got.Name != "db01" {
		t.Errorf("name = %q, want db01 (carried from original)", got.Name)
	}
}

func TestFormUnchangedWhenNothingEdited(t *testing.T) {
	node := &cinc.Node{
		Name:        "db01",
		Environment: "prod",
		RunList:     []string{"recipe[nginx]"},
		Normal:      cinc.Attributes{"role": "db"},
	}
	m := ctrlD(newForm(t, node))
	if !m.Finished() {
		t.Fatal("Ctrl-D should finish the form")
	}
	if m.Changed() {
		t.Error("no edits should report unchanged")
	}
}

func TestFormAbortsOnCtrlC(t *testing.T) {
	m := ctrlC(newForm(t, &cinc.Node{Name: "db01"}))
	if !m.Finished() || !m.Aborted() {
		t.Fatalf("Ctrl-C should abort: finished=%v aborted=%v", m.Finished(), m.Aborted())
	}
	if m.Result() != nil {
		t.Error("an aborted edit must not produce a result")
	}
}

func TestFormUpDownMovesFocusAndClamps(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01"})
	if m.focus != focusEnv {
		t.Fatalf("form should start focused on environment, got %v", m.focus)
	}
	m = up(m) // already at top: stays
	if m.focus != focusEnv {
		t.Errorf("up at top should stay on env, got %v", m.focus)
	}
	for _, w := range []focus{focusPolicyGroup, focusPolicyName, focusRunList, focusAttrs} {
		m = down(m)
		if m.focus != w {
			t.Errorf("down -> focus=%v, want %v", m.focus, w)
		}
	}
	m = down(m) // already at bottom: stays
	if m.focus != focusAttrs {
		t.Errorf("down at bottom should stay on attrs, got %v", m.focus)
	}
}

func TestFormDiveIntoRunListAndType(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01"})
	for m.focus != focusRunList {
		m = down(m)
	}
	m = typeRunes(m, "ignored") // not dived yet: typing does nothing
	m = enter(m)                // dive in
	if !m.dived {
		t.Fatal("Enter on the run list should dive in")
	}
	m = typeRunes(m, "recipe[apache]")
	m = esc(m) // back out
	if m.dived {
		t.Error("Esc should exit the dive")
	}
	m = ctrlD(m)
	rl := m.Result().RunList
	if len(rl) != 1 || rl[0] != "recipe[apache]" {
		t.Errorf("run_list = %v, want [recipe[apache]]", rl)
	}
}

func TestFormRunListAutoBulletsEachLine(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01"})
	for m.focus != focusRunList {
		m = down(m)
	}
	m = enter(m) // dive in: an empty run list seeds the first "- " bullet
	m = typeRunes(m, "recipe[base]")
	m = enter(m) // Enter starts a new bulleted line, not a bare newline
	m = typeRunes(m, "role[web]")
	m = esc(m)
	m = ctrlD(m)
	got := m.Result().RunList
	want := []string{"recipe[base]", "role[web]"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("run_list = %v, want %v", got, want)
	}
}

func TestFormEscAtTopLevelAborts(t *testing.T) {
	m := esc(newForm(t, &cinc.Node{Name: "db01"}))
	if !m.Finished() || !m.Aborted() {
		t.Fatalf("Esc at the top level should cancel: finished=%v aborted=%v", m.Finished(), m.Aborted())
	}
}

func TestFormSavesAttributeEdits(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01"})
	// Stand in a fresh attributes editor carrying an edit, then save.
	m.attrs = jsoneditor.New(
		[]byte(`{"normal":{"role":"db"},"default":{},"override":{}}`),
		func([]byte) error { return nil },
	)
	m = ctrlD(m)
	if m.Result().Normal["role"] != "db" {
		t.Errorf("attribute edit not saved: %v", m.Result().Normal)
	}
	if !m.Changed() {
		t.Error("adding an attribute should be a change")
	}
}

func TestFormRejectsInvalidAttributeBag(t *testing.T) {
	m := newForm(t, &cinc.Node{Name: "db01"})
	m.attrs = jsoneditor.New(
		[]byte(`{"normal":{},"default":{},"override":{},"automatic":{},"bogus":{}}`),
		func([]byte) error { return nil },
	)
	m = ctrlD(m)
	if m.Finished() {
		t.Fatal("an unknown attribute bag should keep the form open")
	}
	if m.errMsg == "" {
		t.Error("expected an inline error for the unknown bag")
	}
}

func newCreateForm(t *testing.T) Model {
	t.Helper()
	m, err := NewCreate()
	if err != nil {
		t.Fatalf("NewCreate: %v", err)
	}
	return m
}

func TestCreateFormStartsOnEditableName(t *testing.T) {
	m := newCreateForm(t)
	if m.focus != focusName {
		t.Fatalf("create form should start focused on the name, got %v", m.focus)
	}
	m = up(m) // already at the top: stays on name
	if m.focus != focusName {
		t.Errorf("up at top should stay on name, got %v", m.focus)
	}
	m = down(m)
	if m.focus != focusEnv {
		t.Errorf("down from name should move to environment, got %v", m.focus)
	}
}

func TestCreateFormSavesTypedName(t *testing.T) {
	m := newCreateForm(t)
	m = typeRunes(m, "web01")
	m = down(m)
	m = typeRunes(m, "staging")
	m = ctrlD(m)
	if !m.Finished() || m.Aborted() {
		t.Fatalf("expected a saved create, finished=%v aborted=%v", m.Finished(), m.Aborted())
	}
	if !m.Changed() {
		t.Error("a created node should always count as changed")
	}
	got := m.Result()
	if got.Name != "web01" {
		t.Errorf("name = %q, want web01", got.Name)
	}
	if got.Environment != "staging" {
		t.Errorf("environment = %q, want staging", got.Environment)
	}
}

func TestCreateFormRequiresAName(t *testing.T) {
	m := newCreateForm(t)
	m = ctrlD(m) // no name typed
	if m.Finished() {
		t.Fatal("an empty name should keep the create form open")
	}
	if m.errMsg == "" {
		t.Error("expected an inline error demanding a name")
	}
	if m.focus != focusName {
		t.Errorf("focus should return to the name field, got %v", m.focus)
	}
}

func TestCommittedRoundTripsToJSON(t *testing.T) {
	m := newCreateForm(t)
	m = typeRunes(m, "web01")
	m = ctrlD(m)
	committed := m.Committed()
	if committed == nil {
		t.Fatal("a saved form should produce committed JSON")
	}
	var n cinc.Node
	if err := json.Unmarshal(committed, &n); err != nil {
		t.Fatalf("committed JSON did not parse: %v", err)
	}
	if n.Name != "web01" {
		t.Errorf("committed name = %q, want web01", n.Name)
	}
}

func TestCommittedNilWhenAborted(t *testing.T) {
	m := ctrlC(newCreateForm(t))
	if m.Committed() != nil {
		t.Error("an aborted form must not produce committed JSON")
	}
}
