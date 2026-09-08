package nodeedit

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"
)

// Run drives the node-edit form as a standalone full-screen program. It
// returns the edited node and whether it differs from the input. A cancelled
// edit returns an error.
func Run(node *cinc.Node) (*cinc.Node, bool, error) {
	m, err := New(node)
	if err != nil {
		return nil, false, err
	}
	final, err := tea.NewProgram(runner{m: m}, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, false, fmt.Errorf("cinc: node edit failed: %w", err)
	}
	r := final.(runner)
	if r.m.aborted {
		return nil, false, fmt.Errorf("cinc: edit aborted")
	}
	return r.m.result, r.m.changed, nil
}

// runner adapts the form Model to a standalone tea.Program, quitting once
// the user saves or cancels.
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
