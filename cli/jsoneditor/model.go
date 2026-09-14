package jsoneditor

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// mode is the top-level editing surface: a structural tree walker or the
// raw free-text textarea.
type mode uint8

const (
	modeStructural mode = iota
	modeRaw
)

// editState is the transient sub-state layered on top of the mode.
type editState uint8

const (
	stNavigating editState = iota // structural: moving the cursor between units
	stInlineEdit                  // structural: editing a key or scalar in a textinput
	stBlockEdit                   // structural: editing a whole {}/[] subtree in the textarea
	stPreview                     // confirming the highlighted save preview
)

// Syntax-highlight palette for the always-on structural view, roughly
// matching the chroma "monokai" preview: keys cyan, strings green,
// numbers purple, bool/null pink, punctuation dim. selStyle marks the
// selected unit with reverse video so it reads on any theme.
var (
	styKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	styStr   = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	styNum   = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	styLit   = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	styPunct = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selStyle = lipgloss.NewStyle().Reverse(true)
)

// styler adapts a lipgloss.Style to the renderTheme's func(string) string.
func styler(s lipgloss.Style) func(string) string {
	return func(t string) string { return s.Render(t) }
}

// syntaxTheme is the production renderTheme: every token is colored by its
// JSON type and the selected unit is reverse-video.
func syntaxTheme() renderTheme {
	return renderTheme{
		key: styler(styKey), str: styler(styStr), num: styler(styNum),
		lit: styler(styLit), punct: styler(styPunct), sel: styler(selStyle),
	}
}

// Model is the editor state. It is an embeddable bubbletea component:
// call New, then forward Update/View to it, and check Finished after each
// Update to learn when the user has committed or aborted. It does not quit
// the surrounding program — embedding code decides what to do when
// Finished reports true.
//
// By default it opens in structural mode: a JSON-aware cursor selects a
// key, a scalar value, or a whole object/array block (highlighted as a
// unit) and edits it in place. Tab drops to the raw free-text textarea for
// bulk edits, and Tab returns to structural mode.
type Model struct {
	validate func([]byte) error

	mode  mode
	state editState

	root   *node
	units  []unit
	cursor int

	input    textinput.Model // inline key/scalar edit
	editPath []int
	editKey  bool

	textarea  textarea.Model // raw mode and whole-block edit
	blockPath []int

	errMsg    string
	pending   []byte
	preview   string
	committed []byte
	aborted   bool
	finished  bool

	width, height int
}

// New returns an editor seeded with initial. validate is invoked on each
// save attempt; returning a non-nil error keeps the user editing with the
// message shown inline (no work lost). If initial is not valid JSON the
// editor opens in raw mode so the user can repair it.
func New(initial []byte, validate func([]byte) error) Model {
	ta := textarea.New()
	ta.SetWidth(120)
	ta.SetHeight(20)
	ta.ShowLineNumbers = true
	// Ctrl-D is the editor's "save/commit"; the textarea would otherwise
	// use it to delete a character forward, so disable that binding.
	ta.KeyMap.DeleteCharacterForward.SetEnabled(false)

	ti := textinput.New()
	ti.Prompt = ""

	m := Model{validate: validate, textarea: ta, input: ti}

	root, err := parseTree(initial)
	if err != nil {
		// Fall back to raw mode on malformed seed JSON.
		m.mode = modeRaw
		m.errMsg = err.Error()
		m.textarea.SetValue(string(initial))
		m.textarea.Focus()
		return m
	}
	m.root = root
	m.units = collectUnits(root)
	m.mode = modeStructural
	return m
}

func (m Model) Init() tea.Cmd { return textarea.Blink }

// SetSize resizes the editor to fit the given terminal dimensions,
// reserving a few lines for the header.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.textarea.SetWidth(width)
	if height > 6 {
		m.textarea.SetHeight(height - 5)
	}
	if width > 4 {
		m.input.Width = width - 4
	}
}

// Update advances the editor. It returns the concrete Model (not
// tea.Model) so embedding code can call Finished/Committed without a type
// assertion.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(sz.Width, sz.Height)
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.forwardToActiveInput(msg)
	}
	// Ctrl-C always abandons the edit, from any state.
	if k.Type == tea.KeyCtrlC {
		m.aborted = true
		m.finished = true
		return m, nil
	}
	switch m.state {
	case stPreview:
		return m.updatePreview(k), nil
	case stInlineEdit:
		return m.updateInlineEdit(k)
	case stBlockEdit:
		return m.updateBlockEdit(k)
	default:
		if m.mode == modeRaw {
			return m.updateRaw(k)
		}
		return m.updateNavigating(k)
	}
}

// ----- structural navigation -------------------------------------------

func (m Model) updateNavigating(k tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case k.Type == tea.KeyUp || isRune(k, 'k'):
		if m.cursor > 0 {
			m.cursor--
		}
	case k.Type == tea.KeyDown || isRune(k, 'j'):
		if m.cursor < len(m.units)-1 {
			m.cursor++
		}
	case k.Type == tea.KeyEnter:
		return m.beginEdit(), nil
	case isRune(k, 'a'):
		return m.addNode(), nil
	case isRune(k, 'd'):
		return m.deleteNode(), nil
	case k.Type == tea.KeyTab:
		m.mode = modeRaw
		m.errMsg = ""
		m.textarea.SetValue(string(m.root.bytes()))
		m.textarea.Focus()
		return m, textarea.Blink
	case k.Type == tea.KeyCtrlD:
		return m.saveStructural(), nil
	case k.Type == tea.KeyEsc:
		m.aborted = true
		m.finished = true
	}
	return m, nil
}

func (m Model) beginEdit() Model {
	u := m.selected()
	switch u.typ {
	case uKey:
		key, _ := m.root.memberKeyAt(u.path)
		m.input.SetValue(key)
		m.input.CursorEnd()
		m.input.Focus()
		m.editPath = u.path
		m.editKey = true
		m.state = stInlineEdit
	case uScalar:
		m.input.SetValue(m.root.at(u.path).scalar)
		m.input.CursorEnd()
		m.input.Focus()
		m.editPath = u.path
		m.editKey = false
		m.state = stInlineEdit
	case uBlock:
		n := m.root.at(u.path)
		if n.kind == kindArray {
			// An array is a list: drill into its entries rather than
			// editing the whole [ … ] as one raw blob.
			return m.enterArray(u.path)
		}
		m.textarea.SetValue(string(n.bytes()))
		m.textarea.Focus()
		m.blockPath = u.path
		m.state = stBlockEdit
	}
	return m
}

// enterArray moves the cursor to the array's first entry so the user
// navigates and edits entries as a list. An empty array gains a first
// (null) entry, so "open" always lands on a real, editable entry.
func (m Model) enterArray(path []int) Model {
	if arr := m.root.at(path); len(arr.elems) == 0 {
		m.root.addElem(path, nullNode())
		m.rebuild()
	}
	m.cursor = m.findValueUnit(childPath(path, 0))
	return m
}

// findValueUnit returns the index of the value unit (scalar or block, not
// a key) at path, or 0 if none matches.
func (m Model) findValueUnit(path []int) int {
	for i, u := range m.units {
		if u.typ != uKey && pathEq(u.path, path) {
			return i
		}
	}
	return 0
}

func (m Model) addNode() Model {
	u := m.selected()
	var target []int
	var targetType unitType
	switch u.typ {
	case uBlock:
		block := m.root.at(u.path)
		if block.kind == kindObject {
			m.root.addMember(u.path, "newKey", nullNode())
			target = childPath(u.path, len(block.members)-1)
			targetType = uKey
		} else {
			m.root.addElem(u.path, nullNode())
			target = childPath(u.path, len(block.elems)-1)
			targetType = uScalar
		}
	default:
		// A top-level scalar has an empty path and therefore no parent
		// container to add a sibling into — adding is a no-op.
		if len(u.path) == 0 {
			return m
		}
		parent := m.root.at(u.path[:len(u.path)-1])
		if parent.kind == kindObject {
			m.root.insertSiblingAfter(u.path, "newKey", nullNode())
			targetType = uKey
		} else {
			m.root.insertSiblingAfter(u.path, "", nullNode())
			targetType = uScalar
		}
		target = childPath(u.path[:len(u.path)-1], u.path[len(u.path)-1]+1)
	}
	m.rebuild()
	m.cursor = m.findUnit(target, targetType)
	return m
}

func (m Model) deleteNode() Model {
	u := m.selected()
	if !m.root.deleteAt(u.path) {
		return m
	}
	m.rebuild()
	if m.cursor >= len(m.units) {
		m.cursor = len(m.units) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// ----- inline (key / scalar) edit --------------------------------------

func (m Model) updateInlineEdit(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		val := m.input.Value()
		if m.editKey {
			m.root.setKeyAt(m.editPath, val)
		} else {
			parsed, err := parseTree([]byte(val))
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.root.replaceValueAt(m.editPath, parsed)
		}
		m.input.Blur()
		m.errMsg = ""
		m.state = stNavigating
		m.rebuild()
		return m, nil
	case tea.KeyEsc:
		m.input.Blur()
		m.errMsg = ""
		m.state = stNavigating
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

// ----- whole-block edit ------------------------------------------------

func (m Model) updateBlockEdit(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlD:
		parsed, err := parseTree([]byte(m.textarea.Value()))
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.root.replaceValueAt(m.blockPath, parsed)
		m.errMsg = ""
		m.state = stNavigating
		m.rebuild()
		return m, nil
	case tea.KeyEsc:
		m.errMsg = ""
		m.state = stNavigating
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(k)
	return m, cmd
}

// ----- raw free-text mode ----------------------------------------------

func (m Model) updateRaw(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyTab:
		parsed, err := parseTree([]byte(m.textarea.Value()))
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.root = parsed
		m.rebuild()
		if m.cursor >= len(m.units) {
			m.cursor = len(m.units) - 1
		}
		m.mode = modeStructural
		m.errMsg = ""
		return m, nil
	case tea.KeyCtrlD:
		return m.save([]byte(m.textarea.Value())), nil
	case tea.KeyEsc:
		m.aborted = true
		m.finished = true
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(k)
	return m, cmd
}

// ----- save / preview --------------------------------------------------

// saveStructural commits the structurally-edited tree immediately. The
// tree is always syntactically valid JSON and the colored view already
// shows exactly what will be written, so there is nothing for a preview to
// confirm — only the caller's validate hook can still reject the document,
// in which case the message is shown inline and the user keeps editing.
func (m Model) saveStructural() Model {
	canonical := m.root.bytes()
	if err := m.validate(canonical); err != nil {
		m.errMsg = err.Error()
		return m
	}
	m.errMsg = ""
	m.committed = canonical
	m.finished = true
	return m
}

// save validates candidate JSON and, on success, shows the canonical
// preview for confirmation. It backs raw mode, where the free-text buffer
// can be malformed and a preview is a useful last check. On failure the
// message is shown inline and the user keeps editing.
func (m Model) save(candidate []byte) Model {
	parsed, err := parseTree(candidate)
	if err != nil {
		m.errMsg = err.Error()
		return m
	}
	canonical := parsed.bytes()
	if err := m.validate(canonical); err != nil {
		m.errMsg = err.Error()
		return m
	}
	m.errMsg = ""
	m.pending = canonical
	m.preview = highlightJSON(canonical)
	m.state = stPreview
	return m
}

func (m Model) updatePreview(k tea.KeyMsg) Model {
	switch k.Type {
	case tea.KeyEnter, tea.KeyCtrlD:
		m.committed = m.pending
		m.finished = true
	case tea.KeyEsc:
		m.preview = ""
		m.pending = nil
		m.state = stNavigating
	}
	return m
}

// ----- helpers ---------------------------------------------------------

func (m Model) forwardToActiveInput(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case m.state == stInlineEdit:
		m.input, cmd = m.input.Update(msg)
	case m.state == stBlockEdit || m.mode == modeRaw:
		m.textarea, cmd = m.textarea.Update(msg)
	}
	return m, cmd
}

func (m Model) selected() unit {
	if m.cursor < 0 || m.cursor >= len(m.units) {
		return unit{}
	}
	return m.units[m.cursor]
}

// rebuild recomputes the selectable units after a tree mutation.
func (m *Model) rebuild() { m.units = collectUnits(m.root) }

// findUnit returns the index of the unit at path with the given type, or
// 0 if none matches.
func (m Model) findUnit(path []int, typ unitType) int {
	for i, u := range m.units {
		if u.typ == typ && pathEq(u.path, path) {
			return i
		}
	}
	return 0
}

func nullNode() *node { return &node{kind: kindNull, scalar: "null"} }

func isRune(k tea.KeyMsg, r rune) bool {
	return k.Type == tea.KeyRunes && len(k.Runes) == 1 && k.Runes[0] == r
}

// Finished reports whether the user has committed or aborted.
func (m Model) Finished() bool { return m.finished }

// Aborted reports whether the user abandoned the edit.
func (m Model) Aborted() bool { return m.aborted }

// Editing reports whether the editor is in a transient sub-edit (editing a
// key/scalar inline or a whole block). Embedding code uses this to decide
// whether Esc should cancel that sub-edit or back out of the editor.
func (m Model) Editing() bool {
	return m.state == stInlineEdit || m.state == stBlockEdit
}

// Committed returns the canonical JSON the user confirmed, or nil if the
// edit was aborted or is still in progress.
func (m Model) Committed() []byte { return m.committed }

// Value returns the document's current canonical JSON, reflecting in-progress
// edits without requiring a commit. It lets embedding code (e.g. a larger
// form) read the live document at any time. In raw mode it canonicalizes the
// textarea contents, falling back to the raw bytes if they are not yet valid
// JSON.
func (m Model) Value() []byte {
	if m.mode == modeRaw {
		if t, err := parseTree([]byte(m.textarea.Value())); err == nil {
			return t.bytes()
		}
		return []byte(m.textarea.Value())
	}
	return m.root.bytes()
}

// ----- view ------------------------------------------------------------

func (m Model) View() string {
	if m.state == stPreview {
		return "Preview — Enter or Ctrl-D to confirm, Esc to keep editing\n\n" + m.preview
	}
	if m.mode == modeRaw {
		return m.header(progname.Get()+" edit (raw) — Ctrl-D validate & preview · Tab structural · Esc abort") + m.ContentView()
	}
	if m.state == stBlockEdit {
		return m.header(progname.Get()+" edit (block) — Ctrl-D apply · Esc cancel") + m.ContentView()
	}
	if m.state == stInlineEdit {
		label := "value"
		if m.editKey {
			label = "key"
		}
		return m.header(progname.Get()+" edit ("+label+") — Enter apply · Esc cancel") + m.ContentView()
	}
	return m.header(progname.Get()+" edit — ↑/↓ move · Enter edit · a add · d delete · Tab raw · Ctrl-D save · Esc abort") + m.ContentView()
}

// ContentView renders just the editor body for the current state, without
// the header line. Embedding code (e.g. the node-edit form) uses it to place
// the editor under its own label instead of the standalone header.
func (m Model) ContentView() string {
	switch {
	case m.mode == modeRaw || m.state == stBlockEdit:
		return m.textarea.View()
	case m.state == stInlineEdit:
		return m.structuralView() + "\n\n› " + m.input.View()
	default:
		return m.structuralView()
	}
}

func (m Model) header(line string) string {
	if m.errMsg != "" {
		return line + "\nError: " + m.errMsg + "\n\n"
	}
	return line + "\n\n"
}

// structuralView renders the tree with the selected unit highlighted,
// windowed so the selection stays on screen.
func (m Model) structuralView() string {
	u := m.selected()
	full := render(m.root, u, syntaxTheme())
	focus := selectedLine(m.root, u)
	return windowLines(full, focus, m.viewHeight())
}

func (m Model) viewHeight() int {
	if m.height <= 0 {
		return 1 << 30 // unbounded: show everything
	}
	if h := m.height - 4; h > 1 {
		return h
	}
	return 1
}

// selectedLine returns the 0-based line on which the selected unit's
// highlight begins, computed by re-rendering with a sentinel theme that
// marks only the selected unit and leaves every other token untouched.
func selectedLine(root *node, u unit) int {
	const sentinel = "\x00"
	th := renderTheme{
		key: identityStr, str: identityStr, num: identityStr,
		lit: identityStr, punct: identityStr,
		sel: func(x string) string { return sentinel + x },
	}
	s := render(root, u, th)
	before, _, found := strings.Cut(s, sentinel)
	if !found {
		return 0
	}
	return strings.Count(before, "\n")
}

func identityStr(s string) string { return s }

// windowLines returns at most height lines of text, scrolled so focus is
// visible.
func windowLines(text string, focus, height int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= height {
		return text
	}
	start := max(focus-height/2, 0)
	if start+height > len(lines) {
		start = len(lines) - height
	}
	return strings.Join(lines[start:start+height], "\n")
}
