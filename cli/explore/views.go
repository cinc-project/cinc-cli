package explore

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// ----- shared chrome ---------------------------------------------------

// cincArt is the ASCII wordmark shown in the top-right of the title bar.
// It is topBarHeight lines tall; keep the two in sync if it changes.
var cincArt = strings.Join([]string{
	`  ___ _            `,
	` / __(_)_ _  __    `,
	`| (__| | ' \/ _|   `,
	` \___|_|_||_\__|   `,
}, "\n")

// topBarHeight is how many lines the title bar occupies — tall enough to
// hold the cincArt wordmark.
const topBarHeight = 4

// chromeHeight is the number of lines frame() reserves around the body:
// the top and bottom border (2), the title bar (topBarHeight), the
// divider rule under it (1), and the two-line footer (2). The body fills
// whatever is left.
const chromeHeight = topBarHeight + 5

// bodyHeight is the number of content lines available between the header
// and the pinned footer, inside the border. Sub-components (the detail
// viewport, the list window) size themselves to this so nothing spills
// past the border.
func (m model) bodyHeight() int {
	return max(1, m.height-chromeHeight)
}

// frame composes a full-terminal layout: the title header at the top, the
// body in the middle, and the footer pinned to the bottom — all wrapped in
// a border that fills the whole terminal window. Because the body is
// padded to fill the gap, the footer (the command hints) always sits on
// the last line no matter how little body there is.
func (m model) frame(title, body string, hints []string) string {
	foot := m.footer(hints)

	// Before the first WindowSizeMsg we have no dimensions to fill, so
	// render plainly rather than drawing a degenerate one-cell border.
	if m.width <= 0 || m.height <= 0 {
		return m.styles.Title.Render(title) + "\n\n" + body + "\n" + foot
	}

	innerW := max(1, m.width-2)
	innerH := max(1, m.height-2)
	head := m.topBar(title, innerW)
	// A full-width rule separating the title bar from the body.
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render(strings.Repeat("─", innerW))
	bodyH := max(1, innerH-lipgloss.Height(head)-1-lipgloss.Height(foot))

	bodyBox := lipgloss.NewStyle().
		Width(innerW).
		Height(bodyH).
		MaxHeight(bodyH).
		Render(body)

	content := lipgloss.JoinVertical(lipgloss.Left, head, divider, bodyBox, foot)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Width(innerW).
		Height(innerH).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(content)
}

// topBar renders the title bar: the title (or breadcrumb) and active
// profile in the top-left, the connected server's host and version in
// italics pinned to the bottom-left, and the cincArt wordmark in the
// top-right. It is exactly topBarHeight lines tall and width columns wide.
func (m model) topBar(title string, width int) string {
	art := m.styles.Title.Render(cincArt)
	artW := lipgloss.Width(art)

	top := m.styles.Title.Render(title)
	if m.profileName != "" {
		top += m.styles.Crumb.Render("  [" + m.profileName + "]")
	}

	// Stack the title at the top and the server info on the last line,
	// with blank filler between, so the whole top bar is topBarHeight tall.
	lines := make([]string, topBarHeight)
	lines[0] = top
	if info := m.serverInfo(); info != "" {
		lines[topBarHeight-1] = info
	}
	leftBox := lipgloss.NewStyle().
		Width(max(1, width-artW)).
		Render(strings.Join(lines, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, art)
}

// serverInfo is the italic "host  API vN" line for the title bar, or ""
// when we don't yet know which server we're talking to.
func (m model) serverInfo() string {
	if m.serverHost == "" {
		return ""
	}
	s := m.serverHost
	if m.serverVersion != "" {
		s += "  " + m.serverVersion
	}
	return m.styles.ServerInfo.Render(s)
}

// crumb is the drill-down breadcrumb, e.g. "Data Bags › creds".
func (m model) crumb() string {
	parts := make([]string, 0, len(m.stack)+1)
	for _, f := range m.stack {
		parts = append(parts, f.kind.Title())
	}
	if m.cur != nil {
		parts = append(parts, m.cur.Title())
	}
	return strings.Join(parts, " › ")
}

// footer renders the status/error line and a contextual key hint line as
// a two-line block (the status line is blank when there's nothing to
// report) so the hint line always lands on the bottom border.
func (m model) footer(hints []string) string {
	var status string
	switch {
	case m.listErr != "":
		status = m.styles.Error.Render("✗ " + m.listErr)
	case m.detailErr != "":
		status = m.styles.Error.Render("✗ " + m.detailErr)
	case m.status != "":
		status = m.styles.Status.Render("✓ " + m.status)
	}
	return status + "\n" + m.styles.Footer.Render(strings.Join(hints, "   "))
}

// actionHints lists the capability-gated action keys the current kind
// supports, in a stable order.
func (m model) actionHints() []string {
	var hints []string
	if _, ok := m.cur.(Editable); ok {
		hints = append(hints, m.hint("e", "edit"))
	}
	if _, ok := m.cur.(Creatable); ok {
		hints = append(hints, m.hint("n", "new"))
	} else if _, ok := m.cur.(NamedCreatable); ok {
		hints = append(hints, m.hint("n", "new"))
	}
	if _, ok := m.cur.(Deletable); ok {
		hints = append(hints, m.hint("d", "delete"))
	}
	if _, ok := m.cur.(Downloadable); ok {
		hints = append(hints, m.hint("D", "download"))
	}
	return hints
}

func (m model) hint(k, desc string) string {
	return m.styles.HelpKey.Render(k) + " " + m.styles.HelpDesc.Render(desc)
}

// ----- profiles --------------------------------------------------------

func (m model) viewProfiles() string {
	var b strings.Builder
	b.WriteString(m.styles.Header.Render("Select a profile") + "\n\n")
	for i, name := range m.profileNames {
		b.WriteString(m.renderChoice(name, i == m.profileCursor) + "\n")
	}
	return m.frame(progname.Get()+" explore", b.String(), []string{
		m.hint("↑/↓", "move"), m.hint("↵", "select"), m.hint("q", "quit"),
	})
}

// ----- kind menu -------------------------------------------------------

func (m model) viewKinds() string {
	var b strings.Builder
	b.WriteString(m.styles.Header.Render("Object types") + "\n\n")
	for i, k := range m.kinds {
		b.WriteString(m.renderChoice(k.Title(), i == m.kindCursor) + "\n")
	}
	hints := []string{m.hint("↑/↓", "move"), m.hint("↵", "open")}
	if _, ok := searchIndexOf(m.kinds[m.kindCursor]); ok {
		hints = append(hints, m.hint("s", "search"))
	}
	if len(m.profileNames) > 1 {
		hints = append(hints, m.hint("esc", "profiles"))
	}
	hints = append(hints, m.hint("q", "quit"))
	return m.frame(progname.Get()+" explore", b.String(), hints)
}

// selectedMarker is the cursor glyph drawn against the highlighted row.
// It is a filled triangle (heavier than a thin chevron) so the selection
// reads clearly as you move between lines; it is one cell wide so the
// blank prefix on unselected rows keeps every row aligned.
const selectedMarker = "▶ "

func (m model) renderChoice(label string, selected bool) string {
	if selected {
		return m.styles.ListCursor.Render(selectedMarker + label)
	}
	return m.styles.ListItem.Render("  " + label)
}

// ----- list ------------------------------------------------------------

// splitMinWidth is the inner width below which the list drops the summary
// pane and uses the full width, so narrow terminals stay usable.
const splitMinWidth = 72

// splitListMin is the floor for the list pane's width in the split.
const splitListMin = 20

func (m model) viewList() string {
	hints := []string{m.hint("↵", m.enterVerb()), m.hint("/", "filter")}
	if _, ok := searchIndexOf(m.cur); ok {
		hints = append(hints, m.hint("s", "search"))
	}
	hints = append(hints, m.hint(":", "kinds"))
	hints = append(hints, m.actionHints()...)
	if m.searchActive {
		hints = append(hints, m.hint("esc", "clear search"))
	} else {
		hints = append(hints, m.hint("esc", "back"))
	}
	hints = append(hints, m.hint("q", "quit"))

	innerW := max(1, m.width-2)
	if innerW < splitMinWidth {
		return m.frame(m.crumb(), m.renderListContent(), hints)
	}

	h := m.bodyHeight()
	listW := max(splitListMin, innerW/3)
	paneW := innerW - listW - 1 // 1 col for the separator

	left := lipgloss.NewStyle().
		Width(listW).MaxWidth(listW).Height(h).MaxHeight(h).
		Render(m.renderListContent())
	sep := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render(verticalRule(h))
	right := lipgloss.NewStyle().
		Width(paneW).MaxWidth(paneW).Height(h).MaxHeight(h).PaddingLeft(1).
		Render(m.renderSummaryContent())

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	return m.frame(m.crumb(), body, hints)
}

// verticalRule returns n stacked "│" glyphs, the split's separator column.
func verticalRule(n int) string {
	if n < 1 {
		n = 1
	}
	return strings.TrimRight(strings.Repeat("│\n", n), "\n")
}

// renderListContent is the left pane: an optional filter line followed by
// the table (or a loading/empty notice).
func (m model) renderListContent() string {
	var b strings.Builder
	rows := m.filteredRows()
	if m.searchActive {
		banner := fmt.Sprintf("🔍 %s: %s  (%d)", m.searchIndex, m.searchQuery, len(rows))
		b.WriteString(m.styles.Status.Render(banner) + "\n")
	}
	if m.filtering || m.filter.Value() != "" {
		b.WriteString(m.filter.View() + "\n")
	}
	switch {
	case m.loading:
		b.WriteString(m.styles.Body.Render("Loading…") + "\n")
	case len(rows) == 0 && m.listErr == "":
		b.WriteString(m.styles.Body.Render("No "+strings.ToLower(m.crumb())+" found.") + "\n")
	default:
		b.WriteString(m.renderTable(rows))
	}
	return b.String()
}

// renderSummaryContent is the right pane: the curated facts (or JSON) for
// the selected object, with a loading or error placeholder until it lands.
func (m model) renderSummaryContent() string {
	row, ok := m.selectedRow()
	if !ok {
		if m.loading {
			return ""
		}
		return m.styles.Body.Render("Nothing selected.")
	}

	// Until the summary lands we only know the row name; once it's cached a
	// kind may supply a richer heading (a node appends its platform).
	title := row.Name
	if view, ok := m.summaryCache[row.Name]; ok && view.Title != "" {
		title = view.Title
	}

	var b strings.Builder
	b.WriteString(m.styles.Title.Render(title) + "\n\n")
	switch {
	case m.summaryErr != "":
		b.WriteString(m.styles.Error.Render("✗ " + m.summaryErr))
	default:
		if view, ok := m.summaryCache[row.Name]; ok {
			if len(view.Fields) > 0 {
				b.WriteString(m.renderFields(view.Fields))
			} else {
				b.WriteString(highlightJSON(m.colorJSON, view.JSON))
			}
		} else {
			b.WriteString(m.styles.Body.Render("Loading…"))
		}
	}
	return b.String()
}

// renderFields renders label/value rows with the labels padded to a common
// width so the values line up.
func (m model) renderFields(fields []summaryField) string {
	w := 0
	for _, f := range fields {
		if len(f.Label) > w {
			w = len(f.Label)
		}
	}
	var b strings.Builder
	for i, f := range fields {
		label := m.styles.Header.Render(fmt.Sprintf("%-*s", w, f.Label))
		b.WriteString(label + "  " + m.styles.Body.Render(f.Value))
		if i < len(fields)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// enterVerb labels the Enter key by what it does for the current kind.
func (m model) enterVerb() string {
	if _, ok := m.cur.(DrillDown); ok {
		return "open"
	}
	return "view"
}

// renderTable lays out the columned, cursor-highlighted rows, windowed
// to the available height.
func (m model) renderTable(rows []Row) string {
	cols := m.cur.Columns()
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for _, r := range rows {
		for i, cell := range r.Cells {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var b strings.Builder
	b.WriteString(m.styles.Header.Render("  "+padCells(cols, widths)) + "\n")

	start, end := m.window(len(rows))
	for i := start; i < end; i++ {
		line := padCells(rows[i].Cells, widths)
		if i == m.cursor {
			b.WriteString(m.styles.ListCursor.Render(selectedMarker+line) + "\n")
		} else {
			b.WriteString(m.styles.ListItem.Render("  "+line) + "\n")
		}
	}
	return b.String()
}

// window returns the visible [start,end) slice of rows so the cursor
// stays on screen. It sizes to the body area minus the table's own header
// line (and the filter line when filtering) so the table never pushes the
// footer past the border.
func (m model) window(n int) (int, int) {
	height := m.bodyHeight() - 1 // the column-header line
	if m.searchActive {
		height-- // the search banner line
	}
	if m.filtering || m.filter.Value() != "" {
		height-- // the filter input line
	}
	height = max(1, height)
	if n <= height {
		return 0, n
	}
	start := max(0, m.cursor-height/2)
	if start+height > n {
		start = n - height
	}
	return start, start + height
}

func padCells(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		if i < len(widths) {
			parts[i] = fmt.Sprintf("%-*s", widths[i], c)
		} else {
			parts[i] = c
		}
	}
	return strings.Join(parts, "  ")
}

// ----- detail ----------------------------------------------------------

func (m model) viewDetail() string {
	body := m.detail.View()
	if m.detailErr != "" {
		body = m.styles.Error.Render("✗ " + m.detailErr)
	}
	hints := []string{m.hint("↑/↓", "scroll")}
	hints = append(hints, m.actionHints()...)
	hints = append(hints, m.hint("esc", "back"), m.hint("q", "quit"))
	return m.frame(m.crumb()+" › "+m.detailName, body, hints)
}

// ----- modals ----------------------------------------------------------

func (m model) viewConfirm() string {
	body := "\n" + m.styles.Warn.Render(m.confirmPrompt)
	return m.frame(m.crumb(), body, []string{m.hint("y", "yes"), m.hint("n", "no")})
}

func (m model) viewName() string {
	body := "\n" + m.styles.Header.Render(m.namePrompt) + "\n\n" + m.nameInput.View()
	return m.frame(m.crumb(), body, []string{m.hint("↵", "confirm"), m.hint("esc", "cancel")})
}

func (m model) viewResult() string {
	body := "\n" + m.styles.Body.Render(m.resultBody)
	return m.frame(m.resultTitle, body, []string{m.hint("any key", "dismiss")})
}

func (m model) viewSearch() string {
	// Label the modal with the kind's display title (plural, e.g. "Nodes")
	// to match the index picker and kind menu, not the singular Solr index.
	body := "\n" + m.styles.Header.Render("Search "+m.searchKind.Title()) + "\n\n" + m.searchInput.View()
	return m.frame(m.crumb(), body, []string{
		m.hint("↵", "search"), m.hint("tab", "change index"), m.hint("esc", "cancel"),
	})
}

func (m model) viewSearchIndex() string {
	var b strings.Builder
	b.WriteString(m.styles.Header.Render("Search which type?") + "\n\n")
	for i, k := range m.searchKinds {
		b.WriteString(m.renderChoice(k.Title(), i == m.searchKindCur) + "\n")
	}
	return m.frame(m.crumb(), b.String(), []string{
		m.hint("↑/↓", "move"), m.hint("↵", "select"), m.hint("esc", "back"),
	})
}
