package explore

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	sm "github.com/cinc-project/cinc-supermarket-api"
)

const (
	searchDebounce  = 250 * time.Millisecond
	previewDelay    = 150 * time.Millisecond
	prefetchTrigger = 10 // start loading the next page this many rows from the bottom
)

func updateBrowse(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cookbooksLoadedMsg:
		return applyCookbooksLoaded(m, msg)
	case cookbookDetailMsg:
		return applyCookbookDetail(m, msg)
	case debounceTickMsg:
		if msg.reqID != m.browse.debounceID {
			return m, nil
		}
		return runSearchOrList(m)
	case previewTickMsg:
		if msg.reqID != m.browse.previewID {
			return m, nil
		}
		if cached, ok := m.browse.previewCache[msg.name]; ok {
			m.browse.preview = cached
			m.browse.previewName = msg.name
			return m, nil
		}
		m.browse.previewBusy = true
		return m, loadDetail(m.ctx, m.client, msg.name, msg.reqID, targetPreview)
	case tea.KeyMsg:
		return handleBrowseKey(m, msg)
	}
	return m, nil
}

func applyCookbooksLoaded(m model, msg cookbooksLoadedMsg) (tea.Model, tea.Cmd) {
	// Drop responses for a sort/mode/query the user has since moved on from.
	if !msg.append {
		if msg.mode != m.browse.mode || msg.sort != m.browse.sort || (msg.mode == modeSearch && msg.query != m.browse.query) {
			return m, nil
		}
	}
	if msg.err != nil {
		m.browse.loading = false
		m.browse.loadingMore = false
		m.browse.lastErr = msg.err.Error()
		return m, nil
	}
	m.browse.lastErr = ""
	if msg.append {
		m.browse.items = append(m.browse.items, msg.page.Items...)
	} else {
		m.browse.items = append([]sm.CookbookSummary(nil), msg.page.Items...)
		m.browse.cursor = 0
		m.browse.preview = nil
		m.browse.previewName = ""
	}
	m.browse.start = msg.page.Start
	m.browse.total = msg.page.Total
	m.browse.hasMore = msg.page.HasMore()
	m.browse.loading = false
	m.browse.loadingMore = false
	if !msg.append {
		return m, schedulePreview(&m)
	}
	return m, nil
}

func applyCookbookDetail(m model, msg cookbookDetailMsg) (tea.Model, tea.Cmd) {
	if msg.target == targetPreview {
		if msg.reqID != m.browse.previewID {
			return m, nil
		}
		m.browse.previewBusy = false
		if msg.err != nil {
			m.browse.previewLastErr = msg.err.Error()
			m.browse.preview = nil
			return m, nil
		}
		m.browse.previewLastErr = ""
		m.browse.preview = msg.cookbook
		m.browse.previewName = msg.name
		m.browse.previewCache[msg.name] = msg.cookbook
		return m, nil
	}
	// targetDetail
	if msg.name != m.detail.name {
		return m, nil
	}
	m.detail.loading = false
	if msg.err != nil {
		m.detail.err = msg.err.Error()
		return m, nil
	}
	m.detail.err = ""
	m.detail.cookbook = msg.cookbook
	m.browse.previewCache[msg.name] = msg.cookbook
	renderDetailViewport(&m)
	return m, nil
}

// handleBrowseKey routes a key event by mode. A pending install
// confirmation intercepts everything; otherwise, in search mode printable
// characters edit the query, and in nav mode sort/quit/etc. hotkeys win.
func handleBrowseKey(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.browse.confirmInstall {
		return handleInstallConfirm(m, msg)
	}
	if m.browse.searchFocused {
		return handleSearchKey(m, msg)
	}
	return handleNavKey(m, msg)
}

func handleNavKey(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up):
		return moveCursor(m, -1)
	case key.Matches(msg, m.keys.Down):
		return moveCursor(m, 1)
	case key.Matches(msg, m.keys.PageUp):
		return moveCursor(m, -10)
	case key.Matches(msg, m.keys.PageDown):
		return moveCursor(m, 10)
	case key.Matches(msg, m.keys.Home):
		return moveCursor(m, -len(m.browse.items))
	case key.Matches(msg, m.keys.End):
		return moveCursor(m, len(m.browse.items))
	case key.Matches(msg, m.keys.Search):
		m.browse.searchInput.SetValue(m.browse.query)
		m.browse.searchInput.Focus()
		m.browse.searchFocused = true
		return m, nil
	case key.Matches(msg, m.keys.Esc):
		if m.browse.query != "" || m.browse.mode == modeSearch {
			return clearSearch(m)
		}
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		return openDetail(m)
	case key.Matches(msg, m.keys.SortDown):
		return setSort(m, sortMostDownloaded)
	case key.Matches(msg, m.keys.SortUpdate):
		return setSort(m, sortRecentlyUpdated)
	case key.Matches(msg, m.keys.SortAlpha):
		return setSort(m, sortAlphabetical)
	case key.Matches(msg, m.keys.Open):
		return openInBrowser(m, m.browse.previewName)
	case key.Matches(msg, m.keys.Install):
		return startInstall(m)
	}
	return m, nil
}

// startInstall opens the y/n confirmation for installing the highlighted
// cookbook onto the server. It no-ops on an empty list or mid-install.
func startInstall(m model) (tea.Model, tea.Cmd) {
	if m.browse.installing || len(m.browse.items) == 0 {
		return m, nil
	}
	m.browse.confirmInstall = true
	m.browse.installName = m.browse.items[m.browse.cursor].Name
	m.browse.installErr = ""
	m.browse.installMsg = ""
	return m, nil
}

// handleInstallConfirm resolves the y/n prompt. Enter or y proceeds;
// anything else cancels.
func handleInstallConfirm(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.browse.confirmInstall = false
		if m.install == nil {
			m.browse.installErr = "Installing isn't available here — launch explore with a configured profile."
			return m, nil
		}
		m.browse.installing = true
		m.browse.installErr = ""
		m.browse.installMsg = ""
		return m, installCookbook(m.ctx, m.install, m.browse.installName, "")
	default:
		m.browse.confirmInstall = false
		return m, nil
	}
}

// applyInstallDone records the outcome of a confirmed install for the
// footer to render.
func applyInstallDone(m model, msg installDoneMsg) (tea.Model, tea.Cmd) {
	m.browse.installing = false
	if msg.err != nil {
		m.browse.installErr = msg.err.Error()
		m.browse.installMsg = ""
		return m, nil
	}
	m.browse.installErr = ""
	m.browse.installMsg = "Installed " + msg.name + " onto the server."
	return m, nil
}

func handleSearchKey(m model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.browse.searchInput.Blur()
		m.browse.searchFocused = false
		if m.browse.searchInput.Value() == "" && m.browse.mode == modeSearch {
			return clearSearch(m)
		}
		m.browse.query = m.browse.searchInput.Value()
		return m, nil
	case tea.KeyEnter:
		m.browse.searchInput.Blur()
		m.browse.searchFocused = false
		m.browse.query = m.browse.searchInput.Value()
		return m, nil
	case tea.KeyCtrlC:
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.browse.searchInput, cmd = m.browse.searchInput.Update(msg)
	newQuery := m.browse.searchInput.Value()
	if newQuery == m.browse.query {
		return m, cmd
	}
	m.browse.query = newQuery
	if newQuery == "" {
		next, clearCmd := clearSearch(m)
		return next, tea.Batch(cmd, clearCmd)
	}
	id := m.nextReqID()
	m.browse.debounceID = id
	m.browse.mode = modeSearch
	m.browse.loading = true
	m.browse.items = nil
	m.browse.cursor = 0
	return m, tea.Batch(cmd, debounce(searchDebounce, id, newQuery))
}

func moveCursor(m model, delta int) (tea.Model, tea.Cmd) {
	if len(m.browse.items) == 0 {
		return m, nil
	}
	c := m.browse.cursor + delta
	if c < 0 {
		c = 0
	}
	if c > len(m.browse.items)-1 {
		c = len(m.browse.items) - 1
	}
	if c == m.browse.cursor {
		return m, nil
	}
	m.browse.cursor = c
	cmds := []tea.Cmd{schedulePreview(&m)}
	if m.browse.hasMore && !m.browse.loadingMore && c >= len(m.browse.items)-prefetchTrigger {
		m.browse.loadingMore = true
		nextStart := m.browse.start + len(m.browse.items)
		switch m.browse.mode {
		case modeList:
			cmds = append(cmds, loadCookbooks(m.ctx, m.client, m.browse.sort, nextStart, m.nextReqID(), true))
		case modeSearch:
			cmds = append(cmds, searchCookbooks(m.ctx, m.client, m.browse.query, nextStart, m.nextReqID(), true))
		}
	}
	return m, tea.Batch(cmds...)
}

// schedulePreview kicks off a preview fetch for the currently highlighted
// row, debounced so arrow-spamming doesn't fire dozens of requests.
func schedulePreview(m *model) tea.Cmd {
	if len(m.browse.items) == 0 {
		m.browse.preview = nil
		m.browse.previewName = ""
		return nil
	}
	name := m.browse.items[m.browse.cursor].Name
	if cached, ok := m.browse.previewCache[name]; ok {
		m.browse.preview = cached
		m.browse.previewName = name
		return nil
	}
	id := m.nextReqID()
	m.browse.previewID = id
	return previewDebounce(previewDelay, id, name)
}

func setSort(m model, s sortOrder) (tea.Model, tea.Cmd) {
	if m.browse.mode == modeSearch {
		// Sort is meaningless during search — ignore.
		return m, nil
	}
	if m.browse.sort == s {
		return m, nil
	}
	m.browse.sort = s
	m.browse.loading = true
	m.browse.items = nil
	m.browse.cursor = 0
	m.browse.preview = nil
	return m, loadCookbooks(m.ctx, m.client, s, 0, m.nextReqID(), false)
}

func clearSearch(m model) (tea.Model, tea.Cmd) {
	m.browse.query = ""
	m.browse.searchInput.SetValue("")
	m.browse.mode = modeList
	m.browse.loading = true
	m.browse.items = nil
	m.browse.cursor = 0
	m.browse.preview = nil
	return m, loadCookbooks(m.ctx, m.client, m.browse.sort, 0, m.nextReqID(), false)
}

// runSearchOrList fires the request the most recent debounce tick is
// gating. Called when a search debounceTick matches the current reqID.
func runSearchOrList(m model) (tea.Model, tea.Cmd) {
	if m.browse.query == "" {
		return m, loadCookbooks(m.ctx, m.client, m.browse.sort, 0, m.nextReqID(), false)
	}
	return m, searchCookbooks(m.ctx, m.client, m.browse.query, 0, m.nextReqID(), false)
}

func openDetail(m model) (tea.Model, tea.Cmd) {
	if len(m.browse.items) == 0 {
		return m, nil
	}
	name := m.browse.items[m.browse.cursor].Name
	m.detail = detailState{
		name:     name,
		viewport: m.detail.viewport,
		ready:    m.detail.ready,
	}
	if cached, ok := m.browse.previewCache[name]; ok {
		m.detail.cookbook = cached
		renderDetailViewport(&m)
		m.view = viewDetail
		return m, nil
	}
	m.detail.loading = true
	m.view = viewDetail
	return m, loadDetail(m.ctx, m.client, name, m.nextReqID(), targetDetail)
}

func openInBrowser(m model, name string) (tea.Model, tea.Cmd) {
	if name == "" || m.openInBrowser == nil {
		return m, nil
	}
	url := cookbookURL(m, name)
	if err := m.openInBrowser(url); err != nil {
		m.openErr = err.Error()
	} else {
		m.openErr = ""
	}
	return m, nil
}

func cookbookURL(m model, name string) string {
	if cached, ok := m.browse.previewCache[name]; ok && cached != nil && cached.ExternalURL != "" {
		return cached.ExternalURL
	}
	site := m.site
	if site == "" {
		site = sm.DefaultBaseURL
	}
	return strings.TrimRight(site, "/") + "/cookbooks/" + name
}

// ----- view -------------------------------------------------------------

func viewBrowseScreen(m model) string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	header := renderHeader(m, width)
	footer := renderFooter(m)
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	body := renderBrowseBody(m, width, bodyHeight)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func renderHeader(m model, width int) string {
	site := m.site
	if site == "" {
		site = sm.DefaultBaseURL
	}
	title := m.styles.Title.Render("cinc supermarket explore")
	subtitle := m.styles.Subtitle.Render(" · " + site)

	sorts := renderSortBar(m)
	stats := ""
	if m.browse.total > 0 {
		stats = m.styles.Status.Render(fmt.Sprintf("%d results", m.browse.total))
	}
	sortLine := lipgloss.JoinHorizontal(lipgloss.Top, sorts, strings.Repeat(" ", spacerWidth(width, sorts, stats)), stats)

	var search string
	label := m.styles.SearchLabel.Render("/ ")
	if m.browse.searchFocused {
		search = label + m.browse.searchInput.View()
	} else if m.browse.query != "" {
		search = label + m.styles.SearchInput.Render(m.browse.query)
	} else {
		search = label + m.styles.Subtitle.Render("press / to search")
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		title+subtitle,
		sortLine,
		search,
	)
}

func renderSortBar(m model) string {
	dim := m.browse.mode == modeSearch
	parts := []string{m.styles.Subtitle.Render("Sort:")}
	parts = append(parts, sortChip(m, "d", "Downloads", sortMostDownloaded, dim))
	parts = append(parts, sortChip(m, "u", "Updated", sortRecentlyUpdated, dim))
	parts = append(parts, sortChip(m, "a", "Alphabetical", sortAlphabetical, dim))
	return strings.Join(parts, "  ")
}

func sortChip(m model, key, label string, value sortOrder, dim bool) string {
	chip := fmt.Sprintf("[%s] %s", key, label)
	switch {
	case dim:
		return m.styles.SortDim.Render(chip)
	case m.browse.sort == value:
		return m.styles.SortActive.Render(chip + "*")
	default:
		return m.styles.SortBar.Render(chip)
	}
}

func renderBrowseBody(m model, width, height int) string {
	listWidth := width * 2 / 5
	if listWidth < 24 {
		listWidth = 24
	}
	if listWidth > width-30 {
		listWidth = width - 30
		if listWidth < 24 {
			listWidth = 24
		}
	}
	previewWidth := width - listWidth - 3 // 1 for divider + 2 padding
	if previewWidth < 10 {
		previewWidth = 10
	}
	listCol := renderList(m, listWidth, height)
	previewCol := renderPreview(m, previewWidth, height)
	divider := renderDivider(height, m)
	return lipgloss.JoinHorizontal(lipgloss.Top, listCol, divider, previewCol)
}

func renderList(m model, width, height int) string {
	if len(m.browse.items) == 0 {
		switch {
		case m.browse.loading:
			return padBlock(m.styles.Status.Render("Loading cookbooks…"), width, height)
		case m.browse.lastErr != "":
			return padBlock(m.styles.Error.Render("⚠ "+m.browse.lastErr), width, height)
		case m.browse.mode == modeSearch:
			return padBlock(m.styles.Status.Render("No cookbooks match \""+m.browse.query+"\""), width, height)
		default:
			return padBlock(m.styles.Status.Render("No cookbooks."), width, height)
		}
	}
	rows := make([]string, 0, height)
	// Window the list around the cursor so it scrolls inside the pane.
	top, bottom := visibleWindow(m.browse.cursor, len(m.browse.items), height)
	for i := top; i < bottom; i++ {
		rows = append(rows, renderListRow(m, i, width))
	}
	if m.browse.loadingMore {
		rows = append(rows, m.styles.Status.Render("  loading…"))
	}
	return padBlock(strings.Join(rows, "\n"), width, height)
}

func renderListRow(m model, i, width int) string {
	cb := m.browse.items[i]
	name := cb.Name
	maintainer := cb.Maintainer
	prefix := "  "
	nameStyle := m.styles.ListItem
	if i == m.browse.cursor {
		prefix = "> "
		nameStyle = m.styles.ListCursor
	}
	// Right-align maintainer when there's room.
	available := width - lipgloss.Width(prefix) - 1
	if available < 8 {
		return prefix + nameStyle.Render(truncate(name, available))
	}
	nameCol := available * 3 / 5
	maintCol := available - nameCol - 1
	if maintCol < 4 {
		return prefix + nameStyle.Render(truncate(name, available))
	}
	nameRendered := nameStyle.Render(padRight(truncate(name, nameCol), nameCol))
	maintRendered := m.styles.Maintainer.Render(truncate(maintainer, maintCol))
	return prefix + nameRendered + " " + maintRendered
}

func renderPreview(m model, width, height int) string {
	if m.browse.previewBusy && m.browse.preview == nil {
		return padBlock(m.styles.Status.Render("Loading preview…"), width, height)
	}
	if m.browse.previewLastErr != "" {
		return padBlock(m.styles.Error.Render("⚠ "+m.browse.previewLastErr), width, height)
	}
	cb := m.browse.preview
	if cb == nil {
		if len(m.browse.items) == 0 {
			return padBlock("", width, height)
		}
		return padBlock(m.styles.Subtitle.Render("Select a cookbook to preview it."), width, height)
	}
	now := time.Now()
	lines := []string{
		m.styles.Title.Render(cb.Name),
		"",
		previewLine(m, "Maintainer", cb.Maintainer),
		previewLine(m, "Category", cb.Category),
		previewLine(m, "Latest", cb.LatestVersion),
		previewLine(m, "Updated", formatRelativeTime(cb.UpdatedAt, now)),
		previewLine(m, "Downloads", formatDownloads(cb.Metrics.Downloads.Total)),
	}
	if cb.Deprecated {
		lines = append(lines, m.styles.Deprecated.Render("DEPRECATED"))
	}
	if cb.Description != "" {
		lines = append(lines, "", m.styles.PreviewBody.Render(wrap(cb.Description, width-2)))
	}
	return padBlock(strings.Join(lines, "\n"), width, height)
}

func previewLine(m model, key, value string) string {
	if value == "" {
		value = "—"
	}
	return m.styles.PreviewKey.Render(padRight(key, 11)) + m.styles.PreviewBody.Render(value)
}

func renderFooter(m model) string {
	help := strings.Join([]string{
		hint(m, m.keys.Up.Help()),
		hint(m, m.keys.Search.Help()),
		hint(m, m.keys.Enter.Help()),
		hint(m, key.NewBinding(key.WithHelp("d/u/a", "sort")).Help()),
		hint(m, m.keys.Esc.Help()),
		hint(m, m.keys.Open.Help()),
		hint(m, m.keys.Install.Help()),
		hint(m, m.keys.Quit.Help()),
	}, "  ")
	if status := installStatusLine(m); status != "" {
		help = status + "\n" + help
	} else if m.openErr != "" {
		help = m.styles.Error.Render("⚠ "+m.openErr) + "\n" + help
	} else if m.browse.lastErr != "" && len(m.browse.items) > 0 {
		help = m.styles.Error.Render("⚠ "+m.browse.lastErr) + "\n" + help
	}
	return m.styles.Footer.Render(help)
}

// installStatusLine renders the most relevant install state — confirm
// prompt, in-flight notice, error, or last success — or "" when there's
// nothing to say.
func installStatusLine(m model) string {
	switch {
	case m.browse.confirmInstall:
		return m.styles.Status.Render("Install " + m.browse.installName + " onto the server? (y/n)")
	case m.browse.installing:
		return m.styles.Status.Render("Installing " + m.browse.installName + "…")
	case m.browse.installErr != "":
		return m.styles.Error.Render("⚠ " + m.browse.installErr)
	case m.browse.installMsg != "":
		return m.styles.Status.Render("✓ " + m.browse.installMsg)
	default:
		return ""
	}
}

func hint(m model, h key.Help) string {
	return m.styles.HelpKey.Render(h.Key) + " " + m.styles.HelpDesc.Render(h.Desc)
}

// ----- view helpers -----------------------------------------------------

func padBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = padRight(line, width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	for i := 1; i < len(runes); i++ {
		candidate := string(runes[:i]) + "…"
		if lipgloss.Width(candidate) > width {
			return string(runes[:i-1]) + "…"
		}
	}
	return s
}

func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := ""
	for _, w := range words {
		if line == "" {
			line = w
			continue
		}
		if lipgloss.Width(line)+1+lipgloss.Width(w) > width {
			b.WriteString(line)
			b.WriteByte('\n')
			line = w
			continue
		}
		line += " " + w
	}
	if line != "" {
		b.WriteString(line)
	}
	return b.String()
}

func visibleWindow(cursor, n, height int) (int, int) {
	if n <= height {
		return 0, n
	}
	top := cursor - height/2
	if top < 0 {
		top = 0
	}
	if top+height > n {
		top = n - height
	}
	return top, top + height
}

func renderDivider(height int, m model) string {
	bar := m.styles.Divider.Render("│")
	rows := make([]string, height)
	for i := range rows {
		rows[i] = bar
	}
	return strings.Join(rows, "\n")
}

func spacerWidth(width int, left, right string) int {
	w := width - lipgloss.Width(left) - lipgloss.Width(right)
	if w < 1 {
		return 1
	}
	return w
}
