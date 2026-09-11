package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case searchMsg:
		if v.Server {
			if v.Revision != m.ServerRevision || v.Value != m.ServerInput.Value() {
				return m, nil
			}
			m.ServerSearch = v.Value
			m.ServerOffset = 0
			return m, nil
		}
		if v.Revision != m.SearchRevision || v.Value != m.SearchInput.Value() {
			return m, nil
		}
		m.Session.Filter.Search = v.Value
		return m, m.changed()
	case dropCheckMsg:
		if v.Revision != m.DropRevision {
			return m, nil
		}
		return m, func() tea.Msg {
			p := components.CleanPath(v.Value)
			if filepath.IsAbs(p) {
				if st, e := os.Stat(p); e == nil && (st.IsDir() || strings.EqualFold(filepath.Ext(p), ".zip")) {
					return dropPathMsg{p, v.Revision, v.Slot}
				}
			}
			return nil
		}
	case dropPathMsg:
		if v.Revision != m.DropRevision || m.Executing || m.Picker != nil {
			return m, nil
		}
		m.DropBuffer = ""
		slot := v.Slot
		m.rememberPackage(slot, v.Path)
		m.DropSlot = slot
		m.Tab = tabInvestigate
		m.PackagesExpanded = true
		m.Input.SetValue("")
		return m, m.run(fmt.Sprintf("open %s \"%s\"", []string{"older", "newer"}[slot], v.Path))
	case tea.WindowSizeMsg:
		m.Width = max(20, v.Width)
		m.Height = max(10, v.Height)
		m.Input.Width = max(10, m.Width-6)
		m.Viewport.Width = max(10, m.Width-4)
		m.Viewport.Height = max(3, m.Height-13)
		if m.Picker != nil {
			return m, tea.Batch(m.Picker.Resize(m.Width-4, max(1, m.Height-17)), m.changed())
		}
		return m, m.changed()
	case tickMsg:
		pending := m.Session.Busy()
		for _, s := range m.Snapshots {
			pending = pending || s.State == "loading"
		}
		if pending {
			return m, tea.Batch(tick(), m.refresh())
		}
		return m, tick()
	case dataMsg:
		m.Loading = false
		if v.Revision != m.Revision {
			return m, m.refresh()
		}
		m.Rows = v.Rows
		m.Groups = v.Groups
		if m.ServerDialog {
			m.Servers = mergeServerOrder(m.Servers, v.Servers)
		} else {
			m.Servers = v.Servers
		}
		m.Days = v.Days
		m.Snapshots = v.Snapshots
		m.Warning = v.Warning
		if v.Err != nil {
			m.Notice = v.Err.Error()
		}
		m.Cursor = min(m.Cursor, max(0, len(m.Rows)-1))
		return m, nil
	case resultMsg:
		m.Executing = false
		if m.LastCommand == "search" || m.LastCommand == "clear" {
			m.SearchInput.SetValue(m.Session.Filter.Search)
			m.SearchRevision++
		}
		if m.LastCommand == "clear" {
			m.DayInput.SetValue("")
		}
		m.ConsoleFollow = m.LastCommand != "help"
		if !m.ConsoleFollow {
			m.Viewport.GotoTop()
		}
		if v.Err != nil {
			m.Notice = v.Err.Error()
			m.Transcript = append(m.Transcript, "Error: "+session.Safe(v.Err.Error()))
		} else {
			m.Notice = "Done"
			if m.LastCommand == "request" {
				m.RequestDialog = false
				m.Notice = strings.TrimSpace(v.Text)
			}
			if m.LastCommand == "open" {
				ss, _ := m.Session.Store.Snapshots(m.ctx)
				if len(ss) == 1 {
					m.DropSlot = 1 - ss[0].Slot
				}
			}
		}
		if v.Text != "" {
			m.Transcript = append(m.Transcript, strings.TrimSpace(v.Text))
		}
		if len(m.Transcript) > 100 {
			m.Transcript = m.Transcript[len(m.Transcript)-100:]
		}
		m.Offset = 0
		return m, m.refresh()
	case components.DirectoryMsg:
		if m.Picker != nil {
			return m, m.Picker.Apply(v)
		}
		return m, nil
	case components.DirectoryCountMsg:
		if m.Picker != nil {
			return m, m.Picker.ApplyCount(v)
		}
		return m, nil
	case components.PickedMsg:
		if m.Picker == nil || v.Generation != m.Picker.Generation {
			return m, nil
		}
		m.rememberPackage(m.PickSlot, v.Path)
		m.Picker.Close()
		m.Picker = nil
		m.Focus = m.defaultFocus()
		m.focusInput()
		return m, m.run(fmt.Sprintf("open %s \"%s\"", []string{"older", "newer"}[m.PickSlot], v.Path))
	case tea.BlurMsg:
		m.Hover = ""
		return m, nil
	case tea.MouseMsg:
		if m.Picker != nil {
			local := v
			local.X -= 2
			local.Y--
			id, cmd := m.Picker.Mouse(local)
			if id != "" {
				m.Hover = id
				if v.Action == tea.MouseActionRelease && v.Button == tea.MouseButtonLeft {
					m.Focus = "pick-input"
					m.focusInput()
					return m, tea.Batch(cmd, m.action(id))
				}
				return m, cmd
			}
		}
		m.Hover = ""
		for _, id := range m.Actions {
			if m.Zones.Get(id).InBounds(v) {
				m.Hover = id
				break
			}
		}
		if m.Hover == "open-old" || m.Hover == "browse-old" {
			m.DropSlot = 0
		}
		if m.Hover == "open-new" || m.Hover == "browse-new" {
			m.DropSlot = 1
		}
		if v.Button == tea.MouseButtonWheelDown || v.Button == tea.MouseButtonWheelUp {
			delta := 1
			if v.Button == tea.MouseButtonWheelUp {
				delta = -1
			}
			if m.Picker != nil {
				return m, nil
			}
			if m.Detail != nil || m.Tab == tabLog || m.Tab == tabConsole {
				m.FollowLog = false
				m.ConsoleFollow = false
				var cmd tea.Cmd
				m.Viewport, cmd = m.Viewport.Update(msg)
				return m, cmd
			}
			if m.Tab == tabInvestigate && !m.ServerDialog {
				m.Offset = max(0, min(max(0, m.ScrollTotal-m.ScrollVisible), m.Offset+delta*3))
				return m, m.refresh()
			}
			if m.ServerDialog {
				m.ServerOffset = max(0, min(max(0, len(m.filteredServers())-m.serverPageSize()), m.ServerOffset+delta*m.serverColumns()))
				return m, nil
			}
		}
		if v.Action == tea.MouseActionRelease && v.Button == tea.MouseButtonLeft && m.Hover != "" {
			m.Focus = m.Hover
			m.focusInput()
			return m, m.action(m.Hover)
		}
		return m, nil
	case tea.KeyMsg:
		key := v.String()
		if len(v.Runes) > 0 && !m.Executing && m.Picker == nil && m.Calendar == nil && !m.RequestDialog && !m.MarginDialog {
			text := string(v.Runes)
			if time.Since(m.DropTime) > 80*time.Millisecond || v.Paste {
				m.DropBuffer = ""
				m.PendingDropSlot = m.DropSlot
				m.DropFocus = m.Focus
				switch m.Focus {
				case "days-input":
					m.DropInput = m.DayInput.Value()
				case "search-input":
					m.DropInput = m.SearchInput.Value()
				}
			}
			m.DropBuffer += text
			m.DropTime = time.Now()
			m.DropRevision++
			if len(m.DropBuffer) > 4096 {
				m.DropBuffer = ""
			}
			candidate := m.DropBuffer
			revision := m.DropRevision
			if v.Paste {
				candidate = text
				m.DropBuffer = text
				m.PendingDropSlot = m.DropSlot
			}
			if filepath.IsAbs(components.CleanPath(candidate)) {
				switch m.DropFocus {
				case "days-input":
					m.DayInput.SetValue(m.DropInput)
				case "search-input":
					m.SearchInput.SetValue(m.DropInput)
				}
				m.Tab = tabInvestigate
				m.PackagesExpanded = true
				m.Notice = "Opening " + []string{"older", "newer"}[m.PendingDropSlot] + " package…"
				slot := m.PendingDropSlot
				return m, tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg { return dropCheckMsg{candidate, revision, slot} })
			}
		}
		if key == "ctrl+c" {
			if m.Picker != nil {
				m.Picker.Close()
			}
			m.Session.Stop()
			return m, tea.Quit
		}
		if key == "esc" {
			if m.Picker != nil {
				m.rememberBrowsing()
				m.Picker.Close()
			}
			m.Picker = nil
			m.Calendar = nil
			m.Detail = nil
			m.ServerDialog = false
			m.RequestDialog = false
			m.MarginDialog = false
			m.FilterDialog = false
			m.Focus = m.defaultFocus()
			m.focusInput()
			return m, nil
		}
		if m.Executing {
			if key == "ctrl+x" {
				m.Session.Stop()
			}
			return m, nil
		}
		if m.Picker != nil {
			if key == "ctrl+tab" || key == "shift+tab" {
				i := slices.Index(m.Actions, m.Focus)
				step := 1
				if key == "shift+tab" {
					step = -1
				}
				if len(m.Actions) > 0 {
					m.Focus = m.Actions[(i+step+len(m.Actions))%len(m.Actions)]
				}
				m.focusInput()
				return m, nil
			}
			if key == "enter" && m.Focus != "pick-input" {
				return m, m.action(m.Focus)
			}
			if key == "tab" && m.Focus != "pick-input" {
				m.Focus = "pick-input"
			}
			if len(v.Runes) > 0 {
				m.Focus = "pick-input"
			}
			m.focusInput()
			return m, m.Picker.Update(msg)
		}
		if m.Calendar != nil {
			c := m.Calendar
			if key == "tab" || key == "shift+tab" {
				controls := []string{"cal-prev", "cal-next", "cal-apply", "cal-clear", "cal-close"}
				controls = slices.DeleteFunc(controls, func(id string) bool { return !m.enabled(id) })
				i := slices.Index(controls, m.Focus)
				step := 1
				if key == "shift+tab" {
					step = -1
				}
				m.Focus = controls[(i+step+len(controls))%len(controls)]
				return m, nil
			}
			if (key == "enter" || key == " ") && strings.HasPrefix(m.Focus, "cal-") {
				return m, m.action(m.Focus)
			}
			switch key {
			case "left":
				m.Focus = "calendar"
				c.Cursor = c.Cursor.AddDate(0, 0, -1)
			case "right":
				m.Focus = "calendar"
				c.Cursor = c.Cursor.AddDate(0, 0, 1)
			case "up":
				m.Focus = "calendar"
				c.Cursor = c.Cursor.AddDate(0, 0, -7)
			case "down":
				m.Focus = "calendar"
				c.Cursor = c.Cursor.AddDate(0, 0, 7)
			case " ":
				c.Toggle(c.Cursor.Format(time.DateOnly))
			case "enter":
				return m, m.action("cal-apply")
			case "pgup":
				c.Cursor = c.Cursor.AddDate(0, -1, 0)
			case "pgdown":
				c.Cursor = c.Cursor.AddDate(0, 1, 0)
			}
			c.Month = time.Date(c.Cursor.Year(), c.Cursor.Month(), 1, 0, 0, 0, 0, time.Local)
			return m, nil
		}
		if key == "ctrl+x" {
			m.Session.Stop()
			m.Notice = "Stopping…"
			return m, nil
		}
		if (key == "pgup" || key == "pgdown") && (m.Tab == tabConsole || m.Tab == tabLog) {
			m.ConsoleFollow = false
			m.FollowLog = false
			var cmd tea.Cmd
			m.Viewport, cmd = m.Viewport.Update(msg)
			return m, cmd
		}
		if key == "ctrl+o" {
			return m, m.action("browse-old")
		}
		if key == "ctrl+n" {
			return m, m.action("browse-new")
		}
		if key == "ctrl+d" {
			return m, m.action("dates")
		}
		if key == "f1" {
			m.Tab = tabConsole
			return m, m.run("help")
		}
		if key == "ctrl+tab" {
			m.Tab = (m.Tab + 1) % 3
			m.Detail = nil
			if m.Tab == tabConsole {
				m.Focus = "command"
				m.Input.Focus()
			} else {
				m.Focus = m.defaultFocus()
				m.Input.Blur()
			}
			return m, nil
		}
		if key == "tab" || key == "shift+tab" {
			i := slices.Index(m.Actions, m.Focus)
			d := 1
			if key == "shift+tab" {
				d = -1
			}
			if len(m.Actions) > 0 {
				m.Focus = m.Actions[(i+d+len(m.Actions))%len(m.Actions)]
			}
			m.focusInput()
			if m.Focus == "open-old" {
				m.DropSlot = 0
			}
			if m.Focus == "open-new" {
				m.DropSlot = 1
			}
			return m, nil
		}
		if m.Detail != nil {
			var cmd tea.Cmd
			m.Viewport, cmd = m.Viewport.Update(msg)
			return m, cmd
		}
		if m.Focus == "days-input" || m.Focus == "search-input" || m.Focus == "request-path" || m.Focus == "margin-before" || m.Focus == "margin-after" || m.Focus == "servers-input" {
			m.focusInput()
			if key == "enter" {
				id := map[string]string{"days-input": "days-apply", "search-input": "search-apply", "request-path": "request-save", "margin-before": "margin-apply", "margin-after": "margin-apply", "servers-input": "servers-search"}[m.Focus]
				return m, m.action(id)
			}
			var cmd tea.Cmd
			switch m.Focus {
			case "days-input":
				m.DayInput, cmd = m.DayInput.Update(msg)
			case "search-input":
				return m, m.updateSearch(msg, false)
			case "servers-input":
				return m, m.updateSearch(msg, true)
			case "request-path":
				m.RequestInput, cmd = m.RequestInput.Update(msg)
			case "margin-before":
				m.BeforeInput, cmd = m.BeforeInput.Update(msg)
			case "margin-after":
				m.AfterInput, cmd = m.AfterInput.Update(msg)
			}
			return m, cmd
		}
		if m.Focus != "command" {
			if m.ServerDialog && slices.Contains([]string{"up", "down", "left", "right"}, key) {
				groups := m.filteredServers()
				if len(groups) == 0 {
					return m, nil
				}
				i := slices.IndexFunc(groups, func(g store.Group) bool { return m.Focus == "server-"+g.ID })
				step := map[string]int{"up": -m.serverColumns(), "down": m.serverColumns(), "left": -1, "right": 1}[key]
				i = max(0, min(len(groups)-1, i+step))
				m.ServerOffset = i / m.serverPageSize() * m.serverPageSize()
				m.Focus = "server-" + groups[i].ID
				return m, nil
			}
			switch key {
			case "enter", " ":
				return m, m.action(m.Focus)
			case "down":
				m.Cursor = min(max(0, len(m.Rows)-1), m.Cursor+1)
				if m.Tab == tabInvestigate && len(m.Rows) > 0 {
					m.Focus = fmt.Sprintf("row-%d", m.Cursor)
				}
			case "up":
				m.Cursor = max(0, m.Cursor-1)
				if m.Tab == tabInvestigate && len(m.Rows) > 0 {
					m.Focus = fmt.Sprintf("row-%d", m.Cursor)
				}
			case "pgdown":
				if m.ServerDialog {
					return m, m.action("servers-next")
				}
				return m, m.action("next")
			case "pgup":
				if m.ServerDialog {
					return m, m.action("servers-prev")
				}
				return m, m.action("previous")
			}
			return m, nil
		}
		switch key {
		case "enter":
			return m, m.submitCommand()
		case "up":
			if len(m.History) > 0 {
				m.HistoryIndex = max(0, m.HistoryIndex-1)
				m.Input.SetValue(m.History[m.HistoryIndex])
				m.Input.CursorEnd()
			}
			return m, nil
		case "down":
			m.HistoryIndex = min(len(m.History), m.HistoryIndex+1)
			m.Input.SetValue("")
			if m.HistoryIndex < len(m.History) {
				m.Input.SetValue(m.History[m.HistoryIndex])
			}
			return m, nil
		}
		if v.Paste {
			p := components.CleanPath(string(v.Runes))
			if _, e := os.Stat(p); e == nil {
				m.PickSlot = 0
				if len(m.Snapshots) > 0 {
					m.PickSlot = 1
				}
				picker := components.NewPicker(m.ctx, p)
				m.Picker = &picker
				return m, m.Picker.Navigate(p)
			}
		}
	}
	var cmd tea.Cmd
	if m.Picker != nil {
		return m, m.Picker.Update(msg)
	}
	if m.Tab == tabConsole {
		m.Input, cmd = m.Input.Update(msg)
	}
	if m.Focus == "days-input" {
		m.DayInput, cmd = m.DayInput.Update(msg)
	}
	if m.Focus == "search-input" {
		return m, m.updateSearch(msg, false)
	}
	if m.Focus == "servers-input" {
		return m, m.updateSearch(msg, true)
	}
	if m.Focus == "request-path" {
		m.RequestInput, cmd = m.RequestInput.Update(msg)
	}
	if m.Focus == "margin-before" {
		m.BeforeInput, cmd = m.BeforeInput.Update(msg)
	}
	if m.Focus == "margin-after" {
		m.AfterInput, cmd = m.AfterInput.Update(msg)
	}
	return m, cmd
}
func (m *Model) action(id string) tea.Cmd {
	if !m.enabled(id) {
		return nil
	}
	if strings.HasPrefix(id, "pick-") {
		if id == "pick-close" {
			if m.Picker != nil {
				m.rememberBrowsing()
				m.Picker.Close()
			}
			m.Picker = nil
			m.Focus = m.defaultFocus()
			m.focusInput()
			return nil
		}
		if m.Picker != nil {
			m.Focus = "pick-input"
			m.focusInput()
			return m.Picker.Action(id)
		}
	}
	if strings.HasPrefix(id, "date-") && m.Calendar != nil {
		m.Calendar.Toggle(strings.TrimPrefix(id, "date-"))
		return nil
	}
	if strings.HasPrefix(id, "cal-") && m.Calendar != nil {
		switch id {
		case "cal-prev":
			m.Calendar.Month = m.Calendar.Month.AddDate(0, -1, 0)
			m.Calendar.Cursor = m.Calendar.Month
		case "cal-next":
			m.Calendar.Month = m.Calendar.Month.AddDate(0, 1, 0)
			m.Calendar.Cursor = m.Calendar.Month
		case "cal-clear":
			m.Calendar.Dates = nil
		case "cal-close":
			m.Calendar = nil
			m.Focus = m.defaultFocus()
			m.focusInput()
		case "cal-apply":
			m.Session.Filter.Dates = slices.Clone(m.Calendar.Dates)
			m.Session.Filter.From = ""
			m.Session.Filter.Until = ""
			m.Calendar = nil
			m.DayInput.SetValue("")
			m.Focus = m.defaultFocus()
			m.focusInput()
			return m.changed()
		}
		return nil
	}
	if d, ok := strings.CutPrefix(id, "remove-date-"); ok {
		m.Session.Filter.Dates = slices.DeleteFunc(m.Session.Filter.Dates, func(v string) bool { return v == d })
		return m.changed()
	}
	if line, ok := strings.CutPrefix(id, "scrollbar-"); ok {
		position, err := strconv.Atoi(line)
		if err != nil || m.ScrollHeight < 1 || m.ScrollTotal <= m.ScrollVisible {
			return nil
		}
		m.Offset = min(m.ScrollTotal-m.ScrollVisible, max(0, position*(m.ScrollTotal-m.ScrollVisible)/max(1, m.ScrollHeight-1)))
		m.Cursor = 0
		m.Focus = "row-0"
		return m.refresh()
	}
	if g, ok := strings.CutPrefix(id, "server-"); ok {
		ids := m.Session.Filter.Guilds
		i := slices.Index(ids, g)
		if i >= 0 {
			ids = slices.Delete(ids, i, i+1)
		} else {
			ids = append(ids, g)
		}
		m.Session.Filter.Guilds = ids
		return m.changed()
	}
	var n int
	if _, e := fmt.Sscanf(id, "row-server-%d", &n); e == nil && n >= 0 && n < len(m.Rows) {
		if m.Rows[n].Guild == "" {
			return nil
		}
		m.Session.Filter.Guilds = []string{m.Rows[n].Guild}
		m.Focus = "row-0"
		return m.changed()
	}
	if _, e := fmt.Sscanf(id, "row-%d", &n); e == nil && n < len(m.Rows) {
		r := m.Rows[n]
		m.Detail = &r
		m.Viewport.GotoTop()
		return nil
	}
	if _, e := fmt.Sscanf(id, "tab-%d", &n); e == nil {
		m.Tab = n
		m.Detail = nil
		if n == tabConsole {
			m.Focus = "command"
			m.Input.Focus()
		} else {
			m.Input.Blur()
		}
		m.Viewport.GotoTop()
		return nil
	}
	switch id {
	case "servers-prev":
		m.ServerOffset = max(0, m.ServerOffset-m.serverPageSize())
	case "servers-next":
		m.ServerOffset = min(max(0, len(m.filteredServers())-1), m.ServerOffset+m.serverPageSize())
	case "open-old", "open-new":
		m.DropSlot = 0
		if id == "open-new" {
			m.DropSlot = 1
		}
		return nil
	case "browse-old", "browse-new":
		m.PickSlot = 0
		if id == "browse-new" {
			m.PickSlot = 1
		}
		m.DropSlot = m.PickSlot
		cwd := m.BrowseDirs[m.PickSlot]
		if cwd == "" {
			cwd = m.SharedBrowseDir
		}
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		p := components.NewPicker(m.ctx, cwd)
		p.Height = max(1, m.Height-15)
		m.Picker = &p
		m.DropRevision++
		m.DropBuffer = ""
		m.Focus = "pick-input"
		m.focusInput()
		return p.Navigate(cwd)
	case "stop":
		m.Session.Stop()
		m.Notice = "Stopping…"
	case "dates", "dates-more":
		c := components.NewCalendar(m.Session.Today, m.Session.Filter.Dates)
		m.Calendar = &c
	case "clear":
		m.DayInput.SetValue("")
		m.SearchInput.SetValue("")
		m.SearchRevision++
		return m.run("clear")
	case "mode":
		modes := []string{"auto", "missing", "all", "older", "newer", "present", "new"}
		i := slices.Index(modes, m.Session.Filter.Mode)
		m.Session.Filter.Mode = modes[(i+1)%len(modes)]
		return m.changed()
	case "media":
		media := []string{"", "attachments", "media"}
		i := slices.Index(media, m.Session.Filter.Media)
		m.Session.Filter.Media = media[(i+1)%len(media)]
		return m.changed()
	case "previous":
		m.Offset = max(0, m.Offset-m.pageSize())
		return m.refresh()
	case "next":
		if len(m.Rows) == m.pageSize() {
			m.Offset += len(m.Rows)
		}
		return m.refresh()
	case "scope":
		if m.RequestScope == "all" {
			m.RequestScope = "filtered"
		} else {
			m.RequestScope = "all"
		}
	case "draft":
		if len(m.Session.Filter.Guilds) == 0 {
			m.Notice = "Choose a server first"
			return nil
		}
		m.ServerDialog = false
		m.RequestDialog = true
		m.RequestInput.SetValue(fmt.Sprintf("deletion-request-%s.txt", time.Now().Format("20060102-150405")))
		m.Focus = "request-path"
		m.Notice = ""
		m.focusInput()
	case "request-save":
		return m.run(fmt.Sprintf("request \"%s\" %s", m.RequestInput.Value(), m.RequestScope))
	case "request-close":
		m.RequestDialog = false
		m.Focus = m.defaultFocus()
	case "packages":
		m.PackagesExpanded = !m.PackagesExpanded
		return m.changed()
	case "clear-old", "clear-new":
		slot := 0
		if id == "clear-new" {
			slot = 1
		}
		m.Executing = true
		m.LastCommand = "remove"
		m.Detail = nil
		m.DropSlot = slot
		m.DropRevision++
		m.Revision++
		return func() tea.Msg { return resultMsg{Err: m.Session.Clear(m.ctx, slot)} }
	case "servers":
		m.ServerDialog = true
		m.Focus = "servers-input"
		m.focusInput()
	case "servers-close":
		m.ServerDialog = false
		m.Focus = m.defaultFocus()
	case "servers-search":
		m.ServerRevision++
		m.ServerSearch = m.ServerInput.Value()
		m.ServerOffset = 0
		m.Focus = "servers-input"
		m.focusInput()
	case "filters":
		m.FilterDialog = true
		m.Focus = "days-input"
		m.focusInput()
	case "filters-close":
		m.FilterDialog = false
		m.Focus = m.defaultFocus()
	case "servers-clear":
		m.Session.Filter.Guilds = nil
		return m.changed()
	case "days-input", "search-input", "request-path", "margin-before", "margin-after", "servers-input":
		m.Focus = id
		m.focusInput()
	case "margin":
		m.MarginDialog = true
		m.BeforeInput.SetValue("")
		m.AfterInput.SetValue("")
		if m.Session.Filter.DateBefore > 0 {
			m.BeforeInput.SetValue(fmt.Sprint(m.Session.Filter.DateBefore))
		}
		if m.Session.Filter.DateAfter > 0 {
			m.AfterInput.SetValue(fmt.Sprint(m.Session.Filter.DateAfter))
		}
		m.Focus = "margin-before"
		m.Notice = ""
		m.focusInput()
	case "margin-apply":
		before, err := session.Margin(m.BeforeInput.Value())
		if err != nil {
			m.Notice = err.Error()
			return nil
		}
		after, err := session.Margin(m.AfterInput.Value())
		if err != nil {
			m.Notice = err.Error()
			return nil
		}
		m.Session.Filter.DateBefore = before
		m.Session.Filter.DateAfter = after
		m.MarginDialog = false
		m.Notice = ""
		m.Focus = m.defaultFocus()
		return m.changed()
	case "margin-clear":
		m.Session.Filter.DateBefore = 0
		m.Session.Filter.DateAfter = 0
		m.MarginDialog = false
		m.Focus = m.defaultFocus()
		return m.changed()
	case "margin-close":
		m.MarginDialog = false
		m.Focus = m.defaultFocus()
	case "days-apply":
		dates, err := session.Dates(m.DayInput.Value(), m.Session.Today)
		if err != nil {
			m.Notice = err.Error()
			return nil
		}
		m.Session.Filter.Dates = slices.Compact(slices.Sorted(slices.Values(append(m.Session.Filter.Dates, dates...))))
		m.DayInput.SetValue("")
		m.Session.Filter.From = ""
		m.Session.Filter.Until = ""
		m.Notice = ""
		m.Focus = "days-input"
		m.focusInput()
		return m.changed()
	case "dates-clear":
		m.DayInput.SetValue("")
		m.Session.Filter.Dates = nil
		m.Session.Filter.From = ""
		m.Session.Filter.Until = ""
		return m.changed()
	case "search-apply":
		m.SearchRevision++
		m.Session.Filter.Search = m.SearchInput.Value()
		m.Focus = "search-input"
		m.focusInput()
		return m.changed()
	case "command-run":
		m.Focus = "command"
		m.focusInput()
		return m.submitCommand()
	case "detail-close":
		m.Detail = nil
	case "command":
		m.Input.Focus()
	case "log-follow":
		m.FollowLog = true
	}
	return nil
}

func (m *Model) focusInput() {
	inputs := map[string]*textinput.Model{"command": &m.Input, "days-input": &m.DayInput, "search-input": &m.SearchInput, "request-path": &m.RequestInput, "margin-before": &m.BeforeInput, "margin-after": &m.AfterInput, "servers-input": &m.ServerInput}
	if m.Picker != nil {
		inputs["pick-input"] = &m.Picker.Path
	}
	if input := inputs[m.Focus]; input != nil && input.Focused() {
		return
	}
	m.Input.Blur()
	m.DayInput.Blur()
	m.SearchInput.Blur()
	m.RequestInput.Blur()
	m.BeforeInput.Blur()
	m.AfterInput.Blur()
	m.ServerInput.Blur()
	if m.Picker != nil {
		m.Picker.Path.Blur()
	}
	switch m.Focus {
	case "pick-input":
		if m.Picker != nil {
			m.Picker.Path.Focus()
		}
	case "command":
		if m.Tab == tabConsole {
			m.Input.Focus()
		}
	case "days-input":
		m.DayInput.Focus()
	case "search-input":
		m.SearchInput.Focus()
	case "servers-input":
		m.ServerInput.Focus()
	case "request-path":
		m.RequestInput.Focus()
	case "margin-before":
		m.BeforeInput.Focus()
	case "margin-after":
		m.AfterInput.Focus()
	}
}

func (m *Model) rememberPackage(slot int, path string) {
	abs, err := filepath.Abs(components.CleanPath(path))
	if err != nil {
		return
	}
	dir := filepath.Dir(abs)
	m.BrowseDirs[slot] = dir
	m.BrowseChosen[slot] = true
	m.SharedBrowseDir = dir
	if !m.BrowseChosen[1-slot] {
		m.BrowseDirs[1-slot] = dir
	}
}

func (m *Model) rememberBrowsing() {
	dir := m.Picker.Dir
	if dir == "" {
		return
	}
	m.BrowseDirs[m.PickSlot] = dir
	if !m.BrowseChosen[1-m.PickSlot] {
		m.SharedBrowseDir = dir
		m.BrowseDirs[1-m.PickSlot] = dir
	}
}

func (m *Model) submitCommand() tea.Cmd {
	line := m.Input.Value()
	if path := components.CleanPath(line); path != "" {
		if _, err := os.Stat(path); err == nil {
			m.PickSlot = 0
			if len(m.Snapshots) > 0 {
				m.PickSlot = 1
			}
			picker := components.NewPicker(m.ctx, path)
			m.Picker = &picker
			m.Focus = "pick-input"
			m.focusInput()
			return picker.Navigate(path)
		}
	}
	return m.run(line)
}

func (m *Model) updateSearch(msg tea.Msg, server bool) tea.Cmd {
	input := &m.SearchInput
	if server {
		input = &m.ServerInput
	}
	previous := input.Value()
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	if previous != input.Value() {
		return tea.Batch(cmd, m.debounce(server))
	}
	return cmd
}
func (m *Model) defaultFocus() string {
	switch m.Tab {
	case tabConsole:
		return "command"
	case tabLog:
		return "log-follow"
	default:
		return "days-input"
	}
}
