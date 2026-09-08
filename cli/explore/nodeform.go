package explore

import (
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/nodeedit"
)

// nodeFormAdapter wraps the nodeedit form as a subEditor, so editing or
// creating a node in the explorer uses the same human-field-plus-attributes
// form as `cinc node edit` instead of raw JSON.
type nodeFormAdapter struct{ m nodeedit.Model }

// newNodeForm builds the node form for an edit (seed is the node's JSON) or a
// create (seed is ignored; the form starts blank with an editable name).
func newNodeForm(action editAction, seed []byte) (subEditor, error) {
	var (
		m   nodeedit.Model
		err error
	)
	if action == actionCreate {
		m, err = nodeedit.NewCreate()
	} else {
		var n cinc.Node
		if err = json.Unmarshal(seed, &n); err != nil {
			return nil, err
		}
		m, err = nodeedit.New(&n)
	}
	if err != nil {
		return nil, err
	}
	return &nodeFormAdapter{m: m}, nil
}

func (a *nodeFormAdapter) Init() tea.Cmd { return a.m.Init() }

func (a *nodeFormAdapter) Update(msg tea.Msg) (subEditor, tea.Cmd) {
	var cmd tea.Cmd
	a.m, cmd = a.m.Update(msg)
	return a, cmd
}

func (a *nodeFormAdapter) View() string      { return a.m.View() }
func (a *nodeFormAdapter) SetSize(w, h int)  { a.m.SetSize(w, h) }
func (a *nodeFormAdapter) Finished() bool    { return a.m.Finished() }
func (a *nodeFormAdapter) Aborted() bool     { return a.m.Aborted() }
func (a *nodeFormAdapter) Committed() []byte { return a.m.Committed() }
