package tui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
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
	if !m.enabled(id) {
		return components.DisabledButton(label)
	}
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
	actionID := map[string]string{"days-input": "days-apply", "search-input": "search-apply", "servers-input": "servers-search", "request-path": "request-save", "command": "command-run"}[id]
	action := ""
	if actionID != "" {
		label := "→"
		if id == "days-input" {
			label = "+"
		}
		action = m.button(actionID, label, false)
	}
	m.Actions = append(m.Actions, id)
	return components.InputField(m.Zones, id, input, w, m.Hover, m.Focus, action, "")
}
func (m *Model) modal(title, body string) string {
	border := components.GradientColor(.62)
	if m.workLabel() != "" {
		border = components.Brightness(border, .06)
	}
	width := min(m.Width-2, max(lipgloss.Width(body)+6, lipgloss.Width(title)+7))
	panel := components.TitledBox(title, body, width, 2, lipgloss.RoundedBorder(), border, m.Frame, m.workLabel() != "")
	return m.Zones.Scan(m.overlayMenu(lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, panel)))
}

func (m *Model) workLabel() string {
	if m.Picker != nil && m.Picker.Busy() {
		return "Inspecting folders and packages…"
	}
	if m.IncidentProcessing != "" {
		return m.IncidentProcessing
	}
	if m.ServerDialog && m.ServerInput.Value() != m.ServerSearch {
		return "Searching servers…"
	}
	if m.Tab == tabInvestigate && (m.SearchInput.Value() != m.Session.Filter.Search || m.Loading && m.Session.Filter.Search != "") {
		if len(m.Session.Filter.IncidentSeconds) > 0 {
			return "Finding messages at exact times…"
		}
		return "Searching messages…"
	}
	if m.Session.Busy() {
		return "Importing packages…"
	}
	if m.Tab == tabStats && m.StatsLoading {
		return "Computing statistics…"
	}
	if m.Executing {
		switch m.LastCommand {
		case "request":
			return "Saving deletion request…"
		case "remove":
			return "Clearing package…"
		case "wait":
			return "Waiting for imports…"
		default:
			return "Running " + m.LastCommand + "…"
		}
	}
	if m.Loading {
		return "Refreshing results…"
	}
	return ""
}

func (m *Model) statusLine(width int) string {
	if label := m.workLabel(); label != "" {
		return components.Working(label, m.Frame, width)
	}
	if m.Warning != "" && m.Warning != "Comparison pending" {
		return components.Warn.Render(components.Fit(m.Warning, width))
	}
	text := strings.TrimSpace(m.Notice)
	if text == "" || text == "Done" {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(components.Accent)
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "applied ") || strings.HasPrefix(lower, "saved ") {
		style = style.Foreground(components.Green).Bold(true)
	} else if strings.Contains(lower, "invalid") || strings.Contains(lower, "error") || strings.Contains(lower, "cannot") || strings.Contains(lower, "has no ") || strings.Contains(lower, "exceeds") {
		style = style.Foreground(components.Deleted)
	} else if strings.HasPrefix(lower, "stopping") || strings.HasPrefix(lower, "choose ") {
		style = components.Warn
	}
	return style.Render(components.Fit(text, width))
}

func (m *Model) appTitle(width int) string {
	return components.TitleRule("Find missing messages", width, m.Frame, m.workLabel() != "")
}
func (m *Model) View() string {
	m.Actions = nil
	m.HoverOnly = nil
	m.Tips = map[string]string{}
	defer m.previewMenu()()
	w := max(16, m.Width-4)
	h := m.bodyHeight()
	if m.Width < 64 || m.Height < 24 {
		return components.Gradient("Find missing messages") + "\n" + components.Warn.Render("Resize to at least 64 × 24.")
	}
	if m.Picker != nil {
		m.Picker.Height = max(1, m.Height-17)
		heading := components.TitleRule([]string{"Older package", "Newer package"}[m.PickSlot], w, m.Frame, m.Picker.Busy())
		body := m.Picker.View(m.Zones, w, m.Hover, m.Focus, m.Frame)
		m.Actions = m.Picker.Actions
		return m.Zones.Scan(lipgloss.NewStyle().Padding(0, 2).Render(heading + "\n" + body))
	}
	if m.Calendar != nil {
		m.Actions = []string{"cal-prev", "cal-next", "cal-apply", "cal-clear", "cal-close"}
		m.Actions = slices.DeleteFunc(m.Actions, func(id string) bool { return !m.enabled(id) })
		for d := 1; d <= m.Calendar.Month.AddDate(0, 1, -1).Day(); d++ {
			m.Actions = append(m.Actions, "date-"+m.Calendar.Month.AddDate(0, 0, d-1).Format(time.DateOnly))
		}
		return m.modal("Dates", m.Calendar.View(m.Zones, m.Hover, m.Focus, m.enabled("cal-apply")))
	}
	if m.MarginDialog {
		before := "Days before\n" + m.field("margin-before", &m.BeforeInput, 10)
		after := "Days after\n" + m.field("margin-after", &m.AfterInput, 10)
		body := lipgloss.JoinHorizontal(lipgloss.Top, before, "  ", after) + "\n" + m.button("margin-apply", "Apply", true) + " " + m.button("margin-clear", "Exact day", false) + " " + m.button("margin-close", "Cancel", false)
		if status := m.statusLine(44); status != "" {
			body += "\n\n" + status
		}
		return m.modal("Around each selected date", body)
	}
	if m.ServerDialog {
		title := "Servers"
		if m.ServerTarget == "stats" {
			title = "Statistics servers"
		}
		return m.modal(title, m.servers())
	}
	if m.FilterDialog {
		body := m.filters(34)
		if len(m.Session.Filter.Guilds) > 0 || len(m.Session.Filter.ExcludedGuilds) > 0 {
			body += "\n\n" + m.button("draft", "Deletion request", false)
		}
		body += "\n" + m.button("filters-close", "Done", true)
		if status := m.statusLine(34); status != "" {
			body += "\n\n" + status
		}
		return m.modal("Filters", body)
	}
	if m.RequestDialog {
		width := min(w-6, 70)
		scope := "All messages in the server selection"
		if m.RequestScope == "filtered" {
			scope = "Current matching messages"
		}
		body := serverSelectionLabel(m.Session.Filter.Guilds, m.Session.Filter.ExcludedGuilds) + "\n\n" + m.button("scope", scope, false) + "\n\n" + m.field("request-path", &m.RequestInput, width) + "\n\n" + m.button("request-close", "Cancel", false)
		if status := m.statusLine(width); status != "" {
			body += "\n\n" + status
		}
		return m.modal("Deletion request", body)
	}
	var tabs []string
	for i, name := range []string{"Investigate", "Stats", "Console", "Log"} {
		id := fmt.Sprintf("tab-%d", i)
		m.Actions = append(m.Actions, id)
		tabs = append(tabs, components.Tab(m.Zones, id, name, m.Hover, m.Focus, m.Tab == i, 2))
	}
	tabRow := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	tabRow += components.Separator(max(0, w-lipgloss.Width(tabRow)))
	var body string
	if m.Detail != nil {
		r := m.Detail
		m.Viewport.Width = w - 4
		m.Viewport.Height = h - 3
		titleStyle := components.Title
		if r.Status == "missing" {
			titleStyle = titleStyle.Foreground(components.Deleted)
		}
		messageContent := components.Highlight(r.Content, m.Session.Filter.Search, lipgloss.NewStyle().Foreground(components.Text))
		if strings.TrimSpace(r.Content) == "" {
			unavailable := "No text content"
			if r.SendEvent && !r.MessageRecord {
				unavailable = "Content is not included in the send_message analytics event."
			}
			messageContent = lipgloss.NewStyle().Foreground(components.Muted).Render(unavailable)
		}
		attachments := lipgloss.NewStyle().Foreground(components.Muted).Render("None")
		if len(r.AttachmentURLs) > 0 {
			lines := make([]string, 0, len(r.AttachmentURLs))
			for _, attachmentURL := range r.AttachmentURLs {
				lines = append(lines, ansi.Hardwrap(session.Safe(attachmentURL), max(20, w-4), false))
			}
			attachments = lipgloss.NewStyle().Foreground(components.Text).Render(strings.Join(lines, "\n"))
		} else if r.HasAttachments {
			attachments = lipgloss.NewStyle().Foreground(components.Muted).Render("URL unavailable in this export")
		}
		attachmentKind := "None"
		if r.HasMedia {
			attachmentKind = "Image, video or audio"
		} else if r.HasAttachments {
			attachmentKind = "Attachment"
		}
		dateDetails := lipgloss.NewStyle().Foreground(components.Muted).Render("Date        " + fullDateTime(r.Date) + "\nAge         " + relativeDate(r.Date, m.Session.Today) + "\nElapsed     " + calendarAge(r.Date, m.Session.Today))
		metadataLines := []string{"Status      " + r.Status, "Sources     " + strings.Join(r.Sources, ", "), "Attachment  " + attachmentKind, "Server ID   " + r.Guild, "Channel ID  " + r.Channel, "Message ID  " + r.ID}
		if r.SendEvent {
			if r.SendTime != "" {
				metadataLines = append(metadataLines, "Event time  "+fullDateTime(r.SendTime))
			}
			if r.SendEventID != "" {
				metadataLines = append(metadataLines, "Event ID    "+r.SendEventID)
			}
			if r.Platform != "" {
				metadataLines = append(metadataLines, "Client      "+r.Platform)
			}
			metadataLines = append(metadataLines, fmt.Sprintf("Reported    %s characters, %s words, %s URLs, %s attachments", number(r.ReportedLength), number(r.ReportedWords), number(r.ReportedURLs), number(r.ReportedFiles)))
		}
		metadata := lipgloss.NewStyle().Foreground(components.Muted).Render(strings.Join(metadataLines, "\n"))
		contentWidth := max(1, w-4)
		content := titleStyle.Render(session.Safe(r.Server)+" / "+displayChannel(*r)) + "\n\n" + dateDetails + "\n\n" + components.TitleRule("Message content", contentWidth, m.Frame, false) + "\n" + messageContent + "\n\n" + components.TitleRule("Attachments", contentWidth, m.Frame, false) + "\n" + attachments + "\n\n" + metadata
		m.Viewport.SetContent(lipgloss.NewStyle().Width(w - 4).Render(content))
		body = m.button("detail-close", "Back to results", false) + "\n\n" + m.Viewport.View()
	} else {
		switch m.Tab {
		case tabInvestigate:
			body = m.investigate(w, h)
		case tabStats:
			body = m.stats(w, h)
		case tabConsole:
			body = m.console(w, h)
		case tabLog:
			lines := m.Session.Log.Lines()
			for i, v := range lines {
				line := session.Safe(v)
				switch {
				case strings.Contains(line, "level=ERROR"):
					line = lipgloss.NewStyle().Foreground(components.Deleted).Render(line)
				case strings.Contains(line, "level=WARN"):
					line = components.Warn.Render(line)
				case strings.Contains(line, "level=INFO"):
					line = lipgloss.NewStyle().Foreground(components.Cyan).Render(line)
				}
				lines[i] = line
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
	footer := m.statusLine(w)
	if m.Tab == tabConsole {
		footer = m.field("command", &m.Input, w) + "\n" + footer
	}
	frame := m.Zones.Scan(m.overlayMenu(lipgloss.NewStyle().Padding(1, 2).Render(m.appTitle(w) + "\n" + tabRow + "\n\n" + lipgloss.NewStyle().Height(h).MaxHeight(h).Width(w).Render(body) + "\n" + footer)))
	if m.Menu != nil {
		return frame
	}
	return m.withTooltip(frame)
}

// withTooltip paints the hovered element's description over the frame without moving anything underneath.
func (m *Model) withTooltip(frame string) string {
	text := m.Tips[m.Hover]
	if text == "" {
		return frame
	}
	zone := m.Zones.Get(m.Hover)
	if zone == nil || zone.IsZero() {
		return frame
	}
	box := components.Tooltip(components.Fit(text, max(10, m.Width-6)))
	width, height := lipgloss.Width(box), lipgloss.Height(box)
	x := min(max(0, zone.EndX+1-width), max(0, m.Width-width))
	y := zone.StartY - height
	if y < 0 {
		y = zone.EndY + 1
	}
	return components.Overlay(frame, box, x, y)
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
		base := filepath.Base(snapshot.Path) + ", " + number(int(snapshot.Count)) + " messages"
		switch snapshot.State {
		case "loading":
			if snapshot.Total <= 0 {
				name = components.Working("Discovering package…", m.Frame, width-6)
			} else {
				percent := min(100, snapshot.Bytes*100/max(1, snapshot.Total))
				phase := map[string]string{
					"account": "reading account", "index": "reading index", "channels": "reading channels",
					"messages": "reading messages", "activity": "reading activity",
				}[snapshot.Phase]
				if phase == "" {
					phase = "reading package"
				}
				label := fmt.Sprintf("%d%%, %s, %s found", percent, phase, number(int(snapshot.Count)))
				barWidth := min(10, max(4, width-9-lipgloss.Width(label)))
				name = components.Spinner(m.Frame) + " " + components.Progress(float64(snapshot.Bytes)/float64(snapshot.Total), barWidth, m.Frame) + " " + lipgloss.NewStyle().Foreground(components.Cyan).Render(label)
			}
		case "stopped":
			name = components.Warn.Render("Stopped, " + number(int(snapshot.Count)) + " messages")
		case "partial":
			name = lipgloss.NewStyle().Foreground(components.Deleted).Render("Incomplete, " + number(int(snapshot.Count)) + " messages")
		default:
			name = lipgloss.NewStyle().Foreground(components.Green).Render(base)
		}
	}
	border := components.GradientColor(.48)
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
		border = components.Brightness(components.Saturation(components.GradientColor(.82), .06), .06)
	}
	if snapshot != nil && snapshot.State == "loading" {
		border = components.Brightness(components.GradientColor(.62), .04)
	}
	inner := width - 6
	buttons := m.button([]string{"browse-old", "browse-new"}[slot], "Browse", false)
	if snapshot != nil {
		buttons += " " + m.button([]string{"clear-old", "clear-new"}[slot], "Clear", false)
	}
	m.Actions = append(m.Actions, id)
	body := components.FitStyled(name, inner) + "\n" + buttons
	return m.Zones.Mark(id, components.TitledBox(label, body, width, 2, b, border, m.Frame, snapshot != nil && snapshot.State == "loading"))
}
func number(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func relativeDate(date string, today time.Time) string {
	messageTime, err := time.Parse(time.RFC3339Nano, date)
	if err != nil {
		return store.LocalDate(date)
	}
	messageTime = messageTime.In(time.Local)
	today = today.In(time.Local)
	messageDay := time.Date(messageTime.Year(), messageTime.Month(), messageTime.Day(), 0, 0, 0, 0, time.UTC)
	todayDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	days := int(todayDay.Sub(messageDay) / (24 * time.Hour))
	if days < 0 {
		return fmt.Sprintf("in %s days", number(-days))
	}
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%s days ago", number(days))
}
func (m *Model) messageDate(id, date string, color lipgloss.Color) string {
	label := relativeDate(date, m.Session.Today)
	messageTime, err := time.Parse(time.RFC3339Nano, date)
	if m.Hover == id {
		if err == nil && len(m.Session.Filter.IncidentSeconds) > 0 {
			label = messageTime.In(time.Local).Format("2006-01-02 15:04:05.000 -07:00")
		} else {
			label = store.LocalDate(date)
		}
	}
	m.HoverOnly = append(m.HoverOnly, id)
	return m.Zones.Mark(id, lipgloss.NewStyle().Foreground(color).Render(label))
}

func fullDateTime(date string) string {
	messageTime, err := time.Parse(time.RFC3339Nano, date)
	if err != nil {
		return date
	}
	return messageTime.In(time.Local).Format("2006-01-02 15:04:05.000 -07:00")
}

func calendarAge(date string, today time.Time) string {
	messageTime, err := time.Parse(time.RFC3339Nano, date)
	if err != nil {
		return "Unknown"
	}
	messageTime = messageTime.In(time.Local)
	today = today.In(time.Local)
	start := time.Date(messageTime.Year(), messageTime.Month(), messageTime.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	suffix := " ago"
	if start.After(end) {
		start, end = end, start
		suffix = " from now"
	}
	years := end.Year() - start.Year()
	cursor := start.AddDate(years, 0, 0)
	if cursor.After(end) {
		years--
		cursor = start.AddDate(years, 0, 0)
	}
	months := int(end.Month() - cursor.Month())
	if end.Year() > cursor.Year() {
		months += 12
	}
	if cursor.AddDate(0, months, 0).After(end) {
		months--
	}
	cursor = cursor.AddDate(0, months, 0)
	days := int(end.Sub(cursor) / (24 * time.Hour))
	if start.Equal(end) {
		return "0 days"
	}
	parts := make([]string, 0, 3)
	for _, part := range []struct {
		value int
		unit  string
	}{{years, "year"}, {months, "month"}, {days, "day"}} {
		if part.value == 0 {
			continue
		}
		unit := part.unit
		if part.value != 1 {
			unit += "s"
		}
		parts = append(parts, fmt.Sprintf("%s %s", number(part.value), unit))
	}
	return strings.Join(parts, ", ") + suffix
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
	parts := []string{m.field("search-input", &m.SearchInput, min(36, w)), components.Title.Render("Message dates"), m.button("clipboard", "Paste from clipboard", false), m.field("days-input", &m.DayInput, min(34, w))}
	parts = append(parts, m.button("dates", "Choose dates", false)+" "+m.button("dates-clear", "Clear", false))
	parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Render("Separate multiple dates with ;"))
	dates := m.Session.Filter.Dates
	incidents := m.Session.Filter.IncidentSeconds
	if len(incidents) > 0 {
		label := fmt.Sprintf("%d exact incident times", len(incidents))
		if len(incidents) == 1 {
			for second := range incidents {
				label = "Exact: " + time.Unix(second, 0).In(time.Local).Format("2006-01-02 15:04:05")
			}
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(components.Accent).Render(label))
	} else if len(dates) == 0 {
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
	if len(f.IncidentSeconds) > 0 {
		margin = "Exact incident seconds"
	}
	if f.DateBefore > 0 || f.DateAfter > 0 {
		margin = fmt.Sprintf("Date margin: %d before, %d after", f.DateBefore, f.DateAfter)
	}
	parts = append(parts, m.button("margin", components.Fit(margin, w-3), f.DateBefore > 0 || f.DateAfter > 0))
	media := optionLabel(mediaOptions, f.Media)
	parts = append(parts, m.dropdown("media", components.Fit(media, w-5), f.Media != ""))
	servers := serverSelectionLabel(f.Guilds, f.ExcludedGuilds)
	parts = append(parts, m.button("servers", servers, len(f.Guilds) > 0 || len(f.ExcludedGuilds) > 0)+" "+m.button("clear", "Reset", false))
	if f.Channel != "" {
		parts = append(parts, m.button("channel-clear", components.Fit("Channel: "+cmp.Or(m.ChannelLabel, f.Channel)+" ×", w-3), true))
	}
	return strings.Join(parts, "\n")
}
func (m *Model) filterPanel(w, h int) string {
	top := m.filters(w)
	if len(m.Session.Filter.Guilds) == 0 && len(m.Session.Filter.ExcludedGuilds) == 0 {
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
		parts = append(parts, m.button("packages", "Packages", false)+"  "+components.FitStyled(m.packageSummary(), w-14))
	}
	if !m.wide() {
		label := "Dates & filters"
		if len(m.Session.Filter.IncidentSeconds) > 0 {
			label = fmt.Sprintf("Times & filters: %d", len(m.Session.Filter.IncidentSeconds))
		} else if len(m.Session.Filter.Dates) > 0 {
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
	resultTitle := components.Title.Render(number(total) + " " + label)
	if m.Loading {
		resultTitle = components.Shimmer(number(total)+" "+label, m.Frame)
	}
	header := resultTitle + "  " + m.dropdown("mode", optionLabel(modeOptions, m.Session.Filter.Mode), false) + " " + m.button("previous", "‹", false) + " " + m.button("next", "›", false)
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
			parts = append(parts, components.Working("Loading messages…", m.Frame, w))
			return strings.Join(parts, "\n")
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
		if s.State == "loading" {
			if s.Total > 0 {
				percent := min(100, s.Bytes*100/max(1, s.Total))
				names[s.Slot] = components.Spinner(m.Frame) + " " + lipgloss.NewStyle().Foreground(components.Cyan).Render(fmt.Sprintf("%d%%, %s messages", percent, number(int(s.Count))))
			} else {
				names[s.Slot] = components.Spinner(m.Frame) + " " + components.Shimmer("Discovering…", m.Frame)
			}
			continue
		}
		name := filepath.Base(s.Path) + ", " + number(int(s.Count)) + " messages"
		if s.State == "ready" {
			names[s.Slot] = lipgloss.NewStyle().Foreground(components.Green).Render(name)
		} else if s.State == "partial" {
			names[s.Slot] = lipgloss.NewStyle().Foreground(components.Deleted).Render("Incomplete, " + number(int(s.Count)) + " messages")
		} else {
			names[s.Slot] = components.Warn.Render("Stopped, " + number(int(s.Count)) + " messages")
		}
	}
	return names[0] + " → " + names[1]
}

func messageIndex(id string) (int, bool) {
	for _, prefix := range []string{"row-server-", "row-date-", "row-"} {
		value, ok := strings.CutPrefix(id, prefix)
		if !ok {
			continue
		}
		index, err := strconv.Atoi(value)
		return index, err == nil
	}
	return 0, false
}

func (m *Model) activeMessage(visible int) int {
	for _, id := range []string{m.Hover, m.Focus} {
		if index, ok := messageIndex(id); ok && index >= 0 && index < visible {
			return index
		}
	}
	return -1
}

func (m *Model) messages(w, h int) string {
	visible := min(len(m.Rows), max(1, h/2))
	total := m.resultCount()
	active := m.activeMessage(visible)
	listWidth := w
	if total > visible {
		listWidth = max(20, w-2)
	}
	var rows []string
	for i, r := range m.Rows[:visible] {
		id := fmt.Sprintf("row-%d", i)
		serverID := fmt.Sprintf("row-server-%d", i)
		eligible := r.Guild != "" && (len(m.Session.Filter.Guilds) != 1 || m.Session.Filter.Guilds[0] != r.Guild || len(m.Session.Filter.ExcludedGuilds) > 0)
		distance := -1
		if active >= 0 {
			distance = i - active
			if distance < 0 {
				distance = -distance
			}
		}
		hover := active == i
		metaColor := components.Accent
		if r.Status == "missing" {
			metaColor = components.Deleted
		}
		metaStyle := lipgloss.NewStyle().Foreground(components.Fade(metaColor, distance))
		contentStyle := lipgloss.NewStyle().Foreground(components.Fade(components.Text, distance))
		mutedColor := components.Fade(components.Muted, distance)
		rowStyle := lipgloss.NewStyle().Padding(0, 1).Width(listWidth - 2)
		if hover {
			rowStyle = rowStyle.Background(components.Surface)
			metaStyle = metaStyle.Bold(true).Foreground(components.Pink)
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
		date := m.messageDate(fmt.Sprintf("row-date-%d", i), r.Date, mutedColor) + lipgloss.NewStyle().Foreground(mutedColor).Render(media)
		metaWidth := listWidth - 4
		if action != "" {
			metaWidth -= lipgloss.Width(action) + 1
		}
		nameWidth := max(8, metaWidth-lipgloss.Width(date)-2)
		name := metaStyle.Render(components.Fit(r.Server+" / "+displayChannel(r), nameWidth))
		meta := name + strings.Repeat(" ", max(1, metaWidth-lipgloss.Width(name)-lipgloss.Width(date))) + date
		if action != "" {
			meta += " " + action
		}
		contentWidth := listWidth - 4
		preview := messagePreview(r)
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

func messagePreview(r store.Row) string {
	if r.Content != "" {
		return r.Content
	}
	if r.SendEvent && !r.MessageRecord {
		return "Content unavailable, recovered from a send_message event"
	}
	if r.HasMedia {
		return "Media attachment"
	}
	if r.HasAttachments {
		return "Attachment"
	}
	return ""
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
	return m.scrollbarView("scrollbar", m.Offset, total, visible, height)
}

func (m *Model) scrollbarView(prefix string, offset, total, visible, height int) string {
	thumbHeight := max(1, height*visible/max(1, total))
	travel := max(0, height-thumbHeight)
	thumbStart := 0
	if total > visible {
		thumbStart = travel * min(offset, total-visible) / (total - visible)
	}
	lines := make([]string, height)
	for line := range height {
		id := fmt.Sprintf("%s-%d", prefix, line)
		m.Actions = append(m.Actions, id)
		glyph := "│"
		position := float64(line) / float64(max(1, height-1))
		color := components.Brightness(components.Saturation(components.GradientColor(position), -.16), -.5)
		if line >= thumbStart && line < thumbStart+thumbHeight {
			glyph = "┃"
			color = components.GradientColor(position)
		}
		if m.Hover == id || m.Focus == id {
			glyph = "█"
			color = components.Pink
		}
		lines[line] = m.Zones.Mark(id, lipgloss.NewStyle().Foreground(color).Render(glyph))
	}
	return strings.Join(lines, "\n")
}
func serverSelectionLabel(included, excluded []string) string {
	if len(included) == 0 && len(excluded) == 0 {
		return "All servers"
	}
	parts := make([]string, 0, 2)
	if len(included) > 0 {
		parts = append(parts, fmt.Sprintf("%d included", len(included)))
	}
	if len(excluded) > 0 {
		parts = append(parts, fmt.Sprintf("%d excluded", len(excluded)))
	}
	return strings.Join(parts, ", ")
}

func (m *Model) serverColumns() int  { return max(1, min(3, (min(m.Width-10, 120)+2)/38)) }
func (m *Model) serverPageSize() int { return max(1, (m.Height-16)/2) * m.serverColumns() }
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
	included, excluded := m.targetGuilds()
	top := m.field("servers-input", &m.ServerInput, min(w, 38)) + "\n"
	top += m.button("servers-close", "Done", true) + " " + m.button("servers-clear", "Clear selection", false) + " " + m.dropdown("servers-sort", optionLabel(serverSorts, m.ServerSort), m.ServerSort != "messages") + " " + m.button("servers-prev", "‹", false) + " " + m.button("servers-next", "›", false)
	top += "\n" + lipgloss.NewStyle().Foreground(components.Muted).Render(fmt.Sprintf("%d matches, %s", len(groups), strings.ToLower(serverSelectionLabel(*included, *excluded))))
	top += "\n" + lipgloss.NewStyle().Foreground(components.Muted).Render("Activate to cycle: include, exclude, any") + "\n"
	if m.ServerInput.Value() != m.ServerSearch {
		top += components.Working("Searching servers…", m.Frame, w) + "\n"
	}
	top += "\n"
	if len(groups) == 0 {
		if m.ServerInput.Value() != m.ServerSearch {
			return lipgloss.NewStyle().Width(w).Render(top)
		}
		return lipgloss.NewStyle().Width(w).Render(top + lipgloss.NewStyle().Foreground(components.Muted).Render("No matching servers"))
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
		include := slices.Contains(*included, g.ID)
		exclude := slices.Contains(*excluded, g.ID)
		mark := "[ ] "
		if include {
			mark = "[+] "
		} else if exclude {
			mark = "[-] "
		}
		style := lipgloss.NewStyle().Foreground(components.Text)
		boxStyle := lipgloss.NewStyle().Width(cellWidth-2).Padding(0, 1)
		if include {
			boxStyle = boxStyle.Background(components.Surface)
			style = style.Foreground(components.Accent).Bold(true)
		} else if exclude {
			boxStyle = boxStyle.Background(components.Surface)
			style = style.Foreground(components.Deleted).Bold(true)
		}
		if m.Hover == id || m.Focus == id {
			boxStyle = boxStyle.Background(components.SurfaceHover)
			style = style.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
		}
		name := components.Highlight(components.Fit(g.Name, cellWidth-6), m.ServerSearch, style)
		label := "messages"
		if g.Count == 1 {
			label = "message"
		}
		summary := number(g.Count) + " " + label
		if g.Missing > 0 {
			summary += ", " + number(g.Missing) + " missing"
		}
		content := style.Render(mark) + name + "\n    " + components.Rule(float64(g.Count)/float64(peak), 7) + " " + lipgloss.NewStyle().Foreground(components.Muted).Render(components.Fit(summary, cellWidth-14))
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
	blocks := make([]string, len(m.Transcript))
	for i, block := range m.Transcript {
		switch {
		case strings.HasPrefix(block, "› "):
			blocks[i] = lipgloss.NewStyle().Foreground(components.Cyan).Bold(true).Render(block)
		case strings.HasPrefix(block, "Error:"):
			blocks[i] = lipgloss.NewStyle().Foreground(components.Deleted).Render(block)
		case strings.HasPrefix(block, "Saved ") || strings.HasPrefix(block, "Applied "):
			blocks[i] = lipgloss.NewStyle().Foreground(components.Green).Render(block)
		default:
			blocks[i] = block
		}
	}
	transcript := strings.Join(blocks, "\n\n")
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
