package explore

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// summaryDebounce is how long the cursor must settle on a row before the
// split-screen panel fetches that object, so fast scrolling doesn't fire
// a request per row.
const summaryDebounce = 200 * time.Millisecond

// screen is the active full-screen view. Editor, confirm, name, and
// result are modal overlays that remember the screen to return to.
type screen int

const (
	screenProfiles screen = iota
	screenKinds
	screenList
	screenDetail
	screenEditor
	screenConfirm
	screenName
	screenResult
	screenSearch
	screenSearchIndex
)

// editAction distinguishes the two ways the JSON editor is used.
type editAction int

const (
	actionEdit editAction = iota
	actionCreate
)

// navFrame is one level of the drill-down stack: the parent kind and
// the list position to restore when the user backs out.
type navFrame struct {
	kind   Kind
	rows   []Row
	cursor int
}

// model is the root bubbletea model for `cinc explore`.
type model struct {
	ctx         context.Context
	opts        Options
	client      *cinc.Client // nil until a profile is chosen
	profileName string

	// server identity shown in the title bar
	serverHost    string // hostname[:port] of the connected server
	serverVersion string // e.g. "API v2"; empty until the probe returns

	screen screen
	width  int
	height int

	// profile picker
	profileNames  []string
	profileCursor int

	// kind menu
	kinds      []Kind
	kindCursor int

	// drill-down stack and current list view
	stack   []navFrame
	cur     Kind
	rows    []Row // unfiltered rows for cur
	cursor  int   // index into the filtered rows
	loading bool
	listErr string

	// list filtering
	filtering bool
	filter    textinput.Model

	// server search: when active, the list is restricted to the identities
	// the server's search index returned for searchQuery against searchIndex.
	searchActive bool
	searchIndex  string
	searchQuery  string
	searchNames  map[string]bool

	// search modal: searchInput holds the query being composed, searchKind
	// the index it will hit; searchKinds/searchKindCur drive the index picker.
	searchInput   textinput.Model
	searchKind    Kind
	searchKinds   []Kind
	searchKindCur int

	// split-screen summary panel: the right pane's per-row object summary,
	// cached by name and cleared when the list changes.
	summaryCache map[string]summaryView
	summaryErr   string
	debounceID   uint64 // monotonic; only the latest scroll's tick acts

	// detail view
	detail      viewport.Model
	detailReady bool
	detailName  string
	detailErr   string

	// editor modal: the generic JSON editor or a kind's typed form, behind
	// the subEditor interface.
	editor   subEditor
	editKind editAction
	editName string

	// returnTo is the screen a modal returns to when cancelled.
	returnTo screen

	// confirm modal
	confirmPrompt string
	pending       func() tea.Cmd

	// name-prompt modal
	namePrompt string
	nameInput  textinput.Model
	nameAction func(string) tea.Cmd

	// result modal (one-time secrets etc.)
	resultTitle string
	resultBody  string

	// reqID stamps every async request; stale responses are dropped.
	// Bubbletea dispatches Update serially so a plain counter is safe.
	reqID uint64

	status string // transient success line shown in the footer
	styles styles
	keys   keyMap

	// colorJSON enables syntax highlighting of the JSON panes; resolved once
	// from the terminal's color support so non-TTY output stays plain.
	colorJSON bool
}

// startup carries the resolved entry state from Run into the model.
type startup struct {
	client       *cinc.Client
	profileName  string
	screen       screen
	profileNames []string
}

func newModel(ctx context.Context, opts Options, s startup) model {
	filter := textinput.New()
	filter.Prompt = "/"
	filter.Placeholder = "filter…"

	name := textinput.New()
	name.Prompt = "› "

	search := textinput.New()
	search.Prompt = "› "
	search.Placeholder = "name:web* AND chef_environment:prod…"

	return model{
		ctx:          ctx,
		opts:         opts,
		client:       s.client,
		profileName:  s.profileName,
		serverHost:   hostFromProfile(opts, s.profileName),
		screen:       s.screen,
		profileNames: s.profileNames,
		kinds:        registry(),
		filter:       filter,
		nameInput:    name,
		searchInput:  search,
		styles:       newStyles(),
		keys:         newKeyMap(),
		colorJSON:    colorize(newStyles()),
	}
}

// hostFromProfile pulls the server hostname[:port] out of a profile's
// server URL, for display in the title bar. It returns "" when the
// profile or URL is missing or unparseable.
func hostFromProfile(opts Options, name string) string {
	p, ok := opts.Profiles[name]
	if !ok {
		return ""
	}
	u, err := url.Parse(p.ServerURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func (m *model) nextReqID() uint64 {
	m.reqID++
	return m.reqID
}

// ----- messages --------------------------------------------------------

type clientReadyMsg struct {
	name   string
	client *cinc.Client
	err    error
}

type listLoadedMsg struct {
	reqID uint64
	rows  []Row
	err   error
}

type detailLoadedMsg struct {
	reqID uint64
	name  string
	body  string
	err   error
}

type editSeedMsg struct {
	reqID uint64
	name  string
	json  string
	err   error
}

type mutationDoneMsg struct {
	verb   string // "Updated", "Created", "Deleted"
	name   string
	secret string
	err    error
}

type downloadDoneMsg struct {
	name string
	path string
	err  error
}

type serverInfoMsg struct {
	version string
}

// summaryTickMsg fires when the debounce window elapses; id ties it to
// the selection that scheduled it so stale ticks are ignored.
type summaryTickMsg struct {
	id   uint64
	name string
}

type summaryLoadedMsg struct {
	name string
	view summaryView
	err  error
}

// searchLoadedMsg carries the outcome of a server search: the identities
// that matched, plus the index/query/kind they belong to so the list can
// switch to and label them.
type searchLoadedMsg struct {
	reqID uint64
	index string
	query string
	kind  Kind
	names map[string]bool
	err   error
}

// ----- commands --------------------------------------------------------

func buildClientCmd(opts Options, name string, profile config.Profile) tea.Cmd {
	return func() tea.Msg {
		c, err := opts.NewClient(profile)
		return clientReadyMsg{name: name, client: c, err: err}
	}
}

func listCmd(ctx context.Context, c *cinc.Client, k Kind, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		rows, err := k.List(ctx, c)
		return listLoadedMsg{reqID: reqID, rows: rows, err: err}
	}
}

func describeCmd(ctx context.Context, c *cinc.Client, v Viewable, name string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		body, err := v.Describe(ctx, c, name)
		return detailLoadedMsg{reqID: reqID, name: name, body: body, err: err}
	}
}

func editSeedCmd(ctx context.Context, c *cinc.Client, v Viewable, name string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		body, err := v.Describe(ctx, c, name)
		return editSeedMsg{reqID: reqID, name: name, json: body, err: err}
	}
}

func saveCmd(ctx context.Context, c *cinc.Client, e Editable, name string, edited []byte) tea.Cmd {
	return func() tea.Msg {
		err := e.Save(ctx, c, name, edited)
		return mutationDoneMsg{verb: "Updated", name: name, err: err}
	}
}

func createCmd(ctx context.Context, c *cinc.Client, cr Creatable, doc []byte) tea.Cmd {
	return func() tea.Msg {
		res, err := cr.Create(ctx, c, doc)
		return mutationDoneMsg{verb: "Created", name: res.Name, secret: res.Secret, err: err}
	}
}

func createNamedCmd(ctx context.Context, c *cinc.Client, cr NamedCreatable, name string) tea.Cmd {
	return func() tea.Msg {
		res, err := cr.CreateNamed(ctx, c, name)
		return mutationDoneMsg{verb: "Created", name: res.Name, secret: res.Secret, err: err}
	}
}

func deleteCmd(ctx context.Context, c *cinc.Client, d Deletable, name string) tea.Cmd {
	return func() tea.Msg {
		err := d.Delete(ctx, c, name)
		return mutationDoneMsg{verb: "Deleted", name: name, err: err}
	}
}

func downloadCmd(ctx context.Context, c *cinc.Client, d Downloadable, name, dir string) tea.Cmd {
	return func() tea.Msg {
		path, err := d.Download(ctx, c, name, dir)
		return downloadDoneMsg{name: name, path: path, err: err}
	}
}

// summaryCmd fetches the selected object's summary for the right pane.
func summaryCmd(ctx context.Context, s Summarizable, c *cinc.Client, name string) tea.Cmd {
	return func() tea.Msg {
		view, err := s.Summary(ctx, c, name)
		return summaryLoadedMsg{name: name, view: view, err: err}
	}
}

// searchCmd runs a server search and reduces the matches to the set of
// identities the list filters by. kind/index/query ride along so the
// handler can switch the view to that index and render the banner.
func searchCmd(ctx context.Context, c *cinc.Client, kind Kind, index, query string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		rows, err := c.Search.SearchAll(ctx, index, query)
		if err != nil {
			return searchLoadedMsg{reqID: reqID, index: index, query: query, kind: kind, err: err}
		}
		names := make(map[string]bool, len(rows))
		for _, raw := range rows {
			if id := searchRowIdentity(raw); id != "" {
				names[id] = true
			}
		}
		return searchLoadedMsg{reqID: reqID, index: index, query: query, kind: kind, names: names}
	}
}

// searchRowIdentity pulls a search hit's list identity: its name, or its
// id for objects keyed by id (data bag items).
func searchRowIdentity(raw json.RawMessage) string {
	var o struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return ""
	}
	if o.Name != "" {
		return o.Name
	}
	return o.ID
}

// serverInfoCmd makes one cheap authenticated request and reads the
// server's Chef API version out of the X-Ops-Server-Api-Version response
// header. A failure just leaves the version blank — it's title-bar trim,
// not load-bearing.
func serverInfoCmd(ctx context.Context, c *cinc.Client) tea.Cmd {
	return func() tea.Msg {
		_, resp, err := c.Nodes.List(ctx)
		if err != nil || resp == nil || resp.HTTPResponse == nil {
			return serverInfoMsg{}
		}
		return serverInfoMsg{version: parseAPIVersion(resp.HTTPResponse.Header.Get("X-Ops-Server-Api-Version"))}
	}
}

// parseAPIVersion turns a Chef X-Ops-Server-Api-Version header value
// (JSON like {"max_version":"2",…}) into a short label like "API v2".
func parseAPIVersion(header string) string {
	if header == "" {
		return ""
	}
	var v struct {
		MaxVersion string `json:"max_version"`
	}
	if err := json.Unmarshal([]byte(header), &v); err != nil || v.MaxVersion == "" {
		return ""
	}
	return "API v" + v.MaxVersion
}

// ----- tea.Model -------------------------------------------------------

func (m model) Init() tea.Cmd {
	// When a profile is resolved up front (single profile or --profile),
	// probe the server version immediately; the picker path fires it on
	// client-ready instead.
	if m.client != nil {
		return serverInfoCmd(m.ctx, m.client)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg), nil
	case clientReadyMsg:
		return m.handleClientReady(msg)
	case listLoadedMsg:
		m = m.handleListLoaded(msg)
		return m, m.scheduleSummary()
	case detailLoadedMsg:
		return m.handleDetailLoaded(msg), nil
	case editSeedMsg:
		return m.handleEditSeed(msg)
	case mutationDoneMsg:
		return m.handleMutationDone(msg)
	case downloadDoneMsg:
		return m.handleDownloadDone(msg), nil
	case serverInfoMsg:
		m.serverVersion = msg.version
		return m, nil
	case summaryTickMsg:
		return m.handleSummaryTick(msg)
	case summaryLoadedMsg:
		return m.handleSummaryLoaded(msg), nil
	case searchLoadedMsg:
		return m.handleSearchLoaded(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	// Forward other messages (cursor blink, etc.) to whichever modal owns
	// a sub-component.
	return m.forwardToActive(msg)
}

func (m model) View() string {
	switch m.screen {
	case screenProfiles:
		return m.viewProfiles()
	case screenKinds:
		return m.viewKinds()
	case screenDetail:
		return m.viewDetail()
	case screenEditor:
		return m.editor.View()
	case screenConfirm:
		return m.viewConfirm()
	case screenName:
		return m.viewName()
	case screenResult:
		return m.viewResult()
	case screenSearch:
		return m.viewSearch()
	case screenSearchIndex:
		return m.viewSearchIndex()
	default:
		return m.viewList()
	}
}

// ----- resize ----------------------------------------------------------

func (m model) handleResize(msg tea.WindowSizeMsg) model {
	m.width, m.height = msg.Width, msg.Height
	// The detail viewport lives inside the bordered frame's body area, so
	// size it to the inner width and the body height frame() leaves it.
	vpW := max(1, msg.Width-2)
	vpH := m.bodyHeight()
	if !m.detailReady {
		m.detail = viewport.New(vpW, vpH)
		m.detailReady = true
	} else {
		m.detail.Width = vpW
		m.detail.Height = vpH
	}
	// The editor is only constructed when opened; resize it only while
	// it's the active screen to avoid touching a zero-value textarea.
	if m.screen == screenEditor && m.editor != nil {
		m.editor.SetSize(msg.Width, msg.Height)
	}
	return m
}

// forwardToActive routes non-key messages to the sub-component of the
// active modal screen.
func (m model) forwardToActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenEditor:
		m.editor, cmd = m.editor.Update(msg)
	case screenName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case screenSearch:
		m.searchInput, cmd = m.searchInput.Update(msg)
	case screenList:
		if m.filtering {
			m.filter, cmd = m.filter.Update(msg)
		}
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	}
	return m, cmd
}

// ----- async message handlers ------------------------------------------

func (m model) handleClientReady(msg clientReadyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.listErr = msg.err.Error()
		m.screen = screenProfiles
		return m, nil
	}
	m.client = msg.client
	m.profileName = msg.name
	m.serverHost = hostFromProfile(m.opts, msg.name)
	m.serverVersion = ""
	m.listErr = ""
	m.screen = screenKinds
	return m, serverInfoCmd(m.ctx, m.client)
}

func (m model) handleListLoaded(msg listLoadedMsg) model {
	if msg.reqID != m.reqID {
		return m // stale
	}
	m.loading = false
	if msg.err != nil {
		m.listErr = msg.err.Error()
		m.rows = nil
		return m
	}
	m.listErr = ""
	m.rows = msg.rows
	if m.cursor >= len(m.filteredRows()) {
		m.cursor = max(0, len(m.filteredRows())-1)
	}
	return m
}

func (m model) handleDetailLoaded(msg detailLoadedMsg) model {
	if msg.reqID != m.reqID {
		return m
	}
	if msg.err != nil {
		m.detailErr = msg.err.Error()
		m.detail.SetContent("")
		return m
	}
	m.detailErr = ""
	m.detailName = msg.name
	m.detail.SetContent(highlightJSON(m.colorJSON, msg.body))
	m.detail.GotoTop()
	return m
}

func (m model) handleEditSeed(msg editSeedMsg) (tea.Model, tea.Cmd) {
	if msg.reqID != m.reqID {
		return m, nil
	}
	if msg.err != nil {
		m.listErr = msg.err.Error()
		m.screen = screenList
		return m, nil
	}
	m.openEditor(actionEdit, msg.name, []byte(msg.json))
	return m, m.editor.Init()
}

func (m model) handleMutationDone(msg mutationDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.listErr = msg.err.Error()
		m.screen = screenList
		return m, nil
	}
	if msg.secret != "" {
		m.resultTitle = msg.verb + " " + msg.name
		m.resultBody = "One-time private key — copy it now, it will not be shown again:\n\n" + msg.secret
		m.screen = screenResult
		m.status = ""
		cmd := m.reloadList()
		return m, cmd
	}
	m.status = msg.verb + " " + msg.name
	m.screen = screenList
	cmd := m.reloadList()
	return m, cmd
}

// scheduleSummary arms the debounce for the currently selected row. It
// returns nil when there's nothing to fetch (no row, already cached, or a
// kind that isn't Summarizable); otherwise a tick that, once it survives
// the debounce window, drives the fetch.
func (m *model) scheduleSummary() tea.Cmd {
	row, ok := m.selectedRow()
	if !ok {
		return nil
	}
	if _, cached := m.summaryCache[row.Name]; cached {
		m.summaryErr = ""
		return nil
	}
	if _, ok := m.cur.(Summarizable); !ok {
		return nil
	}
	m.debounceID++
	id, name := m.debounceID, row.Name
	return tea.Tick(summaryDebounce, func(time.Time) tea.Msg {
		return summaryTickMsg{id: id, name: name}
	})
}

// handleSummaryTick fires the fetch only if this tick is still the latest
// scheduled one and its row is still selected and uncached.
func (m model) handleSummaryTick(msg summaryTickMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.debounceID {
		return m, nil
	}
	row, ok := m.selectedRow()
	if !ok || row.Name != msg.name {
		return m, nil
	}
	if _, cached := m.summaryCache[msg.name]; cached {
		return m, nil
	}
	s, ok := m.cur.(Summarizable)
	if !ok {
		return m, nil
	}
	return m, summaryCmd(m.ctx, s, m.client, msg.name)
}

// handleSummaryLoaded caches a fetched summary. Caching by name is always
// safe: the panel renders whatever the currently selected row maps to, so
// a result that arrives after the cursor moved on simply waits in cache.
func (m model) handleSummaryLoaded(msg summaryLoadedMsg) model {
	row, current := m.selectedRow()
	isCurrent := current && row.Name == msg.name
	if msg.err != nil {
		if isCurrent {
			m.summaryErr = msg.err.Error()
		}
		return m
	}
	if m.summaryCache == nil {
		m.summaryCache = map[string]summaryView{}
	}
	m.summaryCache[msg.name] = msg.view
	if isCurrent {
		m.summaryErr = ""
	}
	return m
}

func (m model) handleDownloadDone(msg downloadDoneMsg) model {
	if msg.err != nil {
		m.listErr = msg.err.Error()
		return m
	}
	m.status = "Downloaded " + msg.name + " to " + msg.path
	return m
}

// ----- shared list helpers ---------------------------------------------

// filteredRows returns the rows matching the active filter (case
// -insensitive substring on Name). With no filter it returns all rows.
func (m model) filteredRows() []Row {
	rows := m.rows
	// An active server search restricts the list to the matched identities;
	// the text filter (if any) then narrows further within those.
	if m.searchActive {
		kept := make([]Row, 0, len(rows))
		for _, r := range rows {
			if m.searchNames[r.Name] {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	q := strings.TrimSpace(strings.ToLower(m.filter.Value()))
	if q == "" {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), q) {
			out = append(out, r)
		}
	}
	return out
}

func (m model) selectedRow() (Row, bool) {
	rows := m.filteredRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return Row{}, false
	}
	return rows[m.cursor], true
}

// openList switches to a kind's list and fires the load. It does not
// touch the drill-down stack — callers manage that.
func (m *model) openList(k Kind) tea.Cmd {
	m.cur = k
	m.rows = nil
	m.cursor = 0
	m.loading = true
	m.listErr = ""
	m.status = ""
	m.filtering = false
	m.filter.SetValue("")
	m.clearSearchState()
	m.summaryCache = map[string]summaryView{}
	m.summaryErr = ""
	m.screen = screenList
	return listCmd(m.ctx, m.client, k, m.nextReqID())
}

// clearSearchState drops any active server search so a plain list shows
// all of its rows again.
func (m *model) clearSearchState() {
	m.searchActive = false
	m.searchNames = nil
	m.searchIndex = ""
	m.searchQuery = ""
}

// handleSearchLoaded installs a server search's result set as the active
// list filter. A stale response (a newer request was issued) is dropped.
func (m model) handleSearchLoaded(msg searchLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.reqID != m.reqID {
		return m, nil
	}
	if msg.err != nil {
		m.loading = false
		m.listErr = msg.err.Error()
		m.screen = screenList
		return m, nil
	}
	return m, m.applySearch(msg.kind, msg.index, msg.query, msg.names)
}

// applySearch switches the view to the searched index and reloads its
// list; filteredRows then restricts the rows to the matched identities,
// so results reuse that kind's columns, summary pane, and actions.
func (m *model) applySearch(kind Kind, index, query string, names map[string]bool) tea.Cmd {
	m.cur = kind
	m.rows = nil
	m.cursor = 0
	m.loading = true
	m.listErr = ""
	m.status = ""
	m.filtering = false
	m.filter.SetValue("")
	m.summaryCache = map[string]summaryView{}
	m.summaryErr = ""
	m.searchActive = true
	m.searchIndex = index
	m.searchQuery = query
	m.searchNames = names
	m.screen = screenList
	return listCmd(m.ctx, m.client, kind, m.nextReqID())
}

// reloadList re-fetches the current kind after a mutation.
func (m *model) reloadList() tea.Cmd {
	m.loading = true
	return listCmd(m.ctx, m.client, m.cur, m.nextReqID())
}

// openEditor seeds and shows the modal editor for the current kind: its
// typed form when it has one, otherwise the generic JSON editor.
func (m *model) openEditor(action editAction, name string, seed []byte) {
	m.editor = m.newSubEditor(action, seed)
	m.editor.SetSize(m.width, m.height)
	m.editKind = action
	m.editName = name
	m.screen = screenEditor
}

// newSubEditor builds the editor for the current kind. A kind's custom form
// wins when it supplies one; if building it fails we fall back to the JSON
// editor so editing is always possible, but record why so the failure isn't
// silent.
func (m *model) newSubEditor(action editAction, seed []byte) subEditor {
	cf, ok := m.cur.(CustomForm)
	if !ok {
		return newJSONEditor(seed)
	}
	ed, err := cf.NewForm(action, seed)
	if err != nil {
		m.listErr = err.Error()
		return newJSONEditor(seed)
	}
	if ed == nil {
		return newJSONEditor(seed)
	}
	return ed
}
