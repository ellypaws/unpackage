package tui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/store"
)

var (
	modeOptions        = []option{{"auto", "Auto"}, {"missing", "Missing"}, {"all", "All"}, {"older", "Older"}, {"newer", "Newer"}, {"present", "In both"}, {"new", "New"}}
	mediaOptions       = []option{{"", "All messages"}, {"attachments", "Messages with attachments"}, {"media", "Images, video or audio"}}
	channelTypeOptions = []option{{"server", "Server channels"}, {"dm", "Direct messages"}, {"group", "Group DMs"}, {"thread", "Threads"}, {"unknown", "Unknown"}}
)

// menu is an open dropdown anchored to the control that opened it.
type menu struct {
	ID      string
	Options []option
	Current string
	Hovered int
	Preview bool
}

// dropdown renders a control that opens a menu; the indicator flips while the menu is open.
func (m *Model) dropdown(id, label string, active bool) string {
	indicator := "▾ "
	if m.Menu != nil && m.Menu.ID == id {
		indicator = "▴ "
	}
	return m.button(id, indicator+label, active)
}

// menuFor describes the options behind a dropdown control and whether hovering an option may preview it.
func (m *Model) menuFor(id string) (options []option, current string, preview bool, ok bool) {
	metricOptions := func(metrics []store.Metric) []option {
		out := make([]option, 0, len(metrics))
		for _, metric := range metrics {
			out = append(out, option{metric.Key(), metric.Label()})
		}
		return out
	}
	switch id {
	case "stats-range":
		return statsRanges, m.StatsRange, false, true
	case "stats-layout":
		return statsLayouts, m.StatsLayout, true, true
	case "stats-cells":
		return statsCells, m.StatsCells, true, true
	case "stats-palette":
		return statsPalettes, m.StatsPalette, true, true
	case "stats-rows":
		return statsRowCounts, strconv.Itoa(m.StatsRowCount), true, true
	case "stats-entity":
		var out []option
		for _, entity := range store.Entities {
			out = append(out, option{string(entity), entityLabels[entity]})
		}
		return out, m.StatsEntity, true, true
	case "stats-metric":
		if m.StatsView == "entity" {
			return metricOptions(store.Entity(m.StatsEntity).Metrics()), m.leaderMetric().Key(), true, true
		}
		all := make([]store.Metric, 0, store.MetricCount)
		for metric := range store.MetricCount {
			all = append(all, metric)
		}
		return metricOptions(all), m.StatsMetric.Key(), true, true
	case "servers-sort":
		return serverSorts, m.ServerSort, true, true
	case "mode":
		return modeOptions, m.Session.Filter.Mode, false, true
	case "media":
		return mediaOptions, m.Session.Filter.Media, false, true
	case "channel-types":
		return channelTypeOptions, "", false, true
	}
	return nil, "", false, false
}

// setOption applies a dropdown choice; it is also used transiently for previews.
func (m *Model) setOption(id, key string) {
	switch id {
	case "stats-range":
		m.StatsRange = key
	case "stats-layout":
		m.StatsLayout = key
		m.HeatCursor = [2]int{}
	case "stats-cells":
		m.StatsCells = key
	case "stats-palette":
		m.StatsPalette = key
	case "stats-rows":
		if n, err := strconv.Atoi(key); err == nil {
			m.StatsRowCount = n
		}
	case "stats-entity":
		m.StatsEntity = key
	case "stats-metric":
		if metric, ok := store.ParseMetric(key); ok {
			m.StatsMetric = metric
		}
	case "servers-sort":
		m.ServerSort = key
		m.sortServers()
	case "mode":
		m.Session.Filter.Mode = key
	case "media":
		m.Session.Filter.Media = key
	}
}

func (m *Model) chooseOption(id, key string) tea.Cmd {
	if id == "channel-types" {
		m.toggleChannelType(key)
		return m.changed()
	}
	_, current, _, _ := m.menuFor(id)
	m.setOption(id, key)
	if key == current {
		return nil
	}
	switch id {
	case "stats-range":
		return m.statsChanged()
	case "mode", "media":
		return m.changed()
	case "stats-metric", "stats-entity":
		m.StatsOffset = 0
		m.StatsCursor = 0
	case "servers-sort":
		m.ServerOffset = 0
	}
	return nil
}

func (m *Model) openMenu(id string) {
	options, current, preview, ok := m.menuFor(id)
	if !ok {
		return
	}
	if m.Menu != nil && m.Menu.ID == id {
		m.Menu = nil
		return
	}
	hovered := slices.IndexFunc(options, func(o option) bool { return o.key == current })
	if id == "channel-types" && hovered < 0 && len(options) > 0 {
		hovered = 0
	}
	m.Menu = &menu{ID: id, Options: options, Current: current, Hovered: hovered, Preview: preview}
	m.Focus = id
}

// previewMenu applies the hovered option for the duration of one render and returns the restore step.
func (m *Model) previewMenu() func() {
	menu := m.Menu
	if menu == nil || !menu.Preview || menu.Hovered < 0 || menu.Hovered >= len(menu.Options) {
		return func() {}
	}
	key := menu.Options[menu.Hovered].key
	if key == menu.Current {
		return func() {}
	}
	m.setOption(menu.ID, key)
	return func() { m.setOption(menu.ID, menu.Current) }
}

func (m *Model) menuKey(key string) tea.Cmd {
	menu := m.Menu
	switch key {
	case "esc", "tab", "shift+tab":
		m.Menu = nil
	case "up", "down":
		step := 1
		if key == "up" {
			step = -1
		}
		menu.Hovered = (menu.Hovered + step + len(menu.Options)) % len(menu.Options)
	case "enter", " ":
		if menu.Hovered >= 0 && menu.Hovered < len(menu.Options) {
			if menu.ID != "channel-types" {
				m.Menu = nil
			}
			return m.chooseOption(menu.ID, menu.Options[menu.Hovered].key)
		}
		m.Menu = nil
	}
	return nil
}

// menuClick resolves a mouse release while a menu is open: choose an item, or close on any click elsewhere.
func (m *Model) menuClick() tea.Cmd {
	menu := m.Menu
	if index, ok := strings.CutPrefix(m.Hover, "menu-"); ok {
		if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(menu.Options) {
			if menu.ID != "channel-types" {
				m.Menu = nil
			}
			return m.chooseOption(menu.ID, menu.Options[i].key)
		}
		return nil
	}
	if m.Hover == "menu" {
		return nil
	}
	m.Menu = nil
	return nil
}

// overlayMenu paints the open menu over the frame before zone scanning so its items stay clickable.
func (m *Model) overlayMenu(frame string) string {
	menu := m.Menu
	if menu == nil {
		return frame
	}
	if index, ok := strings.CutPrefix(m.Hover, "menu-"); ok {
		if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(menu.Options) {
			menu.Hovered = i
		}
	}
	width := 8
	for _, o := range menu.Options {
		width = max(width, lipgloss.Width(o.label)+6)
	}
	width = min(width, m.Width-4)
	var lines []string
	for i, o := range menu.Options {
		id := fmt.Sprintf("menu-%d", i)
		m.Actions = append(m.Actions, id)
		style := lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(components.Text)
		marker := "  "
		if menu.ID == "channel-types" {
			marker = "[ ] "
			if slices.Contains(m.Session.Filter.ChannelTypes, o.key) {
				style = style.Foreground(components.Accent).Bold(true)
				marker = "[+] "
			} else if slices.Contains(m.Session.Filter.ExcludedChannelTypes, o.key) {
				style = style.Foreground(components.Deleted).Bold(true)
				marker = "[-] "
			}
		} else if o.key == menu.Current {
			style = style.Foreground(components.Accent).Bold(true)
			marker = "• "
		}
		if i == menu.Hovered {
			style = style.Background(components.SurfaceHover).Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
		}
		fitWidth := width - 4
		if menu.ID == "channel-types" {
			fitWidth = width - 6
		}
		lines = append(lines, m.Zones.Mark(id, style.Render(marker+components.Fit(o.label, fitWidth))))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(components.Accent).Background(components.Surface).Render(strings.Join(lines, "\n"))
	m.Actions = append(m.Actions, "menu")
	box = m.Zones.Mark("menu", box)
	boxWidth, boxHeight := lipgloss.Width(box), lipgloss.Height(box)
	x, y := 2, 2
	if zone := m.Zones.Get(menu.ID); zone != nil && !zone.IsZero() {
		x = zone.StartX
		y = zone.EndY + 1
		if y+boxHeight > m.Height && zone.StartY-boxHeight >= 0 {
			y = zone.StartY - boxHeight
		}
	}
	x = min(max(0, x), max(0, m.Width-boxWidth))
	y = min(max(0, y), max(0, m.Height-boxHeight))
	return components.Overlay(frame, box, x, y)
}

func (m *Model) toggleChannelType(category string) {
	included := &m.Session.Filter.ChannelTypes
	excluded := &m.Session.Filter.ExcludedChannelTypes
	if i := slices.Index(*included, category); i >= 0 {
		*included = slices.Delete(*included, i, i+1)
		*excluded = append(*excluded, category)
	} else if i := slices.Index(*excluded, category); i >= 0 {
		*excluded = slices.Delete(*excluded, i, i+1)
	} else {
		*included = append(*included, category)
	}
	slices.Sort(*included)
	slices.Sort(*excluded)
}

func channelTypesLabel(included, excluded []string) string {
	if len(included) == 0 && len(excluded) == 0 {
		return "Channel types: All"
	}
	if len(included)+len(excluded) == 1 {
		if len(included) == 1 {
			return "Channel types: " + optionLabel(channelTypeOptions, included[0])
		}
		return "Channel types: not " + optionLabel(channelTypeOptions, excluded[0])
	}
	return fmt.Sprintf("Channel types: +%d, -%d", len(included), len(excluded))
}

// figures remember the last value shown for each number so a change can flash by its relative size and settle.
type figureState struct {
	value     int
	changed   time.Time
	intensity float64
	up        bool
}

const figureSettle = 2 * time.Second

func (m *Model) figure(key, text string, value int, base lipgloss.Style, baseColor lipgloss.Color) string {
	now := time.Now()
	state, seen := m.Figures[key]
	if !seen {
		m.Figures[key] = figureState{value: value}
		return base.Render(text)
	}
	if state.value != value {
		delta := math.Abs(float64(value-state.value)) / math.Max(1, math.Abs(float64(state.value)))
		state = figureState{value: value, changed: now, intensity: min(1, delta), up: value > state.value}
		m.Figures[key] = state
	}
	if state.changed.IsZero() {
		return base.Render(text)
	}
	age := now.Sub(state.changed)
	if age >= figureSettle {
		return base.Render(text)
	}
	remaining := 1 - float64(age)/float64(figureSettle)
	hot := components.PaletteColor(m.StatsPalette, .35+.65*state.intensity)
	if !state.up {
		hot = components.Blend(components.Muted, components.Deleted, .35+.65*state.intensity)
	}
	return base.Foreground(components.Blend(baseColor, hot, remaining)).Bold(true).Render(text)
}
