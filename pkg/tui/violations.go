package tui

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ellypaws/unpackage/pkg/clipboard"
	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/safety"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

type safetyRowsMsg struct {
	Matches  map[string][]store.Row
	Err      error
	Revision int
}

var (
	safetyStates = []option{{"", "All states"}, {safety.StateActive, "Active"}, {safety.StatePending, "Appeal pending"}, {safety.StateUpheld, "Appeal denied"}, {safety.StateInvalidated, "Appeal accepted"}, {safety.StateExpired, "Expired"}, {safety.StateNotice, "Notice only"}}
	safetyScopes = []option{{"", "Account and server"}, {"account", "Account"}, {"server", "Server"}}
	safetyFound  = []option{{"", "Any messages"}, {"found", "Found in packages"}, {"absent", "Not found in packages"}, {"flagged", "Has flagged content"}}
)

type browserGuide struct {
	key, label string
	steps      []string
}

var browserGuides = []browserGuide{
	{"chrome", "Chrome", []string{
		"Press F12 or Ctrl+Shift+I. On macOS, press Cmd+Option+I. The menu path is More tools, then Developer tools.",
		"Open the Network tab and select the Fetch/XHR filter.",
		"Type @me or messages in the Filter box.",
		"Right-click the request, then choose Copy, then Copy response.",
	}},
	{"edge", "Edge", []string{
		"Press F12 or Ctrl+Shift+I. On macOS, press Cmd+Option+I. The menu path is More tools, then Developer tools.",
		"Open the Network tab. If it is hidden, find it under the More tabs arrow. Select the Fetch/XHR filter.",
		"Type @me or messages in the Filter box.",
		"Right-click the request, then choose Copy, then Copy response.",
	}},
	{"firefox", "Firefox", []string{
		"Press Ctrl+Shift+E to open the Network panel directly. On macOS, press Cmd+Option+E.",
		"Select XHR in the request type bar.",
		"Type @me or messages in the Filter URLs box.",
		"Right-click the request, then choose Copy Value, then Copy Response.",
	}},
	{"safari", "Safari", []string{
		"Turn on Show features for web developers in Safari Settings, Advanced. Older versions call it Show Develop menu in menu bar.",
		"Press Cmd+Option+I and open the Network tab. Select XHR/Fetch in the type filter.",
		"Type @me or messages in the Filter box.",
		"Select the request and open its response. Click inside it, press Cmd+A, then Cmd+C.",
	}},
	{"chromium", "Brave, Opera, Vivaldi", []string{
		"These browsers use Chrome's developer tools. Press F12 or Ctrl+Shift+I. On macOS, press Cmd+Option+I.",
		"Open the Network tab and select the Fetch/XHR filter.",
		"Type @me or messages in the Filter box.",
		"Right-click the request, then choose Copy, then Copy response.",
	}},
}

var guideSteps = []string{
	"Open Discord in a web browser at discord.com/app and sign in. The desktop app hides developer tools.",
	"Open developer tools and select the Network tab.",
	"Choose the Fetch/XHR filter so only data requests remain.",
	"Type @me in the filter box for account standing, or messages for notices.",
	"For account standing, open User Settings, then Account Standing. For notices, open your direct messages with the official Discord account.",
	"Select the request that appears, open its response, and copy all of it.",
	"Choose Paste from clipboard here. Each paste adds to what is already loaded.",
}

var guideNotes = []string{
	"Requests appear only while developer tools are open. If the list is empty, reopen Account Standing or the Discord conversation.",
	"Copy the response, never the request headers. Headers contain your login token.",
	"The messages request holds the latest 50 messages. Scroll up in the conversation to load older notices, then paste each new response.",
}

// pasteSafety reads the clipboard off the UI goroutine.
func (m *Model) pasteSafety() tea.Cmd {
	m.IncidentInputRevision++
	revision := m.IncidentInputRevision
	m.IncidentProcessing = "Reading clipboard…"
	m.Notice = ""
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		text, err := clipboard.Read(ctx)
		if err != nil {
			return incidentInputMsg{Clipboard: true, Err: fmt.Errorf("read clipboard: %w. Type unix times in the date box instead", err), Revision: revision}
		}
		report, recognized, err := safety.Parse([]byte(text))
		return incidentInputMsg{Report: report, Recognized: recognized, Clipboard: true, Err: err, Revision: revision}
	}
}

// addSafety merges a paste into the session. In Investigate it also narrows results to the pasted violations.
func (m *Model) addSafety(report safety.Report) tea.Cmd {
	previous := m.Session.Safety
	result := m.Session.Safety.Merge(report)
	now := time.Now()
	if m.SafetyArrivals == nil {
		m.SafetyArrivals = map[string]time.Time{}
	}
	maps.DeleteFunc(m.SafetyArrivals, func(_ string, at time.Time) bool { return now.Sub(at) >= arrivalGlow })
	for _, v := range m.Session.Safety.Violations {
		if !slices.ContainsFunc(previous.Violations, func(old safety.Violation) bool { return old.ID == v.ID }) {
			m.SafetyArrivals[v.ID] = now
		}
	}
	if report.HubLoaded && (!previous.HubLoaded || previous.Standing != report.Standing) {
		m.SafetyStandingAt = now
	}
	m.safetyChanged()
	m.Notice = session.MergeSummary(result, m.Session.Safety)
	cmds := []tea.Cmd{m.refreshSafety()}
	if m.Tab == tabInvestigate {
		m.DayInput.SetValue("")
		m.Session.FilterViolations(report)
		cmds = append(cmds, m.changed())
	} else {
		m.SafetyGuide = false
		if len(report.Violations) > 0 {
			m.SafetySelected = report.Violations[0].ID
			m.SafetyOffset = 0
			m.Viewport.GotoTop()
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) safetyChanged() {
	m.SafetyRevision++
	m.SafetyIndex = m.Session.Safety.Index()
	if !slices.ContainsFunc(m.Session.Safety.Violations, func(v safety.Violation) bool { return v.ID == m.SafetySelected }) {
		m.SafetySelected = ""
		if len(m.Session.Safety.Violations) > 0 {
			m.SafetySelected = m.Session.Safety.Violations[0].ID
		}
	}
}

// refreshSafety finds the loaded messages that each violation identifies.
func (m *Model) refreshSafety() tea.Cmd {
	report := m.Session.Safety
	seconds, ids := report.Seconds(), report.MessageIDs()
	if len(seconds) == 0 && len(ids) == 0 {
		m.SafetyMatches = nil
		return nil
	}
	if m.SafetyRowsLoading {
		m.SafetyDirty = true
		return nil
	}
	m.SafetyRowsLoading = true
	m.SafetyRowsRevision++
	revision := m.SafetyRowsRevision
	index := m.SafetyIndex
	violationIDs := make([]string, len(report.Violations))
	for i, v := range report.Violations {
		violationIDs[i] = v.ID
	}
	s := m.Session.Store
	ctx := m.ctx
	return func() tea.Msg {
		rows, err := s.Rows(ctx, store.Filter{Mode: "auto", IncidentSeconds: seconds, IncidentIDs: ids})
		matches := map[string][]store.Row{}
		for _, r := range rows {
			if i, ok := violationIndex(index, r); ok && i < len(violationIDs) {
				matches[violationIDs[i]] = append(matches[violationIDs[i]], r)
			}
		}
		return safetyRowsMsg{matches, err, revision}
	}
}

func violationIndex(index safety.Index, r store.Row) (int, bool) {
	if i, ok := index.ByID[r.ID]; ok {
		return i, true
	}
	if len(index.BySecond) == 0 {
		return 0, false
	}
	for _, value := range []string{r.Date, r.SendTime} {
		if at, err := time.Parse(time.RFC3339Nano, value); err == nil {
			if i, ok := index.BySecond[at.Unix()]; ok {
				return i, true
			}
		}
	}
	return 0, false
}

// rowViolation reports the violation, if any, that identifies a message row.
func (m *Model) rowViolation(r store.Row) (safety.Violation, bool) {
	i, ok := violationIndex(m.SafetyIndex, r)
	if !ok || i >= len(m.Session.Safety.Violations) {
		return safety.Violation{}, false
	}
	return m.Session.Safety.Violations[i], true
}

func (m *Model) selectedViolation() (safety.Violation, bool) {
	for _, v := range m.Session.Safety.Violations {
		if v.ID == m.SafetySelected {
			return v, true
		}
	}
	return safety.Violation{}, false
}

func (m *Model) filteredViolations() []safety.Violation {
	var out []safety.Violation
	for _, v := range m.Session.Safety.Violations {
		if m.SafetyState != "" && v.State(m.Session.Today) != m.SafetyState {
			continue
		}
		if m.SafetyScope != "" && v.Scope() != m.SafetyScope {
			continue
		}
		switch m.SafetyFound {
		case "found":
			if len(m.SafetyMatches[v.ID]) == 0 {
				continue
			}
		case "absent":
			if len(m.SafetyMatches[v.ID]) > 0 {
				continue
			}
		case "flagged":
			if v.Classification == nil || len(v.Classification.Flagged) == 0 {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

func stateColor(state string) lipgloss.Color {
	switch state {
	case safety.StatePending:
		return components.GroupDM
	case safety.StateUpheld:
		return components.Deleted
	case safety.StateInvalidated:
		return components.Green
	case safety.StateExpired:
		return components.Muted
	case safety.StateNotice:
		return components.Cyan
	}
	return components.Violation
}

func standingColor(state int) lipgloss.Color {
	switch {
	case state <= 100:
		return components.Green
	case state <= 200:
		return components.GroupDM
	case state <= 300:
		return components.Violation
	}
	return components.Deleted
}

func violationActions(v safety.Violation) []string {
	if v.Classification == nil {
		return nil
	}
	var out []string
	for _, a := range v.Classification.Actions {
		out = append(out, safety.ActionLabel(a.Type))
	}
	return slices.Compact(out)
}

// sentenceList joins labels into one phrase, lowercasing every label after the first.
func sentenceList(labels []string) string {
	out := slices.Clone(labels)
	for i := 1; i < len(out); i++ {
		if len(out[i]) > 1 && out[i][1] >= 'a' && out[i][1] <= 'z' {
			out[i] = strings.ToLower(out[i][:1]) + out[i][1:]
		}
	}
	return strings.Join(out, ", ")
}

func incidentText(v safety.Violation) string {
	at, exact := v.Incident()
	switch {
	case at.IsZero():
		return "Time unknown"
	case exact:
		return "Incident " + at.In(time.Local).Format("2006-01-02 15:04:05")
	}
	return "Classified " + at.In(time.Local).Format(time.DateOnly)
}

// violationTip is the one-line summary shown wherever a message is linked to a violation.
func violationTip(v safety.Violation, now time.Time) string {
	parts := []string{v.Title()}
	if actions := violationActions(v); len(actions) > 0 {
		parts = append(parts, sentenceList(actions))
	}
	parts = append(parts, safety.StateLabels[v.State(now)], incidentText(v))
	return strings.Join(parts, ". ")
}

// violationBadge marks a message row that a violation identifies; activating it opens that violation.
func (m *Model) violationBadge(id string, r store.Row, distance int) string {
	v, ok := m.rowViolation(r)
	if !ok {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(components.Fade(stateColor(v.State(m.Session.Today)), distance)).Bold(true)
	if m.Hover == id || m.Focus == id {
		style = style.Foreground(lipgloss.Color("#FFFFFF")).Background(components.SurfaceHover).Underline(true)
	}
	label := map[string]string{safety.StateInvalidated: "violation removed", safety.StateExpired: "violation expired"}[v.State(m.Session.Today)]
	m.Tips[id] = violationTip(v, m.Session.Today)
	m.HoverOnly = append(m.HoverOnly, id)
	m.Actions = append(m.Actions, id)
	return m.Zones.Mark(id, style.Render(cmp.Or(label, "violation")))
}

// violationMarks orders the dated events of a violation for its timeline.
func (m *Model) violationMarks(v safety.Violation) []milestone {
	now := m.Session.Today
	color := stateColor(v.State(now))
	short := func(t time.Time) string { return t.In(time.Local).Format(time.DateOnly) }
	var marks []milestone
	add := func(label string, at time.Time, optional bool) {
		if !at.IsZero() {
			marks = append(marks, milestone{label: label, date: short(at), at: at, done: !at.After(now), optional: optional, color: color})
		}
	}
	if at, exact := v.Incident(); exact {
		add("Incident", at, false)
	}
	add("Classified", safety.SnowflakeTime(v.ID), true)
	for _, n := range v.Notices {
		if !n.System {
			add("Notice", safety.SnowflakeTime(n.MessageID), true)
			break
		}
	}
	c := v.Classification
	if c != nil && (c.AppealStatus == safety.AppealUpheld || c.AppealStatus == safety.AppealInvalidated) {
		for _, n := range slices.Backward(v.Notices) {
			if n.System {
				add(safety.AppealLabel(c.AppealStatus), n.Time, false)
				break
			}
		}
	}
	if c != nil && !c.Expires.IsZero() {
		add(map[bool]string{true: "Expired", false: "Expires"}[!c.Expires.After(now)], c.Expires, false)
	}
	slices.SortStableFunc(marks, func(a, b milestone) int { return a.at.Compare(b.at) })
	if c != nil && c.AppealStatus == safety.AppealPending {
		marks = append(marks, milestone{label: "Appeal pending", date: "now", at: now, done: true, color: components.GroupDM})
		slices.SortStableFunc(marks, func(a, b milestone) int { return a.at.Compare(b.at) })
	} else if slices.ContainsFunc(marks, func(mk milestone) bool { return !mk.done }) {
		marks = append(marks, milestone{label: "Today", date: short(now), at: now, done: true, color: components.Accent})
		slices.SortStableFunc(marks, func(a, b milestone) int { return a.at.Compare(b.at) })
	}
	return marks
}

// nextStep tells the user what the violation means for them now and what they can do about it.
func (m *Model) nextStep(v safety.Violation) string {
	now := m.Session.Today
	c := v.Classification
	expires := ""
	if c != nil && c.Expires.After(now) {
		expires = " It stops counting against the account " + relativeDate(c.Expires.Format(time.RFC3339Nano), now) + "."
	}
	switch v.State(now) {
	case safety.StateInvalidated:
		return "Discord accepted the appeal and removed this violation. It no longer affects account standing."
	case safety.StateUpheld:
		return "Discord reviewed the appeal and kept this violation." + expires
	case safety.StatePending:
		return "Discord is reviewing the appeal. The decision arrives as a system notification from Discord." + expires
	case safety.StateExpired:
		return "This violation has expired and no longer limits the account."
	case safety.StateNotice:
		return "Paste the Safety Hub response to see the actions taken and the appeal status."
	}
	switch {
	case c.COPPA || c.Spam:
		return "This violation cannot be appealed in the app. Discord's support web form handles it." + expires
	case c.AppealIngestion == 1:
		return "Verifying your age with Discord can remove this violation." + expires
	case c.AppealIngestion == 2 && m.Session.Safety.AppealEligible:
		return "You can appeal in Discord under User Settings, Account Standing." + expires
	case c.AppealIngestion == 0:
		return "Appeals for this violation go through Discord's support web form." + expires
	}
	return "No appeal is available for this violation right now." + expires
}

// plainSummary is the copyable text form of a violation, suited to an appeal or support request.
func plainSummary(v safety.Violation, now time.Time) string {
	lines := []string{session.Safe(v.Title()), "Status: " + safety.StateLabels[v.State(now)]}
	if c := v.Classification; c != nil {
		lines = append(lines, "Classification ID: "+c.ID, fmt.Sprintf("Type: %s (%d)", safety.ClassificationLabel(c.Type), c.Type))
		if actions := violationActions(v); len(actions) > 0 {
			lines = append(lines, "Actions: "+sentenceList(actions))
		}
		lines = append(lines, "Appeal: "+safety.AppealLabel(c.AppealStatus))
		if c.Guild != nil {
			lines = append(lines, "Server: "+session.Safe(c.Guild.Name))
		}
		if !c.Expires.IsZero() {
			lines = append(lines, "Expires: "+fullDateTime(c.Expires.Format(time.RFC3339Nano)))
		}
		if c.ExplainerLink != "" {
			lines = append(lines, "Policy: "+c.ExplainerLink)
		}
	}
	if at, exact := v.Incident(); exact {
		lines = append(lines, "Incident: "+fullDateTime(at.Format(time.RFC3339Nano)))
	}
	if ids := v.MessageIDs(); len(ids) > 0 {
		lines = append(lines, "Flagged message IDs: "+strings.Join(ids, ", "))
	}
	var notices []string
	for _, n := range v.Notices {
		notices = append(notices, n.MessageID)
	}
	if len(notices) > 0 {
		lines = append(lines, "Notice message IDs: "+strings.Join(notices, ", "))
	}
	return strings.Join(lines, "\n")
}

// incidentLabel describes the exact-message filter in Investigate, or nothing when it is off.
func (m *Model) incidentLabel() string {
	f := m.Session.Filter
	seconds, ids := len(f.IncidentSeconds), len(f.IncidentIDs)
	switch {
	case seconds == 0 && ids == 0:
		return ""
	case seconds == 1 && ids == 0:
		for second := range f.IncidentSeconds {
			return "Exact: " + time.Unix(second, 0).In(time.Local).Format("2006-01-02 15:04:05")
		}
	case ids == 0:
		return fmt.Sprintf("%d exact incident times", seconds)
	case seconds == 0:
		return session.Plural(ids, "flagged message", "flagged messages")
	}
	return session.Plural(ids, "flagged message", "flagged messages") + ", " + session.Plural(seconds, "time", "times")
}

// violationSummary lists the facts about a violation that matter beside a single message.
func (m *Model) violationSummary(v safety.Violation, width int) string {
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	state := v.State(m.Session.Today)
	lines := []string{lipgloss.NewStyle().Foreground(stateColor(state)).Bold(true).Render(session.Safe(v.Title())) + muted.Render(", "+strings.ToLower(safety.StateLabels[state]))}
	lines = append(lines, lipgloss.NewStyle().Foreground(components.Text).Render(wrapText(m.nextStep(v), width)))
	if track := timeline(m.violationMarks(v), min(width, 96), stateColor(state)); track != "" {
		lines = append(lines, "", track, "")
	}
	if actions := violationActions(v); len(actions) > 0 {
		lines = append(lines, muted.Render("Action      ")+sentenceList(actions))
	}
	if c := v.Classification; c != nil {
		lines = append(lines, muted.Render("Type        ")+safety.ClassificationLabel(c.Type), muted.Render("Appeal      ")+safety.AppealLabel(c.AppealStatus))
	}
	if at, exact := v.Incident(); !at.IsZero() {
		name, value := "Classified  ", store.LocalDate(at.Format(time.RFC3339Nano))
		if exact {
			name, value = "Incident    ", fullDateTime(at.Format(time.RFC3339Nano))
		}
		lines = append(lines, muted.Render(name)+value)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) openViolation(id string) {
	m.Tab = tabViolations
	m.Detail = nil
	m.SafetySelected = id
	m.SafetyGuide = false
	m.SafetyDetail = true
	m.SafetyState, m.SafetyScope, m.SafetyFound = "", "", ""
	if i := slices.IndexFunc(m.Session.Safety.Violations, func(v safety.Violation) bool { return v.ID == id }); i >= 0 {
		m.SafetyOffset = max(0, i-max(1, m.SafetyPage)/2)
	}
	m.Viewport.GotoTop()
	m.Focus = "safety-row-" + id
	m.Input.Blur()
}

// showViolation narrows Investigate to the messages one violation identifies. Without an exact
// time or flagged message, it falls back to the day Discord created the classification.
func (m *Model) showViolation(v safety.Violation) tea.Cmd {
	f := &m.Session.Filter
	f.Dates, f.From, f.Until, f.DateBefore, f.DateAfter = nil, "", "", 0, 0
	f.IncidentSeconds, f.IncidentIDs = nil, nil
	seconds, ids := v.Seconds(), v.MessageIDs()
	if len(seconds) > 0 {
		f.IncidentSeconds = map[int64]int64{}
		for _, n := range v.Notices {
			if !n.System && !n.Time.IsZero() {
				id, _ := strconv.ParseInt(n.MessageID, 10, 64)
				f.IncidentSeconds[n.Time.Unix()] = id
			}
		}
	}
	f.IncidentIDs = ids
	if len(seconds) == 0 && len(ids) == 0 {
		at := v.Time()
		if at.IsZero() {
			m.Notice = "This violation has no time to search by"
			return nil
		}
		f.Dates = []string{at.In(time.Local).Format(time.DateOnly)}
		m.Notice = "No exact time or flagged message, showing the classification date"
	}
	m.DayInput.SetValue("")
	m.Tab = tabInvestigate
	m.Detail = nil
	m.Focus = m.defaultFocus()
	m.focusInput()
	return m.changed()
}

func (m *Model) safetyAction(id string) (tea.Cmd, bool) {
	if rest, ok := strings.CutPrefix(id, "safety-row-"); ok {
		m.SafetySelected = rest
		m.SafetyDetail = true
		m.Viewport.GotoTop()
		return nil, true
	}
	if rest, ok := strings.CutPrefix(id, "safety-msg-"); ok {
		v, found := m.selectedViolation()
		n, err := strconv.Atoi(rest)
		if found && err == nil && n >= 0 && n < len(m.SafetyMatches[v.ID]) {
			r := m.SafetyMatches[v.ID][n]
			m.Detail = &r
			m.Viewport.GotoTop()
		}
		return nil, true
	}
	if key, ok := strings.CutPrefix(id, "safety-sum-"); ok {
		if key == "found" {
			m.SafetyFound = map[bool]string{true: "", false: "found"}[m.SafetyFound == "found"]
		} else {
			m.SafetyState = map[bool]string{true: "", false: key}[m.SafetyState == key]
		}
		m.SafetyOffset = 0
		return nil, true
	}
	if key, ok := strings.CutPrefix(id, "guide-"); ok {
		m.SafetyBrowser = key
		return nil, true
	}
	if line, ok := strings.CutPrefix(id, "safety-scroll-"); ok {
		position, err := strconv.Atoi(line)
		total := len(m.filteredViolations())
		if err == nil && total > m.SafetyPage {
			m.SafetyOffset = min(total-m.SafetyPage, max(0, position*(total-m.SafetyPage)/max(1, m.SafetyPage*2-1)))
		}
		return nil, true
	}
	for _, prefix := range []string{"violation-row-", "violation-srow-"} {
		rest, ok := strings.CutPrefix(id, prefix)
		if !ok {
			continue
		}
		rows := m.Rows
		if prefix == "violation-srow-" {
			rows = m.StatsRows
		}
		if n, err := strconv.Atoi(rest); err == nil && n >= 0 && n < len(rows) {
			if v, found := m.rowViolation(rows[n]); found {
				m.openViolation(v.ID)
			}
		}
		return nil, true
	}
	switch id {
	case "safety-copy-summary":
		if v, found := m.selectedViolation(); found {
			return m.copyText(plainSummary(v, m.Session.Today), "violation summary"), true
		}
	case "safety-copy-id":
		if v, found := m.selectedViolation(); found {
			return m.copyText(v.ID, "classification ID "+v.ID), true
		}
	case "detail-copy":
		if m.Detail != nil {
			return m.copyText(m.Detail.ID, "message ID "+m.Detail.ID), true
		}
	case "violation-detail":
		if m.Detail != nil {
			if v, found := m.rowViolation(*m.Detail); found {
				m.openViolation(v.ID)
			}
		}
	case "safety-paste", "clipboard":
		return m.pasteSafety(), true
	case "safety-guide":
		m.SafetyGuide = !m.SafetyGuide
		m.Viewport.GotoTop()
	case "safety-back":
		m.SafetyDetail = false
		m.Focus = "safety-row-" + m.SafetySelected
	case "safety-show":
		if v, found := m.selectedViolation(); found {
			return m.showViolation(v), true
		}
	case "safety-investigate":
		m.Session.FilterViolations(m.Session.Safety)
		m.DayInput.SetValue("")
		m.Tab = tabInvestigate
		m.Detail = nil
		m.Focus = m.defaultFocus()
		m.focusInput()
		return m.changed(), true
	case "view-violations":
		m.Tab = tabViolations
		m.Detail = nil
		m.Focus = m.defaultFocus()
		m.Input.Blur()
		return m.refreshSafety(), true
	case "safety-clear":
		m.Session.Safety = safety.Report{}
		m.Session.Filter.IncidentSeconds = nil
		m.Session.Filter.IncidentIDs = nil
		m.SafetyMatches = nil
		m.SafetyDetail = false
		m.safetyChanged()
		m.Notice = "Cleared violations"
		m.Focus = m.defaultFocus()
		return m.changed(), true
	default:
		return nil, false
	}
	return nil, true
}

// safetyKey moves the selection through the violation list.
func (m *Model) safetyKey(key string) bool {
	switch key {
	case "pgup":
		m.Viewport.PageUp()
		return true
	case "pgdown":
		m.Viewport.PageDown()
		return true
	}
	list := m.filteredViolations()
	if len(list) == 0 || m.guideVisible() {
		return false
	}
	i := slices.IndexFunc(list, func(v safety.Violation) bool { return v.ID == m.SafetySelected })
	switch key {
	case "up", "down":
		if key == "up" {
			i = max(0, i-1)
		} else {
			i = min(len(list)-1, i+1)
		}
		m.SafetySelected = list[i].ID
		m.Focus = "safety-row-" + list[i].ID
		m.Viewport.GotoTop()
		if i < m.SafetyOffset {
			m.SafetyOffset = i
		} else if i >= m.SafetyOffset+m.SafetyPage {
			m.SafetyOffset = i - m.SafetyPage + 1
		}
		return true
	}
	return false
}

func (m *Model) safetyScroll(delta int) {
	total := len(m.filteredViolations())
	m.SafetyOffset = max(0, min(max(0, total-m.SafetyPage), m.SafetyOffset+delta))
}

func (m *Model) guideVisible() bool {
	return m.SafetyGuide || len(m.Session.Safety.Violations) == 0 && !m.Session.Safety.HubLoaded
}

// flow wraps controls onto as many lines as the width needs.
func flow(items []string, width int) string { return flowGap(items, width, " ") }

func flowGap(items []string, width int, gap string) string {
	var lines []string
	line := ""
	for _, item := range items {
		if item == "" {
			continue
		}
		if line != "" && lipgloss.Width(line)+len(gap)+lipgloss.Width(item) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += gap
		}
		line += item
	}
	return strings.Join(append(lines, line), "\n")
}

func (m *Model) violations(w, h int) string {
	report := m.Session.Safety
	controls := []string{m.button("safety-paste", "Paste from clipboard", false), m.button("safety-guide", "How to copy", m.SafetyGuide)}
	if len(report.Violations) > 0 {
		controls = append(controls,
			m.dropdown("safety-state", optionLabel(safetyStates, m.SafetyState), m.SafetyState != ""),
			m.dropdown("safety-scope", optionLabel(safetyScopes, m.SafetyScope), m.SafetyScope != ""),
			m.dropdown("safety-found", optionLabel(safetyFound, m.SafetyFound), m.SafetyFound != ""),
		)
		m.Tips["safety-investigate"] = "Show every message these violations identify in Investigate"
		controls = append(controls, m.button("safety-investigate", "Show in Investigate", false))
	}
	if len(report.Violations) > 0 || report.HubLoaded {
		controls = append(controls, m.button("safety-clear", "Clear", false))
	}
	parts := []string{flow(controls, w)}
	if report.HubLoaded {
		parts = append(parts, m.standing(w))
	}
	if len(report.Violations) > 0 && !m.guideVisible() {
		parts = append(parts, m.violationCounts(w))
	}
	list := m.filteredViolations()
	if len(list) > 0 && !slices.ContainsFunc(list, func(v safety.Violation) bool { return v.ID == m.SafetySelected }) {
		m.SafetySelected = list[0].ID
	}
	title := "Violations"
	if n := len(report.Violations); n > 0 {
		title = fmt.Sprintf("%d %s", n, map[bool]string{true: "violation", false: "violations"}[n == 1])
		if len(list) != n {
			title += fmt.Sprintf(", %d shown", len(list))
		}
	}
	if m.guideVisible() {
		title = "Copy violations from Discord"
	}
	parts = append(parts, components.TitleRule(title, w, m.Frame, m.SafetyRowsLoading))
	remaining := max(3, h-lipgloss.Height(strings.Join(parts, "\n")))
	switch {
	case m.guideVisible():
		parts = append(parts, m.guide(w, remaining))
	case len(report.Violations) == 0:
		parts = append(parts, lipgloss.NewStyle().Foreground(components.Green).Render("No violations on this account."))
	case len(list) == 0:
		parts = append(parts, lipgloss.NewStyle().Foreground(components.Muted).Render("No violations match these filters."))
	case m.wideViolations():
		listWidth := max(36, w*2/5)
		left := m.violationList(list, listWidth, remaining)
		right := lipgloss.NewStyle().Width(w-listWidth-1).Height(remaining).Padding(0, 1).BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(components.Border).Render(m.violationDetail(w-listWidth-4, remaining))
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	case m.SafetyDetail:
		parts = append(parts, m.button("safety-back", "‹ All violations", false), m.violationDetail(w, remaining-1))
	default:
		parts = append(parts, m.violationList(list, w, remaining))
	}
	return strings.Join(parts, "\n")
}

func (m *Model) wideViolations() bool { return m.Width-4 >= 100 }

// standing draws the account standing scale. The filled track ends at the current state, so the
// position reads without color, and it sweeps in when a paste changes the standing.
func (m *Model) standing(w int) string {
	report := m.Session.Safety
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	current := slices.Index(safety.Standings, report.Standing)
	appeals := "Not eligible to appeal"
	if len(report.AppealEligibility) > 0 {
		var kinds []string
		for _, kind := range report.AppealEligibility {
			kinds = append(kinds, safety.EligibilityLabel(kind))
		}
		appeals = "Can appeal: " + sentenceList(kinds)
	} else if report.AppealEligible || report.DSAEligible {
		appeals = "Can appeal"
	}
	head := muted.Render("Account standing ") + lipgloss.NewStyle().Foreground(standingColor(report.Standing)).Bold(true).Render(safety.StandingLabel(report.Standing))
	if detail := safety.StandingDetail(report.Standing); detail != "" && lipgloss.Width(head)+2+len(detail) <= w {
		head += muted.Render("  " + detail)
	}
	appealText := muted.Render(appeals)
	if lipgloss.Width(head)+3+lipgloss.Width(appealText) <= w {
		head += strings.Repeat(" ", w-lipgloss.Width(head)-lipgloss.Width(appealText)) + appealText
	} else {
		head += "\n" + components.FitStyled(appealText, w)
	}
	segment := min(18, (w+1)/len(safety.Standings)-1)
	if m.Height < 30 || current < 0 || segment < 8 {
		return head
	}
	progress := 1.0
	if age := time.Since(m.SafetyStandingAt); age < standingSweep {
		t := float64(age) / float64(standingSweep)
		progress = 1 - (1-t)*(1-t)*(1-t)
	}
	filled := progress * float64((current+1)*segment)
	var scale []string
	for i, state := range safety.Standings {
		id := fmt.Sprintf("standing-%d", state)
		color := standingColor(state)
		var track strings.Builder
		for c := range segment {
			switch {
			case float64(i*segment+c) >= filled:
				track.WriteString(lipgloss.NewStyle().Foreground(components.Border).Render("─"))
			case i == current:
				track.WriteString(lipgloss.NewStyle().Foreground(color).Render("█"))
			default:
				track.WriteString(lipgloss.NewStyle().Foreground(color).Render("━"))
			}
		}
		name := components.Fit(safety.StandingLabel(state), segment)
		label := muted.Render(name)
		switch {
		case m.Hover == id:
			label = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Underline(true).Render(name)
		case i == current:
			label = lipgloss.NewStyle().Foreground(color).Bold(true).Render(name)
		}
		if i > 0 {
			scale = append(scale, " ")
		}
		column := lipgloss.NewStyle().Width(segment).Render(track.String() + "\n" + label)
		scale = append(scale, m.tip(id, safety.StandingLabel(state)+": "+safety.StandingDetail(state), column))
	}
	return head + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, scale...)
}

// violationCounts summarizes the list by state. Each count filters the list to that state.
func (m *Model) violationCounts(w int) string {
	now := m.Session.Today
	counts := map[string]int{}
	expiring, flagged, found := 0, 0, 0
	var next safety.Violation
	for _, v := range m.Session.Safety.Violations {
		state := v.State(now)
		counts[state]++
		if c := v.Classification; c != nil && state == safety.StateActive && c.Expires.After(now) && c.Expires.Before(now.AddDate(0, 0, 30)) {
			if expiring == 0 || c.Expires.Before(next.Classification.Expires) {
				next = v
			}
			expiring++
		}
		for _, id := range v.MessageIDs() {
			flagged++
			if slices.ContainsFunc(m.SafetyMatches[v.ID], func(r store.Row) bool { return r.ID == id }) {
				found++
			}
		}
	}
	item := func(id, text string, value int, color lipgloss.Color, active bool) string {
		number := lipgloss.NewStyle().Foreground(color).Bold(true)
		label := lipgloss.NewStyle().Foreground(components.Muted)
		if active {
			label = label.Foreground(components.Text).Underline(true)
		}
		if m.Hover == id || m.Focus == id {
			number = number.Background(components.SurfaceHover)
			label = label.Foreground(lipgloss.Color("#FFFFFF")).Background(components.SurfaceHover).Underline(true)
		}
		m.Actions = append(m.Actions, id)
		return m.Zones.Mark(id, m.figure("safety/"+id, fmt.Sprint(value), value, number, color)+label.Render(" "+text))
	}
	var items []string
	for _, state := range []string{safety.StateActive, safety.StatePending, safety.StateUpheld, safety.StateInvalidated, safety.StateExpired, safety.StateNotice} {
		if counts[state] > 0 {
			items = append(items, item("safety-sum-"+state, strings.ToLower(safety.StateLabels[state]), counts[state], stateColor(state), m.SafetyState == state))
		}
	}
	if expiring > 0 {
		at := next.Classification.Expires.Format(time.RFC3339Nano)
		text := lipgloss.NewStyle().Foreground(components.GroupDM).Bold(true).Render(fmt.Sprint(expiring)) + mutedText(" expiring within 30 days")
		items = append(items, m.tip("safety-sum-expiring", "Next: "+session.Safe(next.Title())+", "+relativeDate(at, now), text))
	}
	if flagged > 0 {
		items = append(items, item("safety-sum-found", fmt.Sprintf("of %d flagged found", flagged), found, components.Green, m.SafetyFound == "found"))
	}
	return flowGap(items, w, "    ")
}

// violationOverview is the one-line state of every loaded violation, shown where the tab is named.
func (m *Model) violationOverview() string {
	counts := map[string]int{}
	for _, v := range m.Session.Safety.Violations {
		counts[v.State(m.Session.Today)]++
	}
	var parts []string
	for _, state := range []string{safety.StateActive, safety.StatePending, safety.StateUpheld, safety.StateInvalidated, safety.StateExpired, safety.StateNotice} {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], strings.ToLower(safety.StateLabels[state])))
		}
	}
	text := strings.Join(parts, ", ")
	if m.Session.Safety.HubLoaded {
		text += ". Account standing " + strings.ToLower(safety.StandingLabel(m.Session.Safety.Standing))
	}
	return text
}

func mutedText(text string) string {
	return lipgloss.NewStyle().Foreground(components.Muted).Render(text)
}

func (m *Model) violationList(list []safety.Violation, w, h int) string {
	m.SafetyPage = max(1, h/2)
	m.SafetyOffset = min(m.SafetyOffset, max(0, len(list)-m.SafetyPage))
	visible := list[m.SafetyOffset:min(len(list), m.SafetyOffset+m.SafetyPage)]
	listWidth := w
	if len(list) > m.SafetyPage {
		listWidth = w - 2
	}
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	var rows []string
	for _, v := range visible {
		id := "safety-row-" + v.ID
		selected := v.ID == m.SafetySelected
		hover := m.Hover == id || m.Focus == id
		state := v.State(m.Session.Today)
		rowStyle := lipgloss.NewStyle().Padding(0, 1).Width(listWidth - 2)
		if hover {
			rowStyle = rowStyle.Background(components.SurfaceHover)
		} else if selected {
			rowStyle = rowStyle.Background(components.Surface)
		}
		at := v.Time()
		when := ""
		if !at.IsZero() {
			when = relativeDate(at.Format(time.RFC3339Nano), m.Session.Today)
		}
		right := lipgloss.NewStyle().Foreground(stateColor(state)).Render(safety.StateLabels[state])
		inner := listWidth - 4
		titleStyle := lipgloss.NewStyle().Foreground(components.Text).Bold(selected || hover)
		if age := time.Since(m.SafetyArrivals[v.ID]); age < arrivalGlow {
			glow := components.Blend(components.Pink, components.Text, float64(age)/float64(arrivalGlow))
			titleStyle = titleStyle.Foreground(glow).Bold(true)
			right = lipgloss.NewStyle().Foreground(glow).Render("New  ") + right
		}
		if hover {
			titleStyle = titleStyle.Foreground(lipgloss.Color("#FFFFFF")).Underline(true)
		}
		titleText := titleStyle.Render(components.Fit(v.Title(), max(6, inner-lipgloss.Width(right)-2)))
		line := titleText + strings.Repeat(" ", max(1, inner-lipgloss.Width(titleText)-lipgloss.Width(right))) + right
		var summary []string
		if actions := violationActions(v); len(actions) > 0 {
			summary = append(summary, sentenceList(actions))
		}
		if v.Classification == nil {
			summary = append(summary, "From a Discord notice")
		}
		if n := len(v.MessageIDs()); n > 0 {
			summary = append(summary, fmt.Sprintf("%d flagged", n))
		}
		if n := len(m.SafetyMatches[v.ID]); n > 0 {
			summary = append(summary, fmt.Sprintf("%d in packages", n))
		}
		date := muted.Render(when)
		details := muted.Render(components.Fit(strings.Join(summary, ", "), max(6, inner-lipgloss.Width(date)-2)))
		details += strings.Repeat(" ", max(1, inner-lipgloss.Width(details)-lipgloss.Width(date))) + date
		m.Actions = append(m.Actions, id)
		rows = append(rows, m.Zones.Mark(id, rowStyle.Render(line+"\n"+details)))
	}
	body := strings.Join(rows, "\n")
	if len(list) <= m.SafetyPage {
		return body
	}
	height := max(1, lipgloss.Height(body))
	return lipgloss.JoinHorizontal(lipgloss.Top, body, " ", m.scrollbarView("safety-scroll", m.SafetyOffset, len(list), m.SafetyPage, height))
}

func (m *Model) violationDetail(w, h int) string {
	v, ok := m.selectedViolation()
	preview := false
	if id, hovering := strings.CutPrefix(m.Hover, "safety-row-"); hovering && m.wideViolations() && id != m.SafetySelected {
		for _, candidate := range m.Session.Safety.Violations {
			if candidate.ID == id {
				v, ok, preview = candidate, true, true
			}
		}
	}
	if !ok {
		return lipgloss.NewStyle().Foreground(components.Muted).Render("Select a violation.")
	}
	now := m.Session.Today
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	text := lipgloss.NewStyle().Foreground(components.Text)
	state := v.State(now)
	field := func(name, value string) string {
		return muted.Render(fmt.Sprintf("%-12s", name)) + value
	}
	wrap := func(s string, indent int) string {
		return strings.ReplaceAll(ansi.Wordwrap(session.Safe(s), max(10, w-indent), ""), "\n", "\n"+strings.Repeat(" ", indent))
	}
	lines := []string{
		lipgloss.NewStyle().Foreground(stateColor(state)).Bold(true).Render(wrap(v.Title(), 0)),
		lipgloss.NewStyle().Foreground(stateColor(state)).Render(safety.StateLabels[state]) + muted.Render(", "+map[string]string{"account": "account violation", "server": "server violation"}[v.Scope()]),
		"",
	}
	lines = append(lines[:2], text.Render(wrap(m.nextStep(v), 0)), "")
	show := "Show messages"
	if len(v.Seconds()) == 0 && len(v.MessageIDs()) == 0 {
		show = "Show messages that day"
	}
	buttons := []string{m.button("safety-show", show, false), m.button("safety-copy-summary", "Copy summary", false)}
	if v.Classification != nil {
		buttons = append(buttons, m.button("safety-copy-id", "Copy ID", false))
	}
	lines = append(lines, flow(buttons, w), "")
	if track := timeline(m.violationMarks(v), w, stateColor(state)); track != "" {
		lines = append(lines, track, "")
	}
	at, exact := v.Incident()
	if c := v.Classification; c != nil {
		lines = append(lines, field("Type", text.Render(safety.ClassificationLabel(c.Type))))
		if c.Guild != nil {
			lines = append(lines, field("Server", text.Render(wrap(c.Guild.Name, 12))+muted.Render(", "+strings.ToLower(safety.MemberLabel(c.Guild.MemberType)))))
		}
		for i, a := range c.Actions {
			name := "Action"
			if i > 0 {
				name = ""
			}
			value := text.Render(safety.ActionLabel(a.Type))
			if len(a.Descriptions) > 0 {
				value = m.tip(fmt.Sprintf("safety-action-%d", i), session.Safe(strings.Join(a.Descriptions, " ")), value)
			}
			lines = append(lines, field(name, value))
		}
		appeal := lipgloss.NewStyle().Foreground(stateColor(state)).Render(safety.AppealLabel(c.AppealStatus))
		lines = append(lines, field("Appeal", m.tip("safety-appeal", safety.AppealDetail(c.AppealStatus), appeal)))
		method := safety.IngestionLabel(c.AppealIngestion)
		if c.COPPA || c.Spam {
			method += ", not in the app"
		}
		lines = append(lines, field("Appeal by", text.Render(method)))
		var flags []string
		if c.COPPA {
			flags = append(flags, "COPPA")
		}
		if c.Spam {
			flags = append(flags, "Spam")
		}
		if len(flags) > 0 {
			lines = append(lines, field("Flags", text.Render(strings.Join(flags, ", "))))
		}
	}
	if !at.IsZero() {
		stamp := at.Format(time.RFC3339Nano)
		if exact {
			lines = append(lines, field("Incident", text.Render(fullDateTime(stamp))+muted.Render(", "+relativeDate(stamp, now))))
		}
	}
	if created := safety.SnowflakeTime(v.ID); !created.IsZero() {
		stamp := created.Format(time.RFC3339Nano)
		lines = append(lines, field("Classified", text.Render(fullDateTime(stamp))+muted.Render(", "+relativeDate(stamp, now))))
	}
	if c := v.Classification; c != nil {
		switch {
		case c.Expires.IsZero():
			lines = append(lines, field("Expires", muted.Render("No expiry given")))
		case c.Expires.After(now):
			stamp := c.Expires.Format(time.RFC3339Nano)
			lines = append(lines, field("Expires", text.Render(store.LocalDate(stamp))+muted.Render(", "+relativeDate(stamp, now))))
		default:
			lines = append(lines, field("Expired", muted.Render(store.LocalDate(c.Expires.Format(time.RFC3339Nano)))))
		}
		lines = append(lines, field("ID", muted.Render(v.ID)))
		if c.ExplainerLink != "" {
			lines = append(lines, field("Policy", muted.Render(wrap(c.ExplainerLink, 12))))
		}
		lines = append(lines, "", components.TitleRule("Flagged content", w, m.Frame, false))
		if len(c.Flagged) == 0 {
			lines = append(lines, muted.Render("Discord did not include the flagged content."))
		}
		for _, f := range c.Flagged {
			found := slices.ContainsFunc(m.SafetyMatches[v.ID], func(r store.Row) bool { return r.ID == f.ID })
			status := muted.Render(", not in loaded packages")
			if found {
				status = lipgloss.NewStyle().Foreground(components.Green).Render(", found in packages")
			}
			lines = append(lines, text.Bold(true).Render("Message "+session.Safe(f.ID))+status)
			if strings.TrimSpace(f.Content) != "" {
				lines = append(lines, text.Render(wrap(f.Content, 0)))
			}
			for _, name := range f.Attachments {
				lines = append(lines, muted.Render("Attachment "+components.Fit(name, w-11)))
			}
		}
	} else {
		lines = append(lines, field("ID", muted.Render(v.ID)))
	}
	if len(v.Notices) > 0 {
		lines = append(lines, "", components.TitleRule("Notices", w, m.Frame, false))
		for _, n := range v.Notices {
			kind := "Policy notice"
			if n.System {
				kind = "System notification"
			}
			heading := text.Bold(true).Render(kind)
			if !n.Time.IsZero() {
				heading += muted.Render(", " + fullDateTime(n.Time.Format(time.RFC3339Nano)))
			}
			lines = append(lines, heading)
			if n.Header != "" {
				lines = append(lines, text.Render(wrap(n.Header, 0)))
			}
			if n.Body != "" {
				lines = append(lines, muted.Render(wrap(n.Body, 0)))
			}
		}
	}
	lines = append(lines, "", components.TitleRule("Messages in packages", w, m.Frame, m.SafetyRowsLoading))
	matches := m.SafetyMatches[v.ID]
	switch {
	case len(matches) > 0:
		for i, r := range matches {
			id := fmt.Sprintf("safety-msg-%d", i)
			hover := m.Hover == id || m.Focus == id
			date := muted.Render(relativeDate(r.Date, now))
			location := styledMessageLocation(r, max(8, w-lipgloss.Width(date)-2), components.Accent, -1, hover, false)
			row := location + strings.Repeat(" ", max(1, w-lipgloss.Width(location)-lipgloss.Width(date))) + date + "\n" + text.Render(components.Fit(messagePreview(r), w))
			style := lipgloss.NewStyle().Width(w)
			if hover {
				style = style.Background(components.SurfaceHover)
			}
			m.Actions = append(m.Actions, id)
			lines = append(lines, m.Zones.Mark(id, style.Render(row)))
		}
	case len(m.Snapshots) == 0:
		lines = append(lines, muted.Render("Add a package in Investigate to find these messages."))
	case len(v.Seconds()) == 0 && len(v.MessageIDs()) == 0:
		lines = append(lines, muted.Render("No exact time or flagged message to match."))
	case m.SafetyRowsLoading:
		lines = append(lines, components.Working("Finding messages…", m.Frame, w))
	default:
		lines = append(lines, muted.Render("No loaded message matches this violation."))
	}
	m.Viewport.Width = w
	m.Viewport.Height = max(1, h)
	offset := m.Viewport.YOffset
	m.Viewport.SetContent(lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n")))
	if preview {
		m.Viewport.SetYOffset(0)
		view := m.Viewport.View()
		m.Viewport.SetYOffset(offset)
		return m.Zones.Mark("safety-detail", view)
	}
	return m.Zones.Mark("safety-detail", m.Viewport.View())
}

func (m *Model) guide(w, h int) string {
	code := lipgloss.NewStyle().Foreground(components.Cyan).Bold(true)
	text := lipgloss.NewStyle().Foreground(components.Text)
	muted := lipgloss.NewStyle().Foreground(components.Muted)
	terms := []string{"@me", "messages", "Fetch/XHR", "XHR", "XHR/Fetch", "Network"}
	highlight := func(s string, width, indent int) string {
		lines := strings.Split(ansi.Wordwrap(s, max(10, width-indent), ""), "\n")
		for i, line := range lines {
			words := strings.Split(line, " ")
			for j, word := range words {
				trimmed := strings.TrimRight(word, ".,")
				if slices.Contains(terms, trimmed) {
					words[j] = code.Render(trimmed) + text.Render(word[len(trimmed):])
				} else if word != "" {
					words[j] = text.Render(word)
				}
			}
			lines[i] = strings.Join(words, " ")
			if i > 0 {
				lines[i] = strings.Repeat(" ", indent) + lines[i]
			}
		}
		return strings.Join(lines, "\n")
	}
	numbered := func(steps []string, width int) string {
		var out []string
		for i, step := range steps {
			out = append(out, muted.Render(fmt.Sprintf("%d. ", i+1))+highlight(step, width, 3))
		}
		return strings.Join(out, "\n")
	}
	selected := m.SafetyBrowser
	if key, ok := strings.CutPrefix(m.Hover, "guide-"); ok {
		selected = key
	}
	if selected == "" {
		selected = "chrome"
	}
	current := browserGuides[0]
	var buttons []string
	for _, b := range browserGuides {
		if b.key == selected {
			current = b
		}
		buttons = append(buttons, m.button("guide-"+b.key, b.label, b.key == cmp.Or(m.SafetyBrowser, "chrome")))
	}
	wide := w >= 110
	leftWidth := w
	if wide {
		leftWidth = w/2 - 1
	}
	generic := numbered(guideSteps, leftWidth)
	var notes []string
	for _, note := range guideNotes {
		notes = append(notes, muted.Render(wrapText(note, leftWidth)))
	}
	left := generic + "\n\n" + strings.Join(notes, "\n")
	rightWidth := w
	if wide {
		rightWidth = w - leftWidth - 2
	}
	panel := components.TitledBox(current.label, numbered(current.steps, rightWidth-4), rightWidth, 1, lipgloss.RoundedBorder(), components.GradientColor(.48), "", m.Frame, false)
	right := flow(buttons, rightWidth) + "\n" + panel
	body := left + "\n\n" + right
	if wide {
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth).Render(left), "  ", right)
	}
	if lipgloss.Height(body) <= h {
		return body
	}
	m.Viewport.Width = w
	m.Viewport.Height = max(1, h)
	m.Viewport.SetContent(body)
	return m.Zones.Mark("safety-detail", m.Viewport.View())
}

func wrapText(s string, width int) string {
	return ansi.Wordwrap(s, max(10, width), "")
}
