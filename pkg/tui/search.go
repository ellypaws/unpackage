package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

func (m *Model) validateSearch() bool {
	q, err := store.ParseSearch(m.SearchInput.Value())
	if err == nil {
		err = m.Catalog.Validate(q)
	}
	if err == nil && len(m.Session.Filter.Guilds) > 0 {
		for _, t := range q.Tokens {
			if t.Key != "in" || t.Exclude {
				continue
			}
			found := false
			for _, c := range m.Catalog.Channels {
				if (strings.EqualFold(t.Value, c.ID) || strings.EqualFold(t.Value, c.Name)) && slices.Contains(m.Session.Filter.Guilds, c.Guild) {
					found = true
				}
			}
			if !found {
				err = fmt.Errorf("Channel is not in the selected server")
				break
			}
		}
	}
	m.SearchError = ""
	if err != nil {
		m.SearchError = err.Error()
	}
	m.SearchInput.TextStyle = lipgloss.NewStyle().Foreground(components.Text)
	if err != nil {
		m.SearchInput.TextStyle = m.SearchInput.TextStyle.Foreground(components.Deleted)
	}
	return err == nil
}

func (m *Model) updateCompletions() {
	catalog := m.Catalog
	f := m.Session.Filter
	if len(f.Guilds) > 0 || len(f.ExcludedGuilds) > 0 {
		catalog.Channels = nil
		for _, c := range m.Catalog.Channels {
			if (len(f.Guilds) == 0 || slices.Contains(f.Guilds, c.Guild)) && !slices.Contains(f.ExcludedGuilds, c.Guild) {
				catalog.Channels = append(catalog.Channels, c)
			}
		}
	}
	m.SearchSuggestions = session.CompleteSearch(m.SearchInput.Value(), m.SearchInput.Position(), catalog)
	m.SearchSelected = min(m.SearchSelected, max(0, len(m.SearchSuggestions)-1))
	var values []string
	for _, s := range m.SearchSuggestions {
		values = append(values, s.Value)
	}
	m.SearchInput.SetSuggestions(values)
	m.Input.SetSuggestions(session.CompleteCommand(m.Input.Value(), m.Catalog, m.History))
}

func (m *Model) acceptSearch(index int) tea.Cmd {
	if index < 0 || index >= len(m.SearchSuggestions) {
		return nil
	}
	s := m.SearchSuggestions[index]
	m.SearchInput.SetValue(s.Value)
	m.SearchInput.SetCursor(s.Cursor)
	m.Focus = "search-input"
	m.focusInput()
	m.SearchSelected = 0
	m.SearchDismissed = !strings.HasSuffix(strings.TrimSpace(string([]rune(s.Value)[:s.Cursor])), ":")
	m.validateSearch()
	m.updateCompletions()
	return m.debounce(false)
}

func (m *Model) searchKey(key string) (tea.Cmd, bool) {
	if key == "esc" {
		m.SearchDismissed = true
		return nil, true
	}
	if m.SearchDismissed || len(m.SearchSuggestions) == 0 {
		return nil, false
	}
	switch key {
	case "down":
		m.SearchSelected = (m.SearchSelected + 1) % len(m.SearchSuggestions)
		return nil, true
	case "up":
		m.SearchSelected = (m.SearchSelected + len(m.SearchSuggestions) - 1) % len(m.SearchSuggestions)
		return nil, true
	case "tab", "enter":
		return m.acceptSearch(m.SearchSelected), true
	}
	return nil, false
}

func (m *Model) searchAction(id string) (tea.Cmd, bool) {
	if value, ok := strings.CutPrefix(id, "suggest-"); ok {
		index, _ := strconv.Atoi(value)
		return m.acceptSearch(index), true
	}
	for _, prefix := range []string{"query-edit-", "query-remove-"} {
		if value, ok := strings.CutPrefix(id, prefix); ok {
			index, _ := strconv.Atoi(value)
			q, _ := store.ParseSearch(m.SearchInput.Value())
			if index < 0 || index >= len(q.Tokens) {
				return nil, true
			}
			t := q.Tokens[index]
			if prefix == "query-remove-" {
				r := []rune(m.SearchInput.Value())
				m.SearchInput.SetValue(strings.TrimSpace(string(r[:t.Start]) + string(r[t.End:])))
				m.SearchInput.CursorEnd()
			} else {
				cursor := t.Start + len([]rune(t.Key)) + 1
				if t.Exclude {
					cursor++
				}
				m.SearchInput.SetCursor(cursor)
			}
			m.Focus = "search-input"
			m.focusInput()
			m.SearchDismissed = false
			m.validateSearch()
			m.updateCompletions()
			return m.debounce(false), true
		}
	}
	return nil, false
}

func (m *Model) searchTokens(width int) string {
	q, _ := store.ParseSearch(m.SearchInput.Value())
	var lines []string
	for i, t := range q.Tokens {
		if t.Key == "" {
			continue
		}
		label := t.Key + ":" + t.Value
		if t.Exclude {
			label = "-" + label
		}
		for _, choices := range [][]store.SearchChoice{m.Catalog.Users, m.Catalog.Channels, m.Catalog.Servers} {
			for _, c := range choices {
				if c.ID == t.Value {
					label = t.Key + ":" + c.Name
					if t.Exclude {
						label = "-" + label
					}
					break
				}
			}
		}
		id := fmt.Sprintf("query-edit-%d", i)
		text := components.Fit(session.Safe(label), max(4, width-9))
		edit := m.button(id, text, true)
		if m.SearchError != "" && t.Key == "in" {
			edit = m.Zones.Mark(id, lipgloss.NewStyle().Foreground(components.Deleted).Background(components.Surface).Padding(0, 1).Render(text))
		}
		line := edit + m.button(fmt.Sprintf("query-remove-%d", i), "×", false)
		lines = append(lines, line)
	}
	if m.SearchError != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(components.Deleted).Width(width).Render(session.Safe(m.SearchError)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) overlaySearch(frame string) string {
	if m.Focus != "search-input" || m.SearchDismissed || m.Menu != nil || len(m.SearchSuggestions) == 0 {
		return frame
	}
	width := min(52, m.Width-6)
	count := min(6, len(m.SearchSuggestions), max(1, (m.Height-10)/2))
	start := max(0, m.SearchSelected-count+1)
	var lines []string
	for i := start; i < min(len(m.SearchSuggestions), start+count); i++ {
		s := m.SearchSuggestions[i]
		id := fmt.Sprintf("suggest-%d", i)
		style := lipgloss.NewStyle().Foreground(components.Text).Background(components.Surface).Width(width-2).Padding(0, 1)
		if i == m.SearchSelected || m.Hover == id {
			style = style.Background(components.SurfaceHover).Foreground(components.Accent).Bold(true)
		}
		label := components.Fit(session.Safe(s.Label), width-4)
		if s.Detail != "" {
			label += "\n" + components.Fit(session.Safe(s.Detail), width-4)
		}
		lines = append(lines, m.Zones.Mark(id, style.Render(label)))
		m.Actions = append([]string{id}, m.Actions...)
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(components.Accent).Background(components.Surface).Render(strings.Join(lines, "\n"))
	box = m.Zones.Mark("search-popup", box)
	x, y := 2, 7
	if zone := m.Zones.Get("search-input"); zone != nil && !zone.IsZero() {
		x = zone.StartX
		y = zone.EndY + 1
	}
	x = min(max(0, x), max(0, m.Width-lipgloss.Width(box)))
	y = min(max(0, y), max(0, m.Height-lipgloss.Height(box)))
	return components.Overlay(frame, box, x, y)
}
