// Package jsoneditor is a small bubbletea component that edits a JSON
// document and shows a syntax-highlighted preview before committing. It
// is shared by the `cinc <noun> edit` commands (via Run, which drives it
// as a standalone program) and by the `cinc explore` TUI (which embeds the
// Model as a screen).
//
// The editor is JSON-aware: by default it presents a structural cursor
// (Model in model.go) that selects and edits keys, scalar values, and
// whole {}/[] blocks, with a raw free-text mode a Tab away.
//
// cinc ships its own editor rather than shelling out to $VISUAL /
// $EDITOR so the experience is consistent across systems and works in
// minimal environments without an editor on PATH.
package jsoneditor

import (
	"bytes"
	"fmt"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
)

// Run drives the editor as a standalone full-screen program and returns
// the committed JSON. It is used by the `cinc <noun> edit` commands.
func Run(initial []byte, validate func([]byte) error) ([]byte, error) {
	if !HaveTerminal() {
		return nil, ErrNoTerminal
	}
	final, err := tea.NewProgram(runner{m: New(initial, validate)}, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, fmt.Errorf("cinc: edit failed: %w", err)
	}
	r := final.(runner)
	if r.m.aborted {
		return nil, fmt.Errorf("cinc: edit aborted")
	}
	return r.m.committed, nil
}

// runner adapts the embeddable Model to a standalone tea.Program,
// quitting once the user finishes.
type runner struct{ m Model }

func (r runner) Init() tea.Cmd { return r.m.Init() }

func (r runner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	r.m, cmd = r.m.Update(msg)
	if r.m.finished {
		return r, tea.Quit
	}
	return r, cmd
}

func (r runner) View() string { return r.m.View() }

// highlightJSON wraps b in chroma's terminal256 JSON highlighting. If any
// step fails the raw bytes are returned so the preview is still readable.
func highlightJSON(b []byte) string {
	lexer := lexers.Get("json")
	if lexer == nil {
		return string(b)
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return string(b)
	}
	iterator, err := lexer.Tokenise(nil, string(b))
	if err != nil {
		return string(b)
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return string(b)
	}
	return buf.String()
}
