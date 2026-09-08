package explore

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	sm "github.com/cinc-project/cinc-supermarket-api"
)

// viewID names the two screens the TUI flips between.
type viewID int

const (
	viewBrowse viewID = iota
	viewDetail
)

// model is the root bubbletea model. It owns the API client, both
// sub-screens, and a monotonic request counter so stale responses can
// be discarded.
type model struct {
	ctx    context.Context
	client apiClient
	site   string

	view   viewID
	width  int
	height int

	browse browseState
	detail detailState

	openInBrowser func(string) error // injected so tests don't shell out
	openErr       string             // last "open in browser" error, shown in footer

	// install downloads a cookbook from Supermarket and uploads it to the
	// configured Cinc Server. It's injected (and may be nil) so the TUI
	// stays credential-free until the user actually asks to install — and
	// so tests can drive the flow without touching a server.
	install func(ctx context.Context, name, version string) error

	// reqID is a monotonic counter stamped onto every in-flight
	// command. Responses with a stale ID are dropped. Bubbletea
	// dispatches Update serially, so a plain uint64 is sufficient —
	// no atomic needed (and would in fact violate tea.Model's
	// pass-by-value contract).
	reqID uint64

	styles styles
	keys   keyMap
}

// browseState is everything the list screen owns. Search and list
// modes share most fields; query is empty in list mode.
type browseState struct {
	mode  listMode
	sort  sortOrder
	query string

	searchFocused bool
	searchInput   textinput.Model

	items   []sm.CookbookSummary
	cursor  int
	start   int
	total   int
	hasMore bool

	loading        bool
	loadingMore    bool
	previewBusy    bool
	debounceID     uint64
	previewID      uint64
	lastErr        string
	previewLastErr string

	preview      *sm.Cookbook
	previewName  string
	previewCache map[string]*sm.Cookbook

	// install flow state. confirmInstall gates the y/n prompt;
	// installing marks an upload in flight; installMsg / installErr hold
	// the last result for the footer.
	confirmInstall bool
	installName    string
	installing     bool
	installMsg     string
	installErr     string
}

// detailState owns the full-screen cookbook view.
type detailState struct {
	name     string
	cookbook *sm.Cookbook
	viewport viewport.Model
	loading  bool
	err      string
	ready    bool // first WindowSize received → viewport initialized
}

// initialModel wires a fresh model with sensible defaults. ctx, client
// and the openURL hook are all injected so the unit tests can run the
// model end-to-end without launching a real bubbletea program.
func initialModel(ctx context.Context, client apiClient, site string, openURL func(string) error, install func(context.Context, string, string) error) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "type to search…"
	ti.CharLimit = 100

	return model{
		ctx:    ctx,
		client: client,
		site:   site,
		view:   viewBrowse,
		browse: browseState{
			mode:         modeList,
			sort:         sortMostDownloaded,
			searchInput:  ti,
			previewCache: map[string]*sm.Cookbook{},
		},
		openInBrowser: openURL,
		install:       install,
		styles:        newStyles(),
		keys:          newKeyMap(),
	}
}

// nextReqID hands out a monotonically increasing request identifier so
// every in-flight command captures its own stamp; responses with stale
// IDs are dropped on arrival.
func (m *model) nextReqID() uint64 {
	m.reqID++
	return m.reqID
}

// ----- messages ---------------------------------------------------------

type cookbooksLoadedMsg struct {
	reqID  uint64
	mode   listMode
	sort   sortOrder
	query  string
	append bool
	page   sm.Page[sm.CookbookSummary]
	err    error
}

type cookbookDetailMsg struct {
	reqID    uint64
	name     string
	cookbook *sm.Cookbook
	err      error
	target   detailTarget
}

type detailTarget int

const (
	targetPreview detailTarget = iota
	targetDetail
)

type debounceTickMsg struct {
	reqID uint64
	query string
}

type previewTickMsg struct {
	reqID uint64
	name  string
}

// installDoneMsg reports the outcome of an install the user confirmed.
type installDoneMsg struct {
	name    string
	version string
	err     error
}

// ----- commands ---------------------------------------------------------

// loadCookbooks fires a list-mode request.
func loadCookbooks(ctx context.Context, c apiClient, order sortOrder, start int, reqID uint64, appendMode bool) tea.Cmd {
	return func() tea.Msg {
		page, err := c.List(ctx, sm.ListOptions{
			Order: order.orderParam(),
			Start: start,
			Items: pageSize,
		})
		return cookbooksLoadedMsg{
			reqID: reqID, mode: modeList, sort: order, append: appendMode,
			page: page, err: err,
		}
	}
}

// searchCookbooks fires a search-mode request.
func searchCookbooks(ctx context.Context, c apiClient, query string, start int, reqID uint64, appendMode bool) tea.Cmd {
	return func() tea.Msg {
		page, err := c.Search(ctx, sm.SearchOptions{
			Q: query, Start: start, Items: pageSize,
		})
		return cookbooksLoadedMsg{
			reqID: reqID, mode: modeSearch, query: query, append: appendMode,
			page: page, err: err,
		}
	}
}

// loadDetail fetches one cookbook for either the preview pane or the
// detail screen, depending on `target`.
func loadDetail(ctx context.Context, c apiClient, name string, reqID uint64, target detailTarget) tea.Cmd {
	return func() tea.Msg {
		cb, err := c.Get(ctx, name)
		return cookbookDetailMsg{
			reqID: reqID, name: name, cookbook: cb, err: err, target: target,
		}
	}
}

// debounce queues a tick that fires after d; the consumer checks
// reqID against the current model state to decide whether to act.
func debounce(d time.Duration, reqID uint64, query string) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return debounceTickMsg{reqID: reqID, query: query}
	})
}

func previewDebounce(d time.Duration, reqID uint64, name string) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return previewTickMsg{reqID: reqID, name: name}
	})
}

// installCookbook runs the injected install function off the UI thread and
// reports the result as an installDoneMsg.
func installCookbook(ctx context.Context, fn func(context.Context, string, string) error, name, version string) tea.Cmd {
	return func() tea.Msg {
		err := fn(ctx, name, version)
		return installDoneMsg{name: name, version: version, err: err}
	}
}

// ----- tea.Model --------------------------------------------------------

func (m model) Init() tea.Cmd {
	id := uint64(1) // first request; subsequent requests use nextReqID via Update copies
	return loadCookbooks(m.ctx, m.client, m.browse.sort, 0, id, false)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.detail.ready {
			m.detail.viewport = viewport.New(msg.Width, max(1, msg.Height-4))
			m.detail.ready = true
		} else {
			m.detail.viewport.Width = msg.Width
			m.detail.viewport.Height = max(1, msg.Height-4)
		}
		return m, nil
	case installDoneMsg:
		// Handled here, not per-view, so the outcome is still recorded if
		// the user drilled into a detail screen while the upload ran. It
		// surfaces in the browse footer, where installs are started.
		return applyInstallDone(m, msg)
	}

	switch m.view {
	case viewDetail:
		return updateDetail(m, msg)
	default:
		return updateBrowse(m, msg)
	}
}

func (m model) View() string {
	switch m.view {
	case viewDetail:
		return viewDetailScreen(m)
	default:
		return viewBrowseScreen(m)
	}
}
