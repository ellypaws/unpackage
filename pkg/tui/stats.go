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
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

type statsMsg struct {
	Stats    *store.Stats
	Err      error
	Revision int
}

type statsRowsMsg struct {
	Rows     []store.Row
	Err      error
	Revision int
}

type option struct{ key, label string }

// scope narrows every statistic to one channel, game, platform, or emoji after a drill-down.
type scope struct {
	Kind, ID, Label string
}

var (
	statsViews     = []option{{"overview", "Overview"}, {"activity", "Activity"}, {"leaders", "Top"}}
	statsRanges    = []option{{"30", "Last 30 days"}, {"90", "Last 90 days"}, {"365", "Last year"}, {"730", "Last 2 years"}, {"all", "All time"}}
	statsLayouts   = []option{{"hours", "Weekday and hour"}, {"weeks", "Calendar"}, {"months", "Months"}}
	statsCells     = []option{{"blocks", "Blocks"}, {"dots", "Dots"}, {"shades", "Shades"}, {"digits", "Digits"}}
	statsScales    = []option{{"linear", "Linear"}, {"log", "Log"}}
	statsPalettes  = []option{{"violet", "Violet"}, {"amber", "Amber"}, {"green", "Green"}, {"cyan", "Cyan"}}
	statsRowCounts = []option{{"5", "5 rows"}, {"8", "8 rows"}, {"12", "12 rows"}, {"20", "20 rows"}}
	serverSorts    = []option{{"messages", "Sort by messages"}, {"name", "Sort by name"}, {"missing", "Sort by missing"}}
	entityLabels   = map[store.Entity]string{store.EntityServers: "Servers", store.EntityChannels: "Channels", store.EntityPeople: "Conversations", store.EntityGames: "Games", store.EntityPlatforms: "Platforms", store.EntityEmoji: "Emoji"}
	weekdays       = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	weekdayNames   = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	topBoards      = []boardSpec{{"", store.EntityServers, store.MetricMessages}, {"", store.EntityServers, store.MetricVoiceTime}, {"", store.EntityServers, store.MetricMissing}, {"", store.EntityChannels, store.MetricMessages}, {"", store.EntityChannels, store.MetricMedia}, {"", store.EntityChannels, store.MetricLinks}, {"", store.EntityPeople, store.MetricMessages}, {"", store.EntityPeople, store.MetricVoiceTime}, {"", store.EntityPeople, store.MetricWords}, {"", store.EntityGames, store.MetricPlayTime}, {"", store.EntityPlatforms, store.MetricSessions}, {"", store.EntityEmoji, store.MetricReactions}}
)

const overviewRows = 5

func optionLabel(options []option, key string) string {
	for _, o := range options {
		if o.key == key {
			return o.label
		}
	}
	return key
}

func (m *Model) statsFilter() store.StatsFilter {
	f := store.StatsFilter{Guilds: slices.Clone(m.StatsGuilds), ExcludedGuilds: slices.Clone(m.StatsExcludedGuilds)}
	if days := session.RangeDays(m.StatsRange); days > 0 {
		f.From, f.Until = session.RangeWindow(days, m.Session.Today)
	}
	switch m.StatsScope.Kind {
	case "channel":
		f.Channel = m.StatsScope.ID
	case "game":
		f.Game = m.StatsScope.ID
	case "platform":
		f.Platform = m.StatsScope.ID
	case "emoji":
		f.Emoji = m.StatsScope.ID
	}
	return f
}

func (m *Model) statsStale() bool { return m.Stats == nil || m.StatsShown != m.StatsRevision }

func (m *Model) refreshStats() tea.Cmd {
	if m.StatsLoading {
		return nil
	}
	m.StatsLoading = true
	revision := m.StatsRevision
	f := m.statsFilter()
	s := m.Session.Store
	ctx := m.ctx
	return func() tea.Msg {
		st, err := s.Stats(ctx, f)
		return statsMsg{st, err, revision}
	}
}

func (m *Model) ensureStats() tea.Cmd {
	if m.statsStale() {
		return m.refreshStats()
	}
	return nil
}

func (m *Model) statsChanged() tea.Cmd {
	m.StatsRevision++
	m.StatsOffset = 0
	m.StatsCursor = 0
	return m.refreshStats()
}

func (m *Model) leaderMetric() store.Metric {
	metrics := store.Entity(m.StatsEntity).Metrics()
	if slices.Contains(metrics, m.StatsMetric) {
		return m.StatsMetric
	}
	return metrics[0]
}

func (m *Model) openEntity(entity store.Entity, metric store.Metric) {
	m.StatsEntity = string(entity)
	m.StatsMetric = metric
	m.StatsView = "entity"
	m.StatsOffset = 0
	m.StatsCursor = 0
	m.Focus = "stats-list"
}

func messageMetric(metric store.Metric) bool {
	return slices.Contains([]store.Metric{store.MetricMessages, store.MetricMissing, store.MetricMedia, store.MetricAttachments, store.MetricWords, store.MetricLinks}, metric)
}

// messageFilter selects the messages behind a ranked server, channel, or person for a message metric inside the current range.
func (m *Model) messageFilter(l store.Leader, metric store.Metric) (store.Filter, bool) {
	f := store.Filter{Mode: "all"}
	switch l.Kind {
	case "server":
		f.Guilds = []string{l.ID}
	case "guild", "dm", "unknown-dm", "group", "unknown", "conflict":
		f.Channel = l.ID
	default:
		return f, false
	}
	switch metric {
	case store.MetricMissing:
		if ok, _ := m.Session.Store.Compatible(m.ctx); ok {
			f.Mode = "missing"
		}
	case store.MetricMedia:
		f.Media = "media"
	case store.MetricAttachments:
		f.Media = "attachments"
	case store.MetricLinks:
		f.Search = "http"
	}
	if sf := m.statsFilter(); !sf.From.IsZero() {
		f.From = sf.From.Format(time.DateOnly)
		f.Until = sf.Until.Format(time.DateOnly)
	}
	return f, true
}

func (m *Model) openMessages(l store.Leader, metric store.Metric) tea.Cmd {
	f, ok := m.messageFilter(l, metric)
	if !ok {
		return nil
	}
	m.StatsTarget = l
	m.StatsTargetMetric = metric
	m.StatsView = "messages"
	m.StatsRows = nil
	m.StatsOffset = 0
	m.StatsRowsLoading = true
	m.StatsRowsRevision++
	m.Focus = "stats-view-entity"
	revision := m.StatsRowsRevision
	s := m.Session.Store
	ctx := m.ctx
	return func() tea.Msg {
		rows, err := s.Rows(ctx, f)
		rows = slices.Clone(rows)
		slices.Reverse(rows)
		return statsRowsMsg{rows, err, revision}
	}
}

// openLeader drills into a ranked item in the way that fits its kind and the metric it was ranked by.
func (m *Model) openLeader(l store.Leader, metric store.Metric) tea.Cmd {
	switch l.Kind {
	case "server":
		m.StatsGuilds = []string{l.ID}
		m.StatsExcludedGuilds = nil
		m.StatsScope = scope{}
		m.openEntity(store.EntityChannels, metric)
		return m.statsChanged()
	case "game":
		m.StatsScope = scope{"game", l.ID, leaderName(l)}
	case "platform":
		m.StatsScope = scope{"platform", l.ID, leaderName(l)}
	case "emoji":
		m.StatsScope = scope{"emoji", l.ID, leaderName(l)}
	default:
		if messageMetric(metric) {
			return m.openMessages(l, metric)
		}
		m.StatsScope = scope{"channel", l.ID, leaderName(l)}
	}
	m.StatsMetric = metric
	m.StatsView = "activity"
	m.Focus = "heat"
	return m.statsChanged()
}

func (m *Model) statsAction(id string) (tea.Cmd, bool) {
	if view, ok := strings.CutPrefix(id, "stats-view-"); ok {
		m.StatsView = view
		m.StatsOffset = 0
		if view == "entity" {
			m.Focus = "stats-list"
		}
		return m.ensureStats(), true
	}
	if target, ok := strings.CutPrefix(id, "stats-open-"); ok {
		entity, key, _ := strings.Cut(target, "/")
		metric, valid := store.ParseMetric(key)
		if !valid {
			metric = store.Entity(entity).Metrics()[0]
		}
		m.openEntity(store.Entity(entity), metric)
		return nil, true
	}
	if line, ok := strings.CutPrefix(id, "stats-scroll-"); ok {
		position, err := strconv.Atoi(line)
		if err != nil || m.StatsScrollHeight < 1 || m.StatsTotal <= m.StatsPage {
			return nil, true
		}
		m.StatsOffset = min(m.StatsTotal-m.StatsPage, max(0, position*(m.StatsTotal-m.StatsPage)/max(1, m.StatsScrollHeight-1)))
		return nil, true
	}
	if index, ok := strings.CutPrefix(id, "stats-row-"); ok {
		if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(m.StatsLeaders) {
			m.StatsCursor = i
			return m.openLeader(m.StatsLeaders[i], m.leaderMetric()), true
		}
		return nil, true
	}
	if target, ok := strings.CutPrefix(id, "crow-"); ok {
		entity, rest, _ := strings.Cut(target, "/")
		key, index, _ := strings.Cut(rest, "/")
		metric, valid := store.ParseMetric(key)
		i, err := strconv.Atoi(index)
		if !valid || err != nil || m.Stats == nil {
			return nil, true
		}
		leaders := m.topLeaders(store.Entity(entity), metric, m.cardLimit())
		if i >= 0 && i < len(leaders) {
			return m.openLeader(leaders[i], metric), true
		}
		return nil, true
	}
	if index, ok := strings.CutPrefix(id, "srow-"); ok {
		if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(m.StatsRows) {
			r := m.StatsRows[i]
			m.Detail = &r
			m.Viewport.GotoTop()
		}
		return nil, true
	}
	if _, _, _, ok := m.menuFor(id); ok {
		m.openMenu(id)
		return nil, true
	}
	switch id {
	case "stats-scope-clear":
		m.StatsScope = scope{}
		return m.statsChanged(), true
	case "stats-list":
		if m.StatsCursor >= 0 && m.StatsCursor < len(m.StatsLeaders) {
			return m.openLeader(m.StatsLeaders[m.StatsCursor], m.leaderMetric()), true
		}
		return nil, true
	case "stats-investigate":
		f, ok := m.messageFilter(m.StatsTarget, m.StatsTargetMetric)
		if !ok {
			return nil, true
		}
		m.Session.Filter = store.Filter{Mode: cmpMode(f.Mode), Limit: 50, Guilds: f.Guilds, ExcludedGuilds: f.ExcludedGuilds, Channel: f.Channel, Media: f.Media, Search: f.Search, From: f.From, Until: f.Until}
		m.SearchInput.SetValue(f.Search)
		m.SearchRevision++
		m.ChannelLabel = leaderName(m.StatsTarget)
		m.Tab = tabInvestigate
		m.Focus = m.defaultFocus()
		m.focusInput()
		return m.changed(), true
	case "stats-servers":
		m.ServerTarget = "stats"
		m.ServerDialog = true
		m.sortServers()
		m.Focus = "servers-input"
		m.focusInput()
		return nil, true
	case "stats-scale":
		m.StatsScale = optionLabelKey(statsScales, m.StatsScale)
		return nil, true
	case "stats-prev":
		m.StatsOffset = max(0, m.StatsOffset-m.StatsPage)
		return nil, true
	case "stats-next":
		if m.StatsOffset+m.StatsPage < m.StatsTotal {
			m.StatsOffset += m.StatsPage
		}
		return nil, true
	case "heat":
		return nil, true
	}
	return nil, false
}

func cmpMode(mode string) string {
	if mode == "" {
		return "auto"
	}
	return mode
}

// optionLabelKey flips a two-option setting.
func optionLabelKey(options []option, current string) string {
	for _, o := range options {
		if o.key != current {
			return o.key
		}
	}
	return current
}

func (m *Model) statsKey(key string) bool {
	if m.StatsView == "entity" && m.Focus == "stats-list" {
		switch key {
		case "down", "up":
			step := 1
			if key == "up" {
				step = -1
			}
			m.StatsCursor = max(0, min(max(0, m.StatsTotal-1), m.StatsCursor+step))
			if m.StatsCursor < m.StatsOffset {
				m.StatsOffset = m.StatsCursor
			}
			if m.StatsCursor >= m.StatsOffset+m.StatsPage {
				m.StatsOffset = m.StatsCursor - m.StatsPage + 1
			}
			return true
		}
	}
	if m.StatsView != "activity" {
		switch key {
		case "pgdown":
			m.action("stats-next")
			return true
		case "pgup":
			m.action("stats-prev")
			return true
		case "down":
			m.statsScroll(1)
			return true
		case "up":
			m.statsScroll(-1)
			return true
		}
		return false
	}
	if m.Focus != "heat" {
		return false
	}
	step, ok := map[string][2]int{"up": {-1, 0}, "down": {1, 0}, "left": {0, -1}, "right": {0, 1}}[key]
	if !ok {
		return false
	}
	m.HeatCursor[0] += step[0]
	m.HeatCursor[1] += step[1]
	return true
}

func (m *Model) statsScroll(delta int) {
	if m.StatsView == "activity" {
		return
	}
	if m.StatsView == "entity" || m.StatsView == "messages" {
		delta *= 3
	}
	m.StatsOffset = max(0, min(max(0, m.StatsTotal-m.StatsPage), m.StatsOffset+delta))
}

func (m *Model) cardLimit() int { return max(3, m.StatsRowCount) }

func metricValue(metric store.Metric, v int) string {
	if !metric.Duration() {
		return number(v)
	}
	hours, minutes := v/3600, v%3600/60
	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%sh %02dm", number(hours), minutes)
}

func metricUnit(metric store.Metric, v int) string {
	if metric.Duration() {
		return metricValue(metric, v) + " " + strings.ToLower(metric.Label())
	}
	label := strings.ToLower(metric.Label())
	if v == 1 {
		label = strings.TrimSuffix(label, "s")
	}
	return number(v) + " " + label
}

func humanDuration(seconds int) string {
	units := []struct {
		name string
		size int
	}{{"year", 365 * 86400}, {"month", 30 * 86400}, {"day", 86400}, {"hour", 3600}, {"minute", 60}}
	var parts []string
	for _, unit := range units {
		n := seconds / unit.size
		seconds %= unit.size
		if n == 0 {
			continue
		}
		name := unit.name
		if n != 1 {
			name += "s"
		}
		parts = append(parts, number(n)+" "+name)
	}
	if len(parts) == 0 {
		return "less than a minute"
	}
	return strings.Join(parts, ", ")
}

// valueTip describes a value in full for the hover overlay.
func valueTip(metric store.Metric, v, total int) string {
	text := metricUnit(metric, v)
	if metric.Duration() {
		text = humanDuration(v)
	}
	if total > 0 && v > 0 {
		text += fmt.Sprintf(", %.1f%% of total", 100*float64(v)/float64(total))
	}
	return text
}

func (m *Model) tip(id, text, content string) string {
	m.HoverOnly = append(m.HoverOnly, id)
	m.Tips[id] = text
	return m.Zones.Mark(id, content)
}

// hoverIndex reads the row index from a hovered id such as stats-row-3 or crow-servers/messages/2.
func hoverIndex(id string, prefixes ...string) (int, bool) {
	for _, prefix := range prefixes {
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			if i, err := strconv.Atoi(rest[strings.LastIndex(rest, "/")+1:]); err == nil {
				return i, true
			}
		}
	}
	return 0, false
}

func fadeDistance(active, i int) int {
	if active < 0 {
		return -1
	}
	if i > active {
		return i - active
	}
	return active - i
}

func (m *Model) rangeLabel() string {
	if m.Stats == nil {
		return optionLabel(statsRanges, m.StatsRange)
	}
	from, until := m.Stats.From, m.Stats.Until
	label := from.Format(time.DateOnly) + " to " + until.AddDate(0, 0, -1).Format(time.DateOnly)
	if from.IsZero() {
		label = "All time"
		if !m.Stats.First.IsZero() {
			label += ", " + m.Stats.First.Format(time.DateOnly) + " to " + m.Stats.Last.Format(time.DateOnly)
		}
	}
	if m.StatsScope.Kind != "" {
		label = m.StatsScope.Label + ", " + label
	}
	return label
}

func (m *Model) stats(w, h int) string {
	var parts []string
	var tabs []string
	for _, v := range statsViews {
		active := m.StatsView == v.key || v.key == "leaders" && (m.StatsView == "entity" || m.StatsView == "messages")
		tabs = append(tabs, m.button("stats-view-"+v.key, v.label, active))
	}
	serversLabel := serverSelectionLabel(m.StatsGuilds, m.StatsExcludedGuilds)
	header := strings.Join(tabs, " ") + "  " + m.dropdown("stats-range", optionLabel(statsRanges, m.StatsRange), m.StatsRange != "all") + " " + m.button("stats-servers", serversLabel, len(m.StatsGuilds) > 0 || len(m.StatsExcludedGuilds) > 0)
	if m.StatsScope.Kind != "" {
		chip := m.button("stats-scope-clear", components.Fit(strings.ToUpper(m.StatsScope.Kind[:1])+m.StatsScope.Kind[1:]+": "+m.StatsScope.Label+" ×", w-2), true)
		if lipgloss.Width(header)+lipgloss.Width(chip)+1 <= w {
			header += " " + chip
		} else {
			header += "\n" + chip
		}
	}
	parts = append(parts, header)
	switch m.StatsView {
	case "activity":
		parts = append(parts, m.dropdown("stats-metric", m.StatsMetric.Label(), false)+" "+m.dropdown("stats-layout", optionLabel(statsLayouts, m.StatsLayout), false)+" "+m.dropdown("stats-palette", optionLabel(statsPalettes, m.StatsPalette), false)+" "+m.dropdown("stats-cells", optionLabel(statsCells, m.StatsCells), false)+" "+m.button("stats-scale", optionLabel(statsScales, m.StatsScale), m.StatsScale != "linear"))
	case "leaders":
		parts = append(parts, m.dropdown("stats-rows", optionLabel(statsRowCounts, strconv.Itoa(m.StatsRowCount)), false)+" "+m.dropdown("stats-palette", optionLabel(statsPalettes, m.StatsPalette), false))
	case "entity":
		parts = append(parts, m.button("stats-view-leaders", "‹ Top", false)+" "+m.dropdown("stats-entity", entityLabels[store.Entity(m.StatsEntity)], false)+" "+m.dropdown("stats-metric", "by "+strings.ToLower(m.leaderMetric().Label()), false)+" "+m.button("stats-prev", "‹", false)+" "+m.button("stats-next", "›", false))
	case "messages":
		parts = append(parts, m.button("stats-view-entity", "‹ Back to ranking", false)+" "+m.button("stats-investigate", "Open in Investigate", false)+" "+m.button("stats-prev", "‹", false)+" "+m.button("stats-next", "›", false))
	}
	parts = append(parts, components.TitleRule(m.rangeLabel(), w, m.Frame, m.StatsLoading))
	remaining := h - lipgloss.Height(strings.Join(parts, "\n")) - 1
	if m.Stats == nil {
		if len(m.Snapshots) == 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Width(w).Render("Add a package to see statistics. Voice, game and session figures come from the Activity folder and appear once it finishes loading."))
		} else {
			parts = append(parts, components.Working("Computing statistics…", m.Frame, w))
		}
		return strings.Join(parts, "\n")
	}
	var body string
	switch m.StatsView {
	case "activity":
		body = m.activity(w, remaining)
	case "entity":
		body = m.entity(w, remaining)
	case "messages":
		body = m.statsMessages(w, remaining)
	case "leaders":
		body = m.top(w, remaining)
	default:
		body = m.overview(w, remaining)
	}
	parts = append(parts, body)
	return strings.Join(parts, "\n")
}

func (m *Model) importNote() string {
	for _, s := range m.Snapshots {
		if s.State == "loading" {
			return lipgloss.NewStyle().Foreground(components.Muted).Render("Import in progress. Voice, game and session figures are read last.")
		}
	}
	return ""
}

func peakCell(grid [7][24]int) (int, int, int) {
	day, hour, peak := 0, 0, 0
	for d := range grid {
		for h, v := range grid[d] {
			if v > peak {
				day, hour, peak = d, h, v
			}
		}
	}
	return day, hour, peak
}

// blocks lays out rows of content from the current offset, keeping every row whole so its zones stay intact.
func (m *Model) blocks(rows []string, w, h int) string {
	m.StatsTotal = len(rows)
	m.StatsOffset = min(m.StatsOffset, max(0, len(rows)-1))
	var out []string
	used, shown := 0, 0
	for i := m.StatsOffset; i < len(rows); i++ {
		height := lipgloss.Height(rows[i])
		if used+height > h && shown > 0 {
			break
		}
		out = append(out, rows[i])
		used += height + 1
		shown++
	}
	m.StatsPage = max(1, shown)
	body := strings.Join(out, "\n\n")
	if len(rows) <= shown && m.StatsOffset == 0 {
		m.StatsScrollHeight = 0
		return body
	}
	height := max(1, min(h, lipgloss.Height(body)))
	m.StatsScrollHeight = height
	bar := m.scrollbarView("stats-scroll", m.StatsOffset, len(rows), shown, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(w-2).Render(body), " ", bar)
}

func (m *Model) card(id, title, body string, width int) string {
	border := components.GradientColor(.48)
	var titleColor lipgloss.Color
	if id != "" {
		m.Actions = append(m.Actions, id)
		title = "▸ " + title
		border = components.Accent
		titleColor = components.Accent
		if m.Hover == id || m.Focus == id {
			border = components.Pink
			titleColor = components.Pink
		}
	}
	box := components.TitledBox(title, body, width, 1, lipgloss.RoundedBorder(), border, titleColor, m.Frame, false)
	if id == "" {
		return box
	}
	return m.Zones.Mark(id, box)
}

type boardSpec struct {
	title  string
	entity store.Entity
	metric store.Metric
}

func (m *Model) boardCard(spec boardSpec, width int, limit int) string {
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	allLeaders := m.topLeaders(spec.entity, spec.metric, 1<<20)
	leaders := allLeaders[:min(limit, len(allLeaders))]
	inner := max(8, width-4)
	var lines []string
	if len(leaders) == 0 {
		lines = append(lines, muted.Render("No data"))
	}
	total := 0
	for _, l := range allLeaders {
		total += l.Values[spec.metric]
	}
	valueWidth := 0
	for _, l := range leaders {
		valueWidth = max(valueWidth, lipgloss.Width(metricValue(spec.metric, l.Values[spec.metric])))
	}
	peak := 1
	if len(leaders) > 0 {
		peak = max(1, leaders[0].Values[spec.metric])
	}
	barWidth := 6
	if inner < 30 {
		barWidth = 0
	}
	prefix := string(spec.entity) + "/" + spec.metric.Key() + "/"
	active := -1
	if i, ok := hoverIndex(m.Hover, "crow-"+prefix, "ctip-"+prefix); ok {
		active = i
	}
	for i, l := range leaders {
		distance := fadeDistance(active, i)
		nameColor := components.Fade(components.Text, distance)
		valueColor := components.Fade(components.Muted, distance)
		nameStyle := lipgloss.NewStyle().Foreground(nameColor)
		if i == active {
			nameStyle = nameStyle.Bold(true).Foreground(components.Pink)
			valueColor = components.Text
		}
		amount := fmt.Sprintf("%*s", valueWidth, metricValue(spec.metric, l.Values[spec.metric]))
		nameWidth := max(4, inner-valueWidth-barWidth-2)
		name := components.Fit(leaderName(l), nameWidth)
		line := nameStyle.Render(name) + strings.Repeat(" ", max(1, nameWidth-lipgloss.Width(name)+1))
		if barWidth > 0 {
			line += heatRule(m.StatsPalette, float64(l.Values[spec.metric])/float64(peak), barWidth) + " "
		}
		value := m.figure("card/"+prefix+l.ID, amount, l.Values[spec.metric], lipgloss.NewStyle().Foreground(valueColor), valueColor)
		line += m.tip("ctip-"+prefix+strconv.Itoa(i), valueTip(spec.metric, l.Values[spec.metric], total), value)
		rowID := "crow-" + prefix + strconv.Itoa(i)
		m.HoverOnly = append(m.HoverOnly, rowID)
		lines = append(lines, m.Zones.Mark(rowID, line))
	}
	if remaining := len(allLeaders) - len(leaders); remaining > 0 {
		remainingTotal := 0
		for _, l := range allLeaders[len(leaders):] {
			remainingTotal += l.Values[spec.metric]
		}
		label := fmt.Sprintf("…%s more", number(remaining))
		value := components.Fit(metricUnit(spec.metric, remainingTotal), max(1, inner-5))
		labelWidth := max(4, inner-lipgloss.Width(value)-1)
		label = components.Fit(label, labelWidth)
		line := label + strings.Repeat(" ", max(1, labelWidth-lipgloss.Width(label)+1)) + value
		lines = append(lines, muted.Render(line))
	}
	title := spec.title
	if title == "" {
		title = entityLabels[spec.entity] + " by " + strings.ToLower(spec.metric.Label())
	}
	return m.card("stats-open-"+string(spec.entity)+"/"+spec.metric.Key(), title, strings.Join(lines, "\n"), width)
}

func (m *Model) cardRows(specs []boardSpec, w int, limit int) []string {
	columns := max(1, min(3, w/30))
	cardWidth := (w - (columns-1)*2) / columns
	var rows []string
	for start := 0; start < len(specs); start += columns {
		var cells []string
		for i, spec := range specs[start:min(len(specs), start+columns)] {
			if i > 0 {
				cells = append(cells, "  ")
			}
			cells = append(cells, m.boardCard(spec, cardWidth, limit))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	return rows
}

type tile struct {
	label, value, tip string
	amount            int
}

func (m *Model) tileCard(title string, tiles []tile, width int, compact bool) string {
	label := lipgloss.NewStyle().Foreground(components.Muted)
	value := lipgloss.NewStyle().Foreground(components.Text).Bold(true)
	inner := max(8, width-4)
	columns := max(1, min(4, (inner+2)/20))
	tileWidth := inner / columns
	var lines []string
	for i := 0; i < len(tiles); i += columns {
		var cells []string
		for j, t := range tiles[i:min(len(tiles), i+columns)] {
			rendered := m.figure("tile/"+title+"/"+t.label, t.value, t.amount, value, components.Text)
			if t.tip != "" {
				rendered = m.tip(fmt.Sprintf("tip-%s-%d", strings.ToLower(title), i+j), t.tip, rendered)
			}
			cell := label.Render(components.Fit(t.label, tileWidth-1)) + "\n" + rendered
			if compact {
				cell = label.Render(components.Fit(t.label, max(4, tileWidth-lipgloss.Width(t.value)-2))) + " " + rendered
			}
			cells = append(cells, lipgloss.NewStyle().Width(tileWidth).Render(cell))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	return m.card("", title, strings.Join(lines, "\n"), width)
}

func (m *Model) overview(w, h int) string {
	st := m.Stats
	cw := w - 2
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	count := func(metric store.Metric) tile {
		return tile{metric.Label(), number(st.Totals[metric]), "", st.Totals[metric]}
	}
	duration := func(name string, seconds int) tile {
		return tile{name, metricValue(store.MetricVoiceTime, seconds), humanDuration(seconds), seconds}
	}
	messages := []tile{count(store.MetricMessages), {"Missing", number(st.Totals[store.MetricMissing]), "", st.Totals[store.MetricMissing]}, count(store.MetricMedia), count(store.MetricAttachments), count(store.MetricLinks), count(store.MetricWords)}
	if st.Totals[store.MetricMessages] > 0 {
		average := st.Characters / st.Totals[store.MetricMessages]
		messages = append(messages, tile{"Average length", number(average) + " chars", fmt.Sprintf("%s characters in %s", number(st.Characters), metricUnit(store.MetricMessages, st.Totals[store.MetricMessages])), average})
	}
	messages = append(messages, tile{"Active days", number(st.ActiveDays), "", st.ActiveDays}, tile{"Longest streak", number(st.LongestStreak) + " days", "consecutive days with at least one message", st.LongestStreak})
	if !st.FirstMessage.IsZero() {
		messages = append(messages, tile{"First message", st.FirstMessage.Format(time.DateOnly), calendarAge(st.FirstMessage.Format(time.RFC3339Nano), m.Session.Today), int(st.FirstMessage.Unix())})
	}
	day, hour, peak := peakCell(st.Weekly[store.MetricMessages])
	if peak > 0 {
		messages = append(messages, tile{"Peak hour", fmt.Sprintf("%s %02d:00", weekdays[day], hour), fmt.Sprintf("%s %02d:00 to %02d:00, %s", weekdayNames[day], hour, (hour+1)%24, metricUnit(store.MetricMessages, peak)), day*24 + hour})
		weekday, weekdayCount := 0, 0
		for d := range st.Weekly[store.MetricMessages] {
			sum := 0
			for _, v := range st.Weekly[store.MetricMessages][d] {
				sum += v
			}
			if sum > weekdayCount {
				weekday, weekdayCount = d, sum
			}
		}
		messages = append(messages, tile{"Busiest weekday", weekdayNames[weekday], metricUnit(store.MetricMessages, weekdayCount), weekday})
	}
	busiest, busiestCount := "", 0
	for date, n := range st.Daily[store.MetricMessages] {
		if n > busiestCount || n == busiestCount && date < busiest {
			busiest, busiestCount = date, n
		}
	}
	if busiestCount > 0 {
		messages = append(messages, tile{"Busiest day", busiest, metricUnit(store.MetricMessages, busiestCount), busiestCount})
	}
	voice := []tile{duration("Voice time", st.Totals[store.MetricVoiceTime]), count(store.MetricVoiceSessions), duration("Longest session", st.LongestVoice)}
	if st.Totals[store.MetricVoiceSessions] > 0 {
		voice = append(voice, duration("Average session", st.Totals[store.MetricVoiceTime]/st.Totals[store.MetricVoiceSessions]))
	}
	games := len(m.topLeaders(store.EntityGames, store.MetricPlayTime, 1<<20))
	voice = append(voice, duration("Play time", st.Totals[store.MetricPlayTime]), tile{"Games", number(games), "", games}, count(store.MetricSessions), count(store.MetricStreams))
	if top := m.topLeaders(store.EntityPlatforms, store.MetricSessions, 1); len(top) > 0 {
		voice = append(voice, tile{"Top platform", top[0].Name, metricUnit(store.MetricSessions, top[0].Values[store.MetricSessions]), top[0].Values[store.MetricSessions]})
	}
	servers := len(m.topLeaders(store.EntityServers, store.MetricMessages, 1<<20))
	channels := len(m.topLeaders(store.EntityChannels, store.MetricMessages, 1<<20))
	conversations := len(m.topLeaders(store.EntityPeople, store.MetricMessages, 1<<20))
	community := []tile{{"Servers", number(servers), "servers with messages", servers}, {"Channels", number(channels), "channels with messages", channels}, {"Conversations", number(conversations), "direct and group conversations with messages", conversations}, {"Servers joined", number(st.Joined), "", st.Joined}, count(store.MetricReactions)}
	if top := m.topLeaders(store.EntityEmoji, store.MetricReactions, 1); len(top) > 0 {
		community = append(community, tile{"Top emoji", leaderName(top[0]), metricUnit(store.MetricReactions, top[0].Values[store.MetricReactions]), top[0].Values[store.MetricReactions]})
	}
	community = append(community, count(store.MetricEdits), tile{"Deletions", number(st.Totals[store.MetricDeletions]), "deletions recorded by Discord analytics, not the older versus newer comparison", st.Totals[store.MetricDeletions]})
	compact := h < 22
	rows := []string{m.tileCard("Messages", messages, cw, compact)}
	if note := m.importNote(); note != "" {
		rows = append(rows, note)
	}
	if st.Totals[store.MetricMessages] == 0 && st.Totals[store.MetricSessions] == 0 && st.Totals[store.MetricVoiceSessions] == 0 {
		rows = append(rows, muted.Render("No activity in this range. Choose a wider range to include older data."))
		return m.blocks(rows, w, h)
	}
	columns := max(1, min(2, cw/44))
	groupWidth := (cw - (columns-1)*2) / columns
	groups := []string{m.tileCard("Voice and apps", voice, groupWidth, compact), m.tileCard("Community", community, groupWidth, compact)}
	if columns == 2 {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, groups[0], "  ", groups[1]))
	} else {
		rows = append(rows, groups...)
	}
	boards := []boardSpec{{"Top servers", store.EntityServers, store.MetricMessages}, {"Top conversations", store.EntityPeople, store.MetricMessages}, {"Top games", store.EntityGames, store.MetricPlayTime}, {"Most voice time", store.EntityServers, store.MetricVoiceTime}, {"Most missing", store.EntityServers, store.MetricMissing}, {"Most media", store.EntityChannels, store.MetricMedia}, {"Most words", store.EntityPeople, store.MetricWords}, {"Most links", store.EntityChannels, store.MetricLinks}, {"Most edited", store.EntityChannels, store.MetricEdits}}
	rows = append(rows, m.cardRows(boards, cw, overviewRows)...)
	return m.blocks(rows, w, h)
}

func (m *Model) top(w, h int) string {
	rows := m.cardRows(topBoards, w-2, m.cardLimit())
	if note := m.importNote(); note != "" {
		rows = append(rows, note)
	}
	return m.blocks(rows, w, h)
}

func (m *Model) topLeaders(entity store.Entity, metric store.Metric, limit int) []store.Leader {
	leaders := slices.Clone(m.Stats.Leaders(entity))
	leaders = slices.DeleteFunc(leaders, func(l store.Leader) bool { return l.Values[metric] == 0 })
	store.SortLeaders(leaders, metric)
	return leaders[:min(limit, len(leaders))]
}

func entityNoun(entity store.Entity, n int) string {
	if n == 1 {
		return map[store.Entity]string{store.EntityServers: "server", store.EntityChannels: "channel", store.EntityPeople: "conversation", store.EntityGames: "game", store.EntityPlatforms: "platform", store.EntityEmoji: "emoji"}[entity]
	}
	return strings.ToLower(entityLabels[entity])
}

func leaderName(l store.Leader) string {
	switch l.Kind {
	case "dm", "unknown-dm", "group":
		return displayChannel(store.Row{Name: l.Name, Kind: l.Kind})
	case "emoji":
		return ":" + session.Safe(l.Name) + ":"
	}
	return session.Safe(l.Name)
}

// drillHint says what opening a ranked item will show.
func drillHint(entity store.Entity, metric store.Metric) string {
	switch entity {
	case store.EntityServers:
		return "Open a server for its channels by " + strings.ToLower(metric.Label()) + "."
	case store.EntityChannels, store.EntityPeople:
		if messageMetric(metric) {
			return "Open a row to read its messages."
		}
		return "Open a row for its activity by " + strings.ToLower(metric.Label()) + "."
	}
	return "Open a row for its activity over time."
}

func (m *Model) entity(w, h int) string {
	entity := store.Entity(m.StatsEntity)
	metric := m.leaderMetric()
	leaders := m.topLeaders(entity, metric, 1<<20)
	m.StatsLeaders = leaders
	m.StatsTotal = len(leaders)
	m.StatsPage = max(1, h-1)
	m.StatsOffset = min(m.StatsOffset, max(0, m.StatsTotal-1))
	m.StatsCursor = min(m.StatsCursor, max(0, m.StatsTotal-1))
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	if len(leaders) == 0 {
		m.StatsScrollHeight = 0
		note := m.importNote()
		if note == "" {
			note = muted.Render("No " + strings.ToLower(metric.Label()) + " for " + strings.ToLower(entityLabels[entity]) + " in this range.")
		}
		return note
	}
	m.Actions = append(m.Actions, "stats-list")
	total := 0
	for _, l := range leaders {
		total += l.Values[metric]
	}
	peak := leaders[0].Values[metric]
	end := min(m.StatsTotal, m.StatsOffset+m.StatsPage)
	cw := w
	if m.StatsTotal > m.StatsPage {
		cw = w - 2
	}
	valueWidth := 0
	for _, l := range leaders[m.StatsOffset:end] {
		valueWidth = max(valueWidth, lipgloss.Width(metricValue(metric, l.Values[metric])))
	}
	barWidth := max(6, min(16, cw/6))
	rankWidth := len(strconv.Itoa(m.StatsTotal)) + 2
	active := -1
	if i, ok := hoverIndex(m.Hover, "stats-row-", "tip-row-"); ok {
		active = i
	}
	cursor := -1
	if m.Focus == "stats-list" {
		cursor = m.StatsCursor
	}
	var lines []string
	lines = append(lines, muted.Render(fmt.Sprintf("%s %s, showing %d to %d. %s", number(m.StatsTotal), entityNoun(entity, m.StatsTotal), m.StatsOffset+1, end, drillHint(entity, metric))))
	for i := m.StatsOffset; i < end; i++ {
		l := leaders[i]
		rowID := fmt.Sprintf("stats-row-%d", i)
		distance := fadeDistance(active, i)
		current := i == active || i == cursor
		rank := lipgloss.NewStyle().Foreground(components.Fade(components.Muted, distance)).Render(fmt.Sprintf("%*d. ", rankWidth-2, i+1))
		amount := fmt.Sprintf("%*s", valueWidth, metricValue(metric, l.Values[metric]))
		tipText := valueTip(metric, l.Values[metric], total)
		if entity == store.EntityGames {
			tipText += fmt.Sprintf(", %s sessions", number(l.Values[store.MetricSessions]))
			if l.Lifetime > 0 {
				tipText += ", lifetime " + humanDuration(l.Lifetime)
			}
		}
		valueColor := components.Fade(components.Text, distance)
		nameStyle := lipgloss.NewStyle().Foreground(components.Fade(components.Text, distance))
		valueStyle := lipgloss.NewStyle().Foreground(valueColor)
		if current {
			valueColor = components.Pink
			valueStyle = valueStyle.Bold(true).Foreground(components.Pink)
			nameStyle = nameStyle.Bold(true)
		}
		bar := heatRule(m.StatsPalette, float64(l.Values[metric])/float64(max(1, peak)), barWidth)
		value := m.figure("row/"+string(entity)+"/"+metric.Key()+"/"+l.ID, amount, l.Values[metric], valueStyle, valueColor)
		right := bar + " " + m.tip(fmt.Sprintf("tip-row-%d", i), tipText, value)
		nameWidth := max(8, cw-rankWidth-barWidth-valueWidth-3)
		name := nameStyle.Render(components.Fit(leaderName(l), nameWidth))
		line := rank + name + strings.Repeat(" ", max(1, nameWidth-lipgloss.Width(name)+1)) + right
		rowStyle := lipgloss.NewStyle().Width(cw)
		if current {
			rowStyle = rowStyle.Background(components.Surface)
		}
		m.HoverOnly = append(m.HoverOnly, rowID)
		lines = append(lines, m.Zones.Mark(rowID, rowStyle.Render(line)))
	}
	body := m.Zones.Mark("stats-list", strings.Join(lines, "\n"))
	if m.StatsTotal <= m.StatsPage {
		m.StatsScrollHeight = 0
		return body
	}
	height := max(1, lipgloss.Height(body))
	m.StatsScrollHeight = height
	return lipgloss.JoinHorizontal(lipgloss.Top, body, " ", m.scrollbarView("stats-scroll", m.StatsOffset, m.StatsTotal, m.StatsPage, height))
}

func (m *Model) statsMessages(w, h int) string {
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	subject := leaderName(m.StatsTarget)
	switch m.StatsTargetMetric {
	case store.MetricMissing:
		subject = "Missing in " + subject
	case store.MetricMedia:
		subject = "Media in " + subject
	case store.MetricAttachments:
		subject = "Attachments in " + subject
	case store.MetricLinks:
		subject = "Links in " + subject
	}
	title := components.Title.Render(components.Fit(subject, max(10, w-24)))
	if m.StatsRowsLoading {
		m.StatsTotal, m.StatsPage, m.StatsScrollHeight = 0, 1, 0
		return title + "\n" + components.Working("Loading messages…", m.Frame, w)
	}
	rows := m.StatsRows
	m.StatsTotal = len(rows)
	m.StatsPage = max(1, (h-2)/2)
	m.StatsOffset = min(m.StatsOffset, max(0, m.StatsTotal-1))
	if len(rows) == 0 {
		m.StatsScrollHeight = 0
		return title + "\n" + muted.Render("No messages in this range.")
	}
	end := min(m.StatsTotal, m.StatsOffset+m.StatsPage)
	cw := w
	if m.StatsTotal > m.StatsPage {
		cw = w - 2
	}
	active := -1
	if i, ok := hoverIndex(m.Hover, "srow-", "sdate-"); ok {
		active = i
	}
	lines := []string{title + "  " + muted.Render(fmt.Sprintf("%s, newest first, %d to %d", metricUnit(store.MetricMessages, m.StatsTotal), m.StatsOffset+1, end))}
	for i := m.StatsOffset; i < end; i++ {
		r := rows[i]
		id := fmt.Sprintf("srow-%d", i)
		distance := fadeDistance(active, i)
		hover := i == active
		metaColor := components.Accent
		if unavailableStatus(r.Status) {
			metaColor = components.Deleted
		}
		contentColor := components.Text
		if r.SendEvent && !r.MessageRecord {
			contentColor = components.Muted
		}
		metaStyle := lipgloss.NewStyle().Foreground(components.Fade(metaColor, distance))
		contentStyle := lipgloss.NewStyle().Foreground(components.Fade(contentColor, distance))
		mutedColor := components.Fade(components.Muted, distance)
		rowStyle := lipgloss.NewStyle().Padding(0, 1).Width(cw - 2)
		if hover {
			rowStyle = rowStyle.Background(components.Surface)
			metaStyle = metaStyle.Bold(true).Foreground(components.Pink)
		}
		media := ""
		if r.HasMedia {
			media = ", media"
		} else if r.HasAttachments {
			media = ", attachment"
		}
		date := m.messageDate(fmt.Sprintf("sdate-%d", i), r.Date, mutedColor) + lipgloss.NewStyle().Foreground(mutedColor).Render(media)
		metaWidth := cw - 4
		nameWidth := max(8, metaWidth-lipgloss.Width(date)-2)
		name := metaStyle.Render(components.Fit(r.Server+" / "+displayChannel(r), nameWidth))
		meta := name + strings.Repeat(" ", max(1, metaWidth-lipgloss.Width(name)-lipgloss.Width(date))) + date
		preview := messagePreview(r)
		content := contentStyle.Render(components.Fit(preview, cw-4))
		m.HoverOnly = append(m.HoverOnly, id)
		lines = append(lines, m.Zones.Mark(id, rowStyle.Render(meta+"\n"+content)))
	}
	body := strings.Join(lines, "\n")
	if m.StatsTotal <= m.StatsPage {
		m.StatsScrollHeight = 0
		return body
	}
	height := max(1, lipgloss.Height(body))
	m.StatsScrollHeight = height
	return lipgloss.JoinHorizontal(lipgloss.Top, body, " ", m.scrollbarView("stats-scroll", m.StatsOffset, m.StatsTotal, m.StatsPage, height))
}

func heatRule(palette string, fraction float64, width int) string {
	filled := int(math.Round(fraction * float64(width)))
	var b strings.Builder
	for i := range width {
		position := float64(i) / float64(max(1, width-1))
		if i < filled {
			b.WriteString(lipgloss.NewStyle().Foreground(components.PaletteColor(palette, .25+.75*position)).Render("━"))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(components.Border).Render("─"))
		}
	}
	return b.String()
}

type heatGrid struct {
	rows, cols int
	labelWidth int
	rowLabels  []string
	colLabels  map[int]string
	value      func(r, c int) int
	valid      func(r, c int) bool
	describe   func(r, c int) string
	note       string
}

func (m *Model) heatGrid(w, cellWidth int) heatGrid {
	st := m.Stats
	metric := m.StatsMetric
	f := m.statsFilter()
	from, until := f.From, f.Until
	if from.IsZero() {
		from = st.First
		until = st.Last.AddDate(0, 0, 1)
	}
	if from.IsZero() {
		from = m.Session.Today
		until = from.AddDate(0, 0, 1)
	}
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	until = time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, time.Local)
	switch m.StatsLayout {
	case "weeks":
		start := from.AddDate(0, 0, -((int(from.Weekday()) + 6) % 7))
		weeks := int(until.Sub(start).Hours()/24/7) + 1
		if !until.After(start.AddDate(0, 0, (weeks-1)*7)) {
			weeks--
		}
		weeks = max(1, weeks)
		maxCols := max(1, (w-4)/cellWidth)
		note := ""
		if weeks > maxCols {
			start = start.AddDate(0, 0, (weeks-maxCols)*7)
			note = fmt.Sprintf("Showing the last %d of %d weeks", maxCols, weeks)
			weeks = maxCols
		}
		labels := map[int]string{}
		for c := range weeks {
			monday := start.AddDate(0, 0, c*7)
			if c == 0 || monday.Month() != monday.AddDate(0, 0, -7).Month() {
				labels[c] = monday.Format("Jan")
			}
		}
		date := func(r, c int) time.Time { return start.AddDate(0, 0, c*7+r) }
		return heatGrid{rows: 7, cols: weeks, labelWidth: 4, rowLabels: weekdays, colLabels: labels, note: note,
			value:    func(r, c int) int { return st.Daily[metric][date(r, c).Format(time.DateOnly)] },
			valid:    func(r, c int) bool { d := date(r, c); return !d.Before(from) && d.Before(until) },
			describe: func(r, c int) string { return date(r, c).Format("Monday 2006-01-02") }}
	case "months":
		last := until.AddDate(0, 0, -1)
		years := last.Year() - from.Year() + 1
		labels := map[int]string{}
		for c := range 12 {
			labels[c] = time.Month(c + 1).String()[:3]
		}
		rowLabels := make([]string, years)
		for r := range years {
			rowLabels[r] = fmt.Sprint(from.Year() + r)
		}
		month := func(r, c int) time.Time { return time.Date(from.Year()+r, time.Month(c+1), 1, 0, 0, 0, 0, time.Local) }
		return heatGrid{rows: years, cols: 12, labelWidth: 5, rowLabels: rowLabels, colLabels: labels,
			value:    func(r, c int) int { return st.Monthly[metric][month(r, c).Format("2006-01")] },
			valid:    func(r, c int) bool { mo := month(r, c); return mo.AddDate(0, 1, 0).After(from) && mo.Before(until) },
			describe: func(r, c int) string { return month(r, c).Format("January 2006") }}
	}
	labels := map[int]string{}
	for c := 0; c < 24; c += 3 {
		labels[c] = fmt.Sprint(c)
	}
	return heatGrid{rows: 7, cols: 24, labelWidth: 4, rowLabels: weekdays, colLabels: labels,
		value:    func(r, c int) int { return st.Weekly[metric][r][c] },
		valid:    func(int, int) bool { return true },
		describe: func(r, c int) string { return fmt.Sprintf("%s %02d:00 to %02d:00", weekdayNames[r], c, (c+1)%24) }}
}

func (m *Model) activity(w, h int) string {
	metric := m.StatsMetric
	cellWidth := 2
	if w >= 84 {
		cellWidth = 3
	}
	if m.StatsLayout == "months" {
		cellWidth = 4
	}
	g := m.heatGrid(w, cellWidth)
	if m.StatsLayout == "months" {
		maxRows := max(1, h-5)
		if g.rows > maxRows {
			skip := g.rows - maxRows
			g.rowLabels = g.rowLabels[skip:]
			inner := g
			g.value = func(r, c int) int { return inner.value(r+skip, c) }
			g.valid = func(r, c int) bool { return inner.valid(r+skip, c) }
			g.describe = func(r, c int) string { return inner.describe(r+skip, c) }
			g.rows = maxRows
			g.note = fmt.Sprintf("Showing the last %d of %d years", maxRows, inner.rows)
		}
	}
	m.HeatCursor[0] = max(0, min(g.rows-1, m.HeatCursor[0]))
	m.HeatCursor[1] = max(0, min(g.cols-1, m.HeatCursor[1]))
	peak, total := 0, 0
	for r := range g.rows {
		for c := range g.cols {
			if g.valid(r, c) {
				v := g.value(r, c)
				peak = max(peak, v)
				total += v
			}
		}
	}
	m.Actions = append(m.Actions, "heat")
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	header := strings.Repeat(" ", g.labelWidth)
	for c := 0; c < g.cols; c++ {
		label := g.colLabels[c]
		if lipgloss.Width(header) > g.labelWidth+c*cellWidth {
			label = ""
		}
		if label != "" {
			header += strings.Repeat(" ", max(0, g.labelWidth+c*cellWidth-lipgloss.Width(header))) + label
		}
	}
	lines := []string{muted.Render(components.FitStyled(header, w))}
	active := ""
	activeRow, activeCol := -1, -1
	if m.Focus == "heat" {
		activeRow, activeCol = m.HeatCursor[0], m.HeatCursor[1]
	}
	if r, c, ok := heatCell(m.Hover); ok && r < g.rows && c < g.cols {
		activeRow, activeCol = r, c
	}
	for r := range g.rows {
		var row strings.Builder
		row.WriteString(muted.Render(fmt.Sprintf("%-*s", g.labelWidth, g.rowLabels[r])))
		for c := range g.cols {
			id := fmt.Sprintf("heat-%d-%d", r, c)
			m.HoverOnly = append(m.HoverOnly, id)
			highlighted := r == activeRow && c == activeCol
			cell := m.heatCell(g, r, c, peak, cellWidth, highlighted)
			if highlighted {
				active = g.describe(r, c) + ", " + metricUnit(metric, g.value(r, c))
				if metric.Duration() {
					active = g.describe(r, c) + ", " + humanDuration(g.value(r, c))
				}
				if total > 0 && g.value(r, c) > 0 {
					active += fmt.Sprintf(" (%.0f%%)", 100*float64(g.value(r, c))/float64(total))
				}
			}
			row.WriteString(m.Zones.Mark(id, cell))
		}
		lines = append(lines, row.String())
	}
	var legend strings.Builder
	legend.WriteString(muted.Render("Less "))
	for i := range 6 {
		legend.WriteString(lipgloss.NewStyle().Foreground(components.PaletteColor(m.StatsPalette, .15+.85*float64(i)/5)).Render("█"))
	}
	legend.WriteString(muted.Render(" More"))
	if g.note != "" {
		legend.WriteString(muted.Render("   " + g.note))
	}
	totalText := metricUnit(metric, total)
	if metric.Duration() {
		totalText = humanDuration(total)
	}
	legend.WriteString(muted.Render("   Total ") + m.figure("heat/"+metric.Key(), totalText, total, lipgloss.NewStyle().Foreground(components.Text), components.Text))
	lines = append(lines, "", legend.String())
	if active == "" {
		if peak == 0 {
			active = "No " + strings.ToLower(metric.Label()) + " in this range."
		} else {
			day, hour, count := peakCell(m.Stats.Weekly[metric])
			active = fmt.Sprintf("Peak %s %02d:00 with %s. Hover or focus a cell for details.", weekdayNames[day], hour, metricUnit(metric, count))
		}
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(components.Text).Render(components.Fit(active, w)))
	if note := m.importNote(); note != "" {
		lines = append(lines, note)
	}
	body := strings.Join(lines, "\n")
	return m.Zones.Mark("heat", body)
}

func heatCell(id string) (int, int, bool) {
	var r, c int
	if _, err := fmt.Sscanf(id, "heat-%d-%d", &r, &c); err != nil || !strings.HasPrefix(id, "heat-") {
		return 0, 0, false
	}
	return r, c, true
}

func (m *Model) heatCell(g heatGrid, r, c, peak, cellWidth int, highlighted bool) string {
	if !g.valid(r, c) {
		return strings.Repeat(" ", cellWidth)
	}
	v := g.value(r, c)
	position := 0.0
	if v > 0 && peak > 0 {
		position = float64(v) / float64(peak)
		if m.StatsScale == "log" {
			position = math.Log1p(float64(v)) / math.Log1p(float64(peak))
		}
	}
	var glyph string
	switch m.StatsCells {
	case "dots":
		glyph = center("●", cellWidth)
	case "shades":
		glyph = strings.Repeat(string([]rune("░▒▓█")[min(3, int(position*3.999))]), cellWidth)
	case "digits":
		glyph = center(fmt.Sprint(max(1, int(math.Round(position*9)))), cellWidth)
	default:
		glyph = strings.Repeat("█", cellWidth)
	}
	style := lipgloss.NewStyle()
	if v == 0 {
		glyph = center("·", cellWidth)
		style = style.Foreground(components.Border)
	} else {
		style = style.Foreground(components.PaletteColor(m.StatsPalette, .15+.85*position))
	}
	if highlighted {
		style = style.Foreground(components.Pink).Background(components.SurfaceHover).Bold(true)
		if m.StatsCells == "blocks" || m.StatsCells == "shades" || v == 0 {
			glyph = center("◆", cellWidth)
		}
	}
	return style.Render(glyph)
}

func center(text string, width int) string {
	pad := max(0, width-lipgloss.Width(text))
	return strings.Repeat(" ", pad/2) + text + strings.Repeat(" ", pad-pad/2)
}
