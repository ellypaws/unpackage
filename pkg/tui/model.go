package tui

import (
	"cmp"
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/muesli/termenv"

	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

type tickMsg time.Time
type searchMsg struct {
	Server   bool
	Revision int
	Value    string
}
type incidentInputMsg struct {
	Seconds    map[int64]int64
	Recognized bool
	Clipboard  bool
	Err        error
	Revision   int
}

const (
	tabInvestigate = iota
	tabStats
	tabConsole
	tabLog
	tabCount
)

type dataMsg struct {
	Rows                  []store.Row
	Groups, Servers, Days []store.Group
	Snapshots             []store.Snapshot
	Warning               string
	Err                   error
	Revision              int
}
type resultMsg struct {
	Text string
	Err  error
}
type dropCheckMsg struct {
	Value    string
	Revision int
	Slot     int
}
type dropPathMsg struct {
	Path     string
	Revision int
	Slot     int
}
type Model struct {
	Session                                       *session.Session
	ctx                                           context.Context
	Zones                                         *zone.Manager
	Width, Height, Tab, Cursor, Offset, Revision  int
	Frame                                         int
	Input                                         textinput.Model
	ServerInput                                   textinput.Model
	ServerSearch                                  string
	SearchRevision, ServerRevision                int
	FilterDialog                                  bool
	DayInput, SearchInput, RequestInput           textinput.Model
	BeforeInput, AfterInput                       textinput.Model
	MarginDialog                                  bool
	PackagesExpanded, ServerDialog, RequestDialog bool
	DropSlot, PendingDropSlot                     int
	Viewport                                      viewport.Model
	Picker                                        *components.Picker
	Calendar                                      *components.Calendar
	PickSlot                                      int
	BrowseDirs                                    [2]string
	BrowseChosen                                  [2]bool
	SharedBrowseDir                               string
	Hover, Focus, Notice                          string
	Actions                                       []string
	HoverOnly                                     []string
	Rows                                          []store.Row
	Groups, Servers, Days                         []store.Group
	Snapshots                                     []store.Snapshot
	Warning                                       string
	Detail                                        *store.Row
	Transcript                                    []string
	History                                       []string
	HistoryIndex                                  int
	Loading, Executing                            bool
	RequestScope                                  string
	FollowLog                                     bool
	ConsoleFollow                                 bool
	ServerOffset                                  int
	ScrollHeight, ScrollTotal, ScrollVisible      int
	DropBuffer                                    string
	DropInput, DropFocus                          string
	DropTime                                      time.Time
	DropRevision, IncidentInputRevision           int
	IncidentProcessing                            string
	LastCommand                                   string
	ServerSort, ServerTarget                      string
	SnapshotKey                                   string
	Stats                                         *store.Stats
	StatsView, StatsRange, StatsLayout            string
	StatsPalette, StatsCells, StatsScale          string
	StatsEntity                                   string
	StatsMetric                                   store.Metric
	StatsGuilds, StatsExcludedGuilds              []string
	StatsRevision, StatsShown                     int
	StatsLoading                                  bool
	StatsAt                                       time.Time
	StatsOffset, StatsPage, StatsTotal            int
	StatsScrollHeight                             int
	StatsCursor                                   int
	StatsRowCount                                 int
	StatsScope                                    scope
	StatsLeaders                                  []store.Leader
	StatsTarget                                   store.Leader
	StatsTargetMetric                             store.Metric
	Menu                                          *menu
	Figures                                       map[string]figureState
	StatsRows                                     []store.Row
	StatsRowsRevision                             int
	StatsRowsLoading                              bool
	ChannelLabel                                  string
	HeatCursor                                    [2]int
	Tips                                          map[string]string
}

func New(ctx context.Context, s *session.Session) *Model {
	if os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
	input := textinput.New()
	input.Prompt = "› "
	input.Placeholder = "Command…"
	input.CharLimit = 4096
	days := textinput.New()
	days.Prompt = ""
	days.Placeholder = "Days ago, YYYY-MM-DD, or unix time"
	days.CharLimit = 1024
	days.PlaceholderStyle = lipgloss.NewStyle().Foreground(components.Muted)
	search := textinput.New()
	search.Prompt = ""
	search.Placeholder = "Search messages"
	search.CharLimit = 1024
	search.PlaceholderStyle = days.PlaceholderStyle
	serverInput := textinput.New()
	serverInput.Prompt = ""
	serverInput.Placeholder = "Search servers"
	serverInput.CharLimit = 256
	serverInput.PlaceholderStyle = days.PlaceholderStyle
	request := textinput.New()
	request.Prompt = ""
	request.CharLimit = 4096
	input.PlaceholderStyle = days.PlaceholderStyle
	before := textinput.New()
	before.Prompt = ""
	before.Placeholder = "0"
	before.PlaceholderStyle = days.PlaceholderStyle
	before.CharLimit = 4
	after := textinput.New()
	after.Prompt = ""
	after.Placeholder = "0"
	after.PlaceholderStyle = days.PlaceholderStyle
	after.CharLimit = 4
	return &Model{Session: s, ctx: ctx, Zones: zone.New(), Width: 90, Height: 28, Input: input, ServerInput: serverInput, DayInput: days, SearchInput: search, RequestInput: request, BeforeInput: before, AfterInput: after, PackagesExpanded: true, Viewport: viewport.New(80, 15), Focus: "open-old", RequestScope: "all", FollowLog: true, ConsoleFollow: true, ServerSort: "messages", ServerTarget: "filter", StatsView: "overview", StatsRange: "all", StatsLayout: "hours", StatsPalette: "violet", StatsCells: "blocks", StatsScale: "linear", StatsEntity: string(store.EntityServers), StatsRowCount: 8, Figures: map[string]figureState{}}
}

func (m *Model) targetGuilds() (included, excluded *[]string) {
	if m.ServerTarget == "stats" {
		return &m.StatsGuilds, &m.StatsExcludedGuilds
	}
	return &m.Session.Filter.Guilds, &m.Session.Filter.ExcludedGuilds
}

func (m *Model) sortServers() {
	slices.SortStableFunc(m.Servers, func(a, b store.Group) int {
		switch m.ServerSort {
		case "name":
			return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
		case "missing":
			return cmp.Or(cmp.Compare(b.Missing, a.Missing), cmp.Compare(b.Count, a.Count), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
		}
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
}
func (m *Model) Init() tea.Cmd { return tea.Batch(textinput.Blink, tick(false), m.refresh()) }
func tick(active bool) tea.Cmd {
	interval := 250 * time.Millisecond
	if active {
		interval = 120 * time.Millisecond
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m *Model) refresh() tea.Cmd {
	if m.Loading || m.Executing {
		return nil
	}
	m.Loading = true
	f := m.Session.Filter
	f.Guilds = slices.Clone(f.Guilds)
	f.ExcludedGuilds = slices.Clone(f.ExcludedGuilds)
	f.Dates = slices.Clone(f.Dates)
	f.IncidentSeconds = maps.Clone(f.IncidentSeconds)
	f.Limit = m.pageSize()
	f.Offset = m.Offset
	revision := m.Revision
	s := m.Session.Store
	ctx := m.ctx
	return func() tea.Msg {
		d := dataMsg{Revision: revision}
		d.Snapshots, d.Err = s.Snapshots(ctx)
		if d.Err != nil {
			return d
		}
		var comparable bool
		comparable, d.Warning = s.Compatible(ctx)
		full := f
		full.Limit = 0
		full.Offset = 0
		matchingFilter := store.Filter{Mode: full.Mode, Dates: slices.Clone(full.Dates), IncidentSeconds: maps.Clone(full.IncidentSeconds), From: full.From, Until: full.Until, DateBefore: full.DateBefore, DateAfter: full.DateAfter}
		missingFilter := matchingFilter
		missingFilter.Mode = "missing"
		var filtered, matching, missing []store.Row
		var errs [4]error
		var group sync.WaitGroup
		group.Go(func() { filtered, errs[0] = s.Rows(ctx, full) })
		group.Go(func() { matching, errs[1] = s.Rows(ctx, matchingFilter) })
		group.Go(func() { d.Servers, errs[2] = s.Servers(ctx) })
		if comparable {
			group.Go(func() { missing, errs[3] = s.Rows(ctx, missingFilter) })
		}
		group.Wait()
		for _, err := range errs {
			if err != nil {
				d.Err = err
				return d
			}
		}
		start := min(max(0, f.Offset), len(filtered))
		end := len(filtered)
		if f.Limit > 0 {
			end = min(end, start+f.Limit)
		}
		d.Rows = filtered[start:end]
		d.Groups = store.GroupRows(filtered, false)
		d.Days = store.GroupRows(filtered, true)
		matchingGroups := store.GroupRows(matching, false)
		counts := make(map[string]int, len(matchingGroups))
		for _, g := range matchingGroups {
			counts[g.ID] = g.Count
		}
		missingCounts := map[string]int{}
		for _, g := range store.GroupRows(missing, false) {
			missingCounts[g.ID] = g.Count
		}
		for i := range d.Servers {
			d.Servers[i].Count = counts[d.Servers[i].ID]
			d.Servers[i].Missing = missingCounts[d.Servers[i].ID]
		}
		if len(full.Dates) > 0 || len(full.IncidentSeconds) > 0 || full.From != "" || full.Until != "" {
			d.Servers = slices.DeleteFunc(d.Servers, func(group store.Group) bool { return group.Count == 0 })
		}
		return d
	}
}
func (m *Model) changed() tea.Cmd { m.Revision++; m.Cursor = 0; m.Offset = 0; return m.refresh() }

func mergeServerOrder(current, next []store.Group) []store.Group {
	byID := make(map[string]store.Group, len(next))
	for _, group := range next {
		byID[group.ID] = group
	}
	merged := make([]store.Group, 0, len(next))
	for _, group := range current {
		if updated, ok := byID[group.ID]; ok {
			merged = append(merged, updated)
			delete(byID, group.ID)
		}
	}
	for _, group := range next {
		if updated, ok := byID[group.ID]; ok {
			merged = append(merged, updated)
			delete(byID, group.ID)
		}
	}
	return merged
}

func (m *Model) debounce(server bool) tea.Cmd {
	value := m.SearchInput.Value()
	var revision int
	if server {
		value = m.ServerInput.Value()
		m.ServerRevision++
		revision = m.ServerRevision
	} else {
		m.SearchRevision++
		revision = m.SearchRevision
	}
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return searchMsg{server, revision, value} })
}
func (m *Model) run(line string) tea.Cmd {
	if m.Executing {
		return nil
	}
	a, e := session.Split(line)
	if e != nil {
		m.Notice = e.Error()
		return nil
	}
	if len(a) == 0 {
		return nil
	}
	if a[0] == "quit" || a[0] == "exit" {
		return tea.Quit
	}
	m.LastCommand = a[0]
	if slices.Contains([]string{"help", "list", "show", "stats", "days", "servers", "status", "summary", "leaders", "heatmap"}, a[0]) {
		m.Tab = tabConsole
		m.Focus = "command"
		m.Input.Focus()
	}
	m.History = append(m.History, line)
	m.HistoryIndex = len(m.History)
	m.Input.SetValue("")
	m.Transcript = append(m.Transcript, "› "+session.Safe(line))
	m.Revision++
	m.Executing = true
	s := m.Session
	ctx := m.ctx
	// Output is bounded in the embedded console; CLI exports remain streaming.
	if !slices.Contains([]string{"list", "show", "servers", "stats", "days", "request", "wait", "status", "sample", "open", "summary", "leaders", "heatmap"}, a[0]) {
		w := &consoleWriter{}
		e := s.Execute(ctx, a, w)
		return func() tea.Msg { return resultMsg{w.b.String(), e} }
	}
	return func() tea.Msg { w := &consoleWriter{}; e := s.Execute(ctx, a, w); return resultMsg{w.b.String(), e} }
}

type consoleWriter struct {
	b         strings.Builder
	truncated bool
}

func (w *consoleWriter) Write(p []byte) (int, error) {
	n := len(p)
	remain := 65536 - w.b.Len()
	if remain >= n {
		w.b.Write(p)
	}
	if n > remain && !w.truncated {
		w.b.WriteString("\nOutput truncated. Use CLI list for a full export.\n")
		w.truncated = true
	}
	return n, nil
}
