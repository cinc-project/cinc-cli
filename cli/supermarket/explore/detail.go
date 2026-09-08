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

func updateDetail(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cookbookDetailMsg:
		return applyCookbookDetail(m, msg)
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Esc), key.Matches(msg, m.keys.Enter):
			m.view = viewBrowse
			return m, nil
		case key.Matches(msg, m.keys.Open):
			return openInBrowser(m, m.detail.name)
		}
		var cmd tea.Cmd
		m.detail.viewport, cmd = m.detail.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.detail.viewport, cmd = m.detail.viewport.Update(msg)
	return m, cmd
}

func renderDetailViewport(m *model) {
	if m.detail.cookbook == nil {
		m.detail.viewport.SetContent("")
		return
	}
	body := detailBody(*m, m.detail.cookbook)
	m.detail.viewport.SetContent(body)
	m.detail.viewport.GotoTop()
}

func detailBody(m model, cb *sm.Cookbook) string {
	now := time.Now()
	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := width - 2
	if contentWidth < 20 {
		contentWidth = 20
	}

	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.styles.PreviewKey.Render(padRight("Maintainer", 12))+m.styles.PreviewBody.Render(orDash(cb.Maintainer)),
	)

	rows := []string{
		header,
		twoCol(m, "Category", orDash(cb.Category), "Latest", orDash(cb.LatestVersion), contentWidth),
		twoCol(m, "Downloads", formatDownloads(cb.Metrics.Downloads.Total), "Updated", formatRelativeTime(cb.UpdatedAt, now), contentWidth),
		twoCol(m, "Source", orDash(cb.ExternalURL), "Followers", formatDownloads(cb.Metrics.Followers), contentWidth),
	}
	if cb.Deprecated {
		rows = append(rows, m.styles.Deprecated.Render("DEPRECATED"))
	}
	if cb.Description != "" {
		rows = append(rows,
			"",
			m.styles.Title.Render("Description"),
			m.styles.Divider.Render(strings.Repeat("─", 11)),
			m.styles.PreviewBody.Render(wrap(cb.Description, contentWidth)),
		)
	}
	if len(cb.Versions) > 0 {
		rows = append(rows,
			"",
			m.styles.Title.Render(fmt.Sprintf("Versions (%d)", len(cb.Versions))),
			m.styles.Divider.Render(strings.Repeat("─", 13)),
			renderVersions(cb.Versions, cb.Metrics.Downloads.Versions, contentWidth, m),
		)
	}
	return strings.Join(rows, "\n")
}

func twoCol(m model, k1, v1, k2, v2 string, width int) string {
	col := width / 2
	if col < 24 {
		return m.styles.PreviewKey.Render(padRight(k1, 12)) + m.styles.PreviewBody.Render(v1) + "\n" +
			m.styles.PreviewKey.Render(padRight(k2, 12)) + m.styles.PreviewBody.Render(v2)
	}
	left := m.styles.PreviewKey.Render(padRight(k1, 12)) + m.styles.PreviewBody.Render(padRight(truncate(v1, col-13), col-12))
	right := m.styles.PreviewKey.Render(padRight(k2, 12)) + m.styles.PreviewBody.Render(truncate(v2, width-col-12))
	return left + right
}

func renderVersions(versions []string, downloads map[string]int, width int, m model) string {
	const perRow = 6
	var rows []string
	row := make([]string, 0, perRow)
	colW := width / perRow
	if colW < 10 {
		colW = 10
	}
	for _, v := range versions {
		label := "• " + v
		if d, ok := downloads[v]; ok && d > 0 {
			label = fmt.Sprintf("• %s (%s)", v, formatDownloads(d))
		}
		row = append(row, m.styles.PreviewBody.Render(padRight(truncate(label, colW), colW)))
		if len(row) == perRow {
			rows = append(rows, strings.Join(row, ""))
			row = row[:0]
		}
	}
	if len(row) > 0 {
		rows = append(rows, strings.Join(row, ""))
	}
	return strings.Join(rows, "\n")
}

func viewDetailScreen(m model) string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	title := m.styles.Title.Render(m.detail.name)
	if m.detail.cookbook != nil && m.detail.cookbook.Deprecated {
		title += " " + m.styles.Deprecated.Render("[DEPRECATED]")
	}
	header := title

	footer := m.styles.Footer.Render(strings.Join([]string{
		hint(m, m.keys.Up.Help()),
		hint(m, m.keys.Esc.Help()),
		hint(m, m.keys.Open.Help()),
		hint(m, m.keys.Quit.Help()),
	}, "  "))

	body := ""
	switch {
	case m.detail.loading:
		body = m.styles.Status.Render("Loading…")
	case m.detail.err != "":
		body = m.styles.Error.Render("⚠ " + m.detail.err)
	case m.detail.ready:
		body = m.detail.viewport.View()
	}
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		padBlock(body, width, bodyHeight),
		footer,
	)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
