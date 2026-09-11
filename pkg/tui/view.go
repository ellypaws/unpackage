package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/76creates/stickers/flexbox"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

func (m *Model) button(id, label string, active bool) string {
	m.Actions = append(m.Actions, id)
	return components.Button(m.Zones, id, label, m.Hover, m.Focus, active)
}
func (m *Model) bodyHeight() int {
	if m.Tab == tabConsole {
		return max(3, m.Height-12)
	}
	return max(3, m.Height-9)
}
func (m *Model) resultHeight() int {
	h := max(3, m.Height-9) - 5
	if m.wide() {
		h += 2
	}
	if m.PackagesExpanded {
		h -= 5
	} else {
		h -= 1
	}
	return max(2, h)
}
func (m *Model) pageSize() int { return max(1, m.resultHeight()/2) }
func (m *Model) field(id string, input *textinput.Model, w int) string {
	m.Actions = append(m.Actions, id)
	input.Width = max(4, w-4)
	border := components.Border
	if m.Hover == id || m.Focus == id {
		border = components.Accent
	}
	if (id == "search-input" || id == "servers-input") && input.Value() != "" {
		border = lipgloss.Color("#E8BE79")
	}
	return m.Zones.Mark(id, lipgloss.NewStyle().Width(w-2).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(border).Render(ansi.Truncate(input.View(), w-4, "")))
}
func (m *Model) modal(body string) string {
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(components.Accent).Padding(1, 2).Render(body)
	return m.Zones.Scan(lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, panel))
}
func (m *Model) View() string {
	m.Actions = nil
	w := max(16, m.Width-4)
	h := m.bodyHeight()
	if m.Width < 64 || m.Height < 24 {
		return components.Title.Render("Find missing messages") + "\nResize to at least 64 × 24."
	}
	if m.Picker != nil {
		m.Picker.Height = max(1, m.Height-19)
		m.Picker.Path.Width = max(10, w-10)
		m.Actions = []string{"pick-back", "pick-forward", "pick-up", "pick-home", "pick-open", "pick-use", "pick-close", "pick-input"}
		for i := m.Picker.Offset; i < min(len(m.Picker.Entries), m.Picker.Offset+m.Picker.Height); i++ {
			m.Actions = append(m.Actions, fmt.Sprintf("entry-%d", i))
		}
		heading := components.Title.Render([]string{"Older package", "Newer package"}[m.PickSlot])
		return m.Zones.Scan(lipgloss.NewStyle().Padding(0, 2).Render(heading + "\n" + m.Picker.View(m.Zones, w, m.Hover, m.Focus)))
	}
	if m.Calendar != nil {
		m.Actions = []string{"cal-prev", "cal-next", "cal-apply", "cal-clear", "cal-close"}
		for d := 1; d <= m.Calendar.Month.AddDate(0, 1, -1).Day(); d++ {
			m.Actions = append(m.Actions, "date-"+m.Calendar.Month.AddDate(0, 0, d-1).Format(time.DateOnly))
		}
		return m.modal(m.Calendar.View(m.Zones, m.Hover, m.Focus))
	}
	if m.MarginDialog {
		before := "Days before\n" + m.field("margin-before", &m.BeforeInput, 10)
		after := "Days after\n" + m.field("margin-after", &m.AfterInput, 10)
		body := components.Title.Render("Around each selected date") + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, before, "  ", after) + "\n" + m.button("margin-apply", "Apply", true) + " " + m.button("margin-clear", "Exact day", false) + " " + m.button("margin-close", "Cancel", false)
		if m.Notice != "" && m.Notice != "Done" {
			body += "\n\n" + components.Fit(m.Notice, 44)
		}
		return m.modal(body)
	}
	if m.ServerDialog {
		return m.modal(m.servers())
	}
	if m.FilterDialog {
		body := m.filters(34)
		if len(m.Session.Filter.Guilds) > 0 {
			body += "\n\n" + m.button("draft", "Deletion request", false)
		}
		return m.modal(body + "\n" + m.button("filters-close", "Done", true))
	}
	if m.RequestDialog {
		width := min(w-6, 70)
		scope := "All messages in selected servers"
		if m.RequestScope == "filtered" {
			scope = "Current matching messages"
		}
		body := components.Title.Render("Deletion request") + "\n\n" + fmt.Sprintf("%d servers selected", len(m.Session.Filter.Guilds)) + "\n\n" + m.button("scope", scope, false) + "\n\n" + m.field("request-path", &m.RequestInput, width) + "\n\n" + m.button("request-save", "Save draft", true) + "  " + m.button("request-close", "Cancel", false)
		if m.Notice != "" && m.Notice != "Done" {
			body += "\n\n" + components.Fit(m.Notice, width)
		}
		return m.modal(body)
	}
	var tabs []string
	for i, name := range []string{"Investigate", "Console", "Log"} {
		id := fmt.Sprintf("tab-%d", i)
		m.Actions = append(m.Actions, id)
		tabs = append(tabs, components.Tab(m.Zones, id, name, m.Hover, m.Focus, m.Tab == i, 2))
	}
	tabRow := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	tabRow += lipgloss.NewStyle().Foreground(components.Border).Render(strings.Repeat("─", max(0, w-lipgloss.Width(tabRow))))
	var body string
	if m.Detail != nil {
		r := m.Detail
		m.Viewport.Width = w - 4
		m.Viewport.Height = h - 3
		attachment := "None"
		if r.HasMedia {
			attachment = "Image, video or audio"
		} else if r.HasAttachments {
			attachment = "Attachment"
		}
		titleStyle := components.Title
		if r.Status == "missing" {
			titleStyle = titleStyle.Foreground(components.Deleted)
		}
		content := titleStyle.Render(session.Safe(r.Server)+" / "+displayChannel(*r)) + "\n\n" + lipgloss.NewStyle().Foreground(components.Muted).Render(store.LocalDate(r.Date)) + "\n\n" + components.Highlight(r.Content, m.Session.Filter.Search, lipgloss.NewStyle().Foreground(components.Text)) + "\n\n" + lipgloss.NewStyle().Foreground(components.Muted).Render("Attachment  "+attachment+"\nServer      "+r.Guild+"\nChannel     "+r.Channel+"\nMessage     "+r.ID)
		m.Viewport.SetContent(lipgloss.NewStyle().Width(w - 4).Render(content))
		body = m.button("detail-close", "Back to results", false) + "\n\n" + m.Viewport.View()
	} else {
		switch m.Tab {
		case tabInvestigate:
			body = m.investigate(w, h)
		case tabConsole:
			body = m.console(w, h)
		case tabLog:
			lines := m.Session.Log.Lines()
			for i, v := range lines {
				lines[i] = session.Safe(v)
			}
			m.Viewport.Width = w - 4
			m.Viewport.Height = h - 3
			m.Viewport.SetContent(strings.Join(lines, "\n"))
			if m.FollowLog {
				m.Viewport.GotoBottom()
			}
			body = m.button("log-follow", "Follow latest", m.FollowLog) + "\n\n" + m.Viewport.View()
		}
	}
	state := m.Notice
	if m.Executing {
		state = "Working…"
	} else if m.Session.Busy() {
		state = "Loading…"
	}
	if m.Tab == tabInvestigate && (m.SearchInput.Value() != m.Session.Filter.Search || m.Loading && m.Session.Filter.Search != "") {
		state = "Searching…"
	}
	if state == "Done" {
		state = ""
	}
	if m.Warning != "" && m.Warning != "Comparison pending" {
		state = m.Warning
	}
	footer := components.Fit(state, w)
	if m.Tab == tabConsole {
		footer = m.field("command", &m.Input, w) + "\n" + footer
	}
	return m.Zones.Scan(lipgloss.NewStyle().Padding(1, 2).Render(components.Title.Render("Find missing messages") + "\n" + tabRow + "\n\n" + lipgloss.NewStyle().Height(h).MaxHeight(h).Width(w).Render(body) + "\n" + footer))
}
func (m *Model) packageBox(slot, width int) string {
	id := []string{"open-old", "open-new"}[slot]
	label := []string{"Older package", "Newer package"}[slot]
	name := "Drop folder or ZIP"
	var snapshot *store.Snapshot
	for _, v := range m.Snapshots {
		if v.Slot == slot {
			snapshot = new(v)
			break
		}
	}
	if snapshot != nil {
		name = filepath.Base(snapshot.Path) + ", " + number(int(snapshot.Count)) + " messages"
		if snapshot.State == "loading" {
			name = fmt.Sprintf("Loading %d%%, %s messages", min(100, snapshot.Bytes*100/max(1, snapshot.Total)), number(int(snapshot.Count)))
		}
		if snapshot.State == "stopped" {
			name = "Stopped, " + number(int(snapshot.Count)) + " messages"
		}
		if snapshot.State == "partial" {
			name = "Incomplete, " + number(int(snapshot.Count)) + " messages"
		}
	}
	border := components.Border
	fg := components.Accent
	b := lipgloss.RoundedBorder()
	if m.DropSlot == slot {
		border = components.Accent
		b = lipgloss.DoubleBorder()
		label += ": Drop here"
		if width < 35 {
			label = []string{"Older: Drop here", "Newer: Drop here"}[slot]
		}
	}
	if m.Hover == id || m.Focus == id {
		border = lipgloss.Color("#FFB3E3")
		fg = border
	}
	inner := width - 6
	buttons := m.button([]string{"browse-old", "browse-new"}[slot], "Browse", false)
	if snapshot != nil {
		buttons += " " + m.button([]string{"clear-old", "clear-new"}[slot], "Clear", false)
	}
	m.Actions = append(m.Actions, id)
	body := components.Title.Foreground(fg).Render(components.Fit(label, inner)) + "\n" + components.Fit(name, inner) + "\n" + buttons
	return m.Zones.Mark(id, lipgloss.NewStyle().Width(width-2).Padding(0, 2).Border(b).BorderForeground(border).Render(body))
}
func number(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func displayChannel(r store.Row) string {
	name := session.Safe(r.Name)
	if r.Kind == "unknown-dm" {
		return "Unknown participant"
	}
	if r.Kind == "dm" {
		name = strings.TrimPrefix(name, "Direct Message with ")
		if head, tail, ok := strings.Cut(name, "#"); ok && tail != "" && strings.Trim(tail, "0123456789") == "" {
			name = head
		}
		return "@" + strings.TrimPrefix(name, "@")
	}
	return name
}
func (m *Model) wide() bool { return m.Width >= 110 }
func (m *Model) investigate(w, h int) string {
	if !m.wide() {
		return m.results(w, h)
	}
	box := flexbox.New(w, h)
	left := flexbox.NewCell(w-38, 1).SetContentGenerator(func(x, y int) string { return m.results(x-2, y) })
	right := flexbox.NewCell(38, 1).SetContentGenerator(func(x, y int) string {
		return lipgloss.NewStyle().Width(x-1).Height(y).Padding(0, 1).BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(components.Border).Render(m.filterPanel(x-4, y))
	})
	box.AddRows([]*flexbox.Row{box.NewRow().AddCells(left, right)})
	return box.Render()
}
func (m *Model) filters(w int) string {
	parts := []string{m.field("search-input", &m.SearchInput, min(36, w)), components.Title.Render("Message dates"), m.field("days-input", &m.DayInput, min(34, w))}
	parts = append(parts, m.button("days-apply", "Add", false)+" "+m.button("dates", "Calendar", false)+" "+m.button("dates-clear", "Clear", false))
	parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Render("Separate multiple dates with ;"))
	dates := m.Session.Filter.Dates
	if len(dates) == 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Render("Any date"))
	} else {
		var chips []string
		count := min(2, len(dates))
		if len(dates) > 2 {
			count = 1
		}
		for _, d := range dates[:count] {
			chips = append(chips, m.button("remove-date-"+d, d+" ×", true))
		}
		if len(dates) > count {
			chips = append(chips, m.button("dates-more", fmt.Sprintf("+%d dates", len(dates)-count), false))
		}
		parts = append(parts, strings.Join(chips, " "))
	}
	f := m.Session.Filter
	margin := "Exact dates"
	if f.DateBefore > 0 || f.DateAfter > 0 {
		margin = fmt.Sprintf("Date margin: %d before, %d after", f.DateBefore, f.DateAfter)
	}
	parts = append(parts, m.button("margin", components.Fit(margin, w-3), f.DateBefore > 0 || f.DateAfter > 0))
	media := map[string]string{"": "All messages", "attachments": "Messages with attachments", "media": "Images, video or audio"}[f.Media]
	parts = append(parts, m.button("media", components.Fit(media, w-3), f.Media != ""))
	servers := "All servers"
	if len(f.Guilds) > 0 {
		servers = fmt.Sprintf("%d servers selected", len(f.Guilds))
		if len(f.Guilds) == 1 {
			servers = "1 server selected"
		}
	}
	parts = append(parts, m.button("servers", servers, len(f.Guilds) > 0)+" "+m.button("clear", "Reset", false))
	return strings.Join(parts, "\n")
}
func (m *Model) filterPanel(w, h int) string {
	top := m.filters(w)
	if len(m.Session.Filter.Guilds) == 0 {
		return top
	}
	request := components.Title.Render("Request") + "\n" + m.button("draft", "Deletion request", false)
	gap := max(1, h-lipgloss.Height(top)-lipgloss.Height(request)-1)
	return top + strings.Repeat("\n", gap) + request
}
func (m *Model) results(w, h int) string {
	var parts []string
	if m.PackagesExpanded {
		half := (w - 2) / 2
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, m.packageBox(0, half), "  ", m.packageBox(1, w-half-2)))
	} else {
		parts = append(parts, m.button("packages", "Packages", false)+"  "+components.Fit(m.packageSummary(), w-14))
	}
	if !m.wide() {
		label := "Dates & filters"
		if len(m.Session.Filter.Dates) > 0 {
			label = fmt.Sprintf("Dates & filters: %d", len(m.Session.Filter.Dates))
		}
		control := m.button("filters", label, false)
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Center, m.field("search-input", &m.SearchInput, min(36, w-lipgloss.Width(control)-2)), "  ", control))
	} else {
		parts = append(parts, "")
	}
	total := m.resultCount()
	label := "messages"
	if len(m.Rows) > 0 && m.Rows[0].Status == "missing" {
		label = "missing messages"
	}
	if total == 1 {
		label = strings.TrimSuffix(label, "s")
	}
	modes := map[string]string{"auto": "Auto", "missing": "Missing", "all": "All", "older": "Older", "newer": "Newer", "present": "In both", "new": "New"}
	header := components.Title.Render(number(total)+" "+label) + "  " + m.button("mode", modes[m.Session.Filter.Mode], false) + " " + m.button("previous", "‹", false) + " " + m.button("next", "›", false)
	if m.PackagesExpanded {
		header += " " + m.button("packages", "Hide packages", false)
	}
	if m.Session.Busy() {
		header += " " + m.button("stop", "Stop", false)
	}
	parts = append(parts, header)
	remaining := h - lipgloss.Height(strings.Join(parts, "\n")) - 1
	if len(m.Rows) == 0 {
		m.ScrollHeight = 0
		m.ScrollTotal = total
		m.ScrollVisible = 0
		text := "Add your older and newer packages to compare messages."
		if len(m.Snapshots) > 0 {
			text = "No matching messages"
		}
		if m.Session.Busy() {
			text = "Loading messages…"
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Width(w).Render(text))
	} else {
		parts = append(parts, m.messages(w, remaining))
	}
	return strings.Join(parts, "\n")
}
func (m *Model) packageSummary() string {
	names := [2]string{"No older package", "No newer package"}
	for _, s := range m.Snapshots {
		names[s.Slot] = filepath.Base(s.Path) + ", " + number(int(s.Count)) + " messages"
	}
	return names[0] + " → " + names[1]
}
func (m *Model) messages(w, h int) string {
	visible := min(len(m.Rows), max(1, h/2))
	total := m.resultCount()
	listWidth := w
	if total > visible {
		listWidth = max(20, w-2)
	}
	var rows []string
	for i, r := range m.Rows[:visible] {
		id := fmt.Sprintf("row-%d", i)
		serverID := fmt.Sprintf("row-server-%d", i)
		eligible := r.Guild != "" && (len(m.Session.Filter.Guilds) != 1 || m.Session.Filter.Guilds[0] != r.Guild)
		hover := m.Hover == id || m.Focus == id || m.Hover == serverID || m.Focus == serverID
		metaStyle := lipgloss.NewStyle().Foreground(components.Accent)
		contentStyle := lipgloss.NewStyle().Foreground(components.Text)
		rowStyle := lipgloss.NewStyle().Padding(0, 1).Width(listWidth - 2)
		if hover {
			rowStyle = rowStyle.Background(components.Surface)
			metaStyle = metaStyle.Bold(true).Foreground(lipgloss.Color("#FFB3E3"))
		}
		action := ""
		if eligible {
			m.Actions = append(m.Actions, serverID)
			if hover {
				action = components.Button(m.Zones, serverID, "Search this server", m.Hover, m.Focus, false)
			}
		}
		m.Actions = append(m.Actions, id)
		media := ""
		if r.HasMedia {
			media = ", media"
		} else if r.HasAttachments {
			media = ", attachment"
		}
		date := lipgloss.NewStyle().Foreground(components.Muted).Render(store.LocalDate(r.Date) + media)
		metaWidth := listWidth - 4
		if action != "" {
			metaWidth -= lipgloss.Width(action) + 1
		}
		nameWidth := max(8, metaWidth-lipgloss.Width(date)-2)
		if r.Status == "missing" {
			metaStyle = metaStyle.Foreground(components.Deleted)
		}
		name := metaStyle.Render(components.Fit(r.Server+" / "+displayChannel(r), nameWidth))
		meta := name + strings.Repeat(" ", max(1, metaWidth-lipgloss.Width(name)-lipgloss.Width(date))) + date
		if action != "" {
			meta += " " + action
		}
		contentWidth := listWidth - 4
		preview := r.Content
		if preview == "" && r.HasMedia {
			preview = "Media attachment"
		} else if preview == "" && r.HasAttachments {
			preview = "Attachment"
		}
		excerpt := components.SearchExcerpt(preview, m.Session.Filter.Search, contentWidth)
		content := components.Highlight(excerpt, m.Session.Filter.Search, contentStyle)
		rows = append(rows, m.Zones.Mark(id, rowStyle.Render(meta+"\n"+content)))
	}
	list := strings.Join(rows, "\n")
	if total <= visible {
		m.ScrollHeight = 0
		m.ScrollTotal = total
		m.ScrollVisible = visible
		return list
	}
	bar := m.scrollbar(total, visible, max(lipgloss.Height(list), h))
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", bar)
}
func (m *Model) resultCount() int {
	total := 0
	for _, group := range m.Groups {
		total += group.Count
	}
	return total
}
func (m *Model) scrollbar(total, visible, height int) string {
	m.ScrollHeight = height
	m.ScrollTotal = total
	m.ScrollVisible = visible
	thumbHeight := max(1, height*visible/max(1, total))
	travel := max(0, height-thumbHeight)
	thumbStart := 0
	if total > visible {
		thumbStart = travel * min(m.Offset, total-visible) / (total - visible)
	}
	lines := make([]string, height)
	for line := range height {
		id := fmt.Sprintf("scrollbar-%d", line)
		m.Actions = append(m.Actions, id)
		glyph := "│"
		color := components.Border
		if line >= thumbStart && line < thumbStart+thumbHeight {
			glyph = "┃"
			color = components.Accent
		}
		if m.Hover == id || m.Focus == id {
			glyph = "█"
			color = lipgloss.Color("#FFB3E3")
		}
		lines[line] = m.Zones.Mark(id, lipgloss.NewStyle().Foreground(color).Render(glyph))
	}
	return strings.Join(lines, "\n")
}
func (m *Model) serverColumns() int  { return max(1, min(3, (min(m.Width-10, 120)+2)/38)) }
func (m *Model) serverPageSize() int { return max(1, (m.Height-15)/2) * m.serverColumns() }
func (m *Model) filteredServers() []store.Group {
	var out []store.Group
	query := strings.ToLower(m.ServerSearch)
	for _, g := range m.Servers {
		if g.ID != "" && (query == "" || strings.Contains(strings.ToLower(g.Name), query) || g.ID == query) {
			out = append(out, g)
		}
	}
	return out
}
func (m *Model) servers() string {
	w := min(m.Width-10, 120)
	groups := m.filteredServers()
	m.ServerOffset = min(m.ServerOffset, max(0, ((len(groups)-1)/m.serverPageSize())*m.serverPageSize()))
	top := components.Title.Render("Servers") + "\n\n" + m.field("servers-input", &m.ServerInput, min(w, 38)) + "\n"
	top += m.button("servers-close", "Done", true) + " " + m.button("servers-clear", "Clear selection", false) + " " + m.button("servers-prev", "‹", false) + " " + m.button("servers-next", "›", false)
	top += "\n" + lipgloss.NewStyle().Foreground(components.Muted).Render(fmt.Sprintf("%d matches, %d selected", len(groups), len(m.Session.Filter.Guilds))) + "\n\n"
	if len(groups) == 0 {
		return lipgloss.NewStyle().Width(w).Render(top + "No matching servers")
	}
	columns := m.serverColumns()
	cellWidth := (w - (columns-1)*2) / columns
	lines := make([][]string, columns)
	limit := min(len(groups), m.ServerOffset+m.serverPageSize())
	peak := 1
	for _, g := range groups {
		peak = max(peak, g.Count)
	}
	for i := m.ServerOffset; i < limit; i++ {
		g := groups[i]
		id := "server-" + g.ID
		m.Actions = append(m.Actions, id)
		selected := slices.Contains(m.Session.Filter.Guilds, g.ID)
		mark := "[ ] "
		if selected {
			mark = "[x] "
		}
		style := lipgloss.NewStyle().Foreground(components.Text)
		boxStyle := lipgloss.NewStyle().Width(cellWidth-2).Padding(0, 1)
		if selected {
			boxStyle = boxStyle.Background(components.Surface)
			style = style.Foreground(components.Accent).Bold(true)
		}
		if m.Hover == id || m.Focus == id {
			boxStyle = boxStyle.Background(lipgloss.Color("#37445E"))
			style = style.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
		}
		name := components.Highlight(components.Fit(g.Name, cellWidth-6), m.ServerSearch, style)
		label := "messages"
		if g.Count == 1 {
			label = "message"
		}
		content := style.Render(mark) + name + "\n    " + components.Rule(float64(g.Count)/float64(peak), 7) + " " + lipgloss.NewStyle().Foreground(components.Muted).Render(number(g.Count)+" "+label)
		col := (i - m.ServerOffset) % columns
		lines[col] = append(lines[col], m.Zones.Mark(id, boxStyle.Render(content)))
	}
	var views []string
	for _, column := range lines {
		views = append(views, lipgloss.NewStyle().Width(cellWidth).Render(strings.Join(column, "\n")))
	}
	var grid []string
	for i, v := range views {
		if i > 0 {
			grid = append(grid, "  ")
		}
		grid = append(grid, v)
	}
	return top + lipgloss.JoinHorizontal(lipgloss.Top, grid...)
}
func (m *Model) console(w, h int) string {
	transcript := strings.Join(m.Transcript, "\n\n")
	if transcript == "" {
		transcript = session.HelpPanel(h)
	}
	if w < 110 {
		m.Viewport.Width = w
		m.Viewport.Height = h
		m.Viewport.SetContent(transcript)
		if m.ConsoleFollow && len(m.Transcript) > 0 {
			m.Viewport.GotoBottom()
		}
		return m.Viewport.View()
	}
	box := flexbox.New(w, h)
	left := flexbox.NewCell(3, 1).SetContentGenerator(func(x, y int) string {
		m.Viewport.Width = x - 2
		m.Viewport.Height = y
		m.Viewport.SetContent(lipgloss.NewStyle().Width(x - 2).Render(transcript))
		if m.ConsoleFollow && len(m.Transcript) > 0 {
			m.Viewport.GotoBottom()
		}
		return m.Viewport.View()
	})
	right := flexbox.NewCell(2, 1).SetContentGenerator(func(x, y int) string {
		return lipgloss.NewStyle().Width(x-4).Height(y-2).Border(lipgloss.RoundedBorder()).BorderForeground(components.Border).Padding(0, 1).Render(session.HelpPanel(y - 2))
	})
	box.AddRows([]*flexbox.Row{box.NewRow().AddCells(left, right)})
	return box.Render()
}
