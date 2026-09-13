package tui

import (
	"slices"
	"strings"

	"github.com/ellypaws/unpackage/pkg/session"
)

func (m *Model) enabled(id string) bool {
	if m.Executing && id != "stop" {
		return false
	}
	if strings.HasPrefix(id, "pick-") {
		return m.Picker != nil && m.Picker.Enabled(id)
	}
	f := m.Session.Filter
	switch id {
	case "days-apply":
		dates, seconds, err := session.Dates(m.DayInput.Value(), m.Session.Today)
		if err != nil {
			return false
		}
		if len(seconds) > 0 {
			added := false
			for second := range seconds {
				if _, ok := f.IncidentSeconds[second]; !ok {
					added = true
				}
			}
			return added || len(f.Dates) > 0 || f.From != "" || f.Until != "" || f.DateBefore != 0 || f.DateAfter != 0
		}
		return len(f.IncidentSeconds) > 0 || f.From != "" || f.Until != "" || slices.ContainsFunc(dates, func(day string) bool { return !slices.Contains(f.Dates, day) })
	case "dates-clear":
		return len(f.Dates) > 0 || len(f.IncidentSeconds) > 0 || f.From != "" || f.Until != ""
	case "clipboard":
		return m.IncidentProcessing == ""
	case "clear":
		return len(f.Dates) > 0 || len(f.IncidentSeconds) > 0 || len(f.Guilds) > 0 || len(f.ExcludedGuilds) > 0 || f.From != "" || f.Until != "" || f.Search != "" || f.Media != "" || f.Mode != "auto" || f.DateBefore != 0 || f.DateAfter != 0 || f.Channel != "" || f.HideEventOnly || m.DayInput.Value() != "" || m.SearchInput.Value() != ""
	case "previous":
		return m.Offset > 0 && !m.Loading
	case "next":
		return m.Offset+m.pageSize() < m.resultCount() && !m.Loading
	case "servers-prev":
		return m.ServerOffset > 0
	case "servers-next":
		return m.ServerOffset+m.serverPageSize() < len(m.filteredServers())
	case "servers-clear":
		included, excluded := m.targetGuilds()
		return len(*included) > 0 || len(*excluded) > 0
	case "stats-prev":
		return m.StatsOffset > 0
	case "stats-next":
		return m.StatsOffset+m.StatsPage < m.StatsTotal
	case "search-apply":
		return m.SearchInput.Value() != f.Search
	case "servers-search":
		return m.ServerInput.Value() != m.ServerSearch
	case "command-run":
		return strings.TrimSpace(m.Input.Value()) != ""
	case "request-save":
		return strings.TrimSpace(m.RequestInput.Value()) != "" && (len(f.Guilds) > 0 || len(f.ExcludedGuilds) > 0) && (m.RequestScope != "filtered" || m.resultCount() > 0)
	case "draft":
		return len(f.Guilds) > 0 || len(f.ExcludedGuilds) > 0
	case "margin-apply":
		if len(f.IncidentSeconds) > 0 {
			return false
		}
		before, beforeErr := session.Margin(m.BeforeInput.Value())
		after, afterErr := session.Margin(m.AfterInput.Value())
		return beforeErr == nil && afterErr == nil && (before != f.DateBefore || after != f.DateAfter)
	case "margin-clear":
		return f.DateBefore != 0 || f.DateAfter != 0 || strings.Trim(m.BeforeInput.Value(), " 0") != "" || strings.Trim(m.AfterInput.Value(), " 0") != ""
	case "cal-clear":
		return m.Calendar != nil && len(m.Calendar.Dates) > 0
	case "cal-apply":
		return m.Calendar != nil && (len(f.IncidentSeconds) > 0 || !slices.Equal(m.Calendar.Dates, f.Dates) || f.From != "" || f.Until != "")
	case "stop":
		return m.Session.Busy()
	case "log-follow":
		return !m.FollowLog
	case "clear-old", "clear-new", "browse-old", "browse-new":
		slot := 0
		if strings.HasSuffix(id, "new") {
			slot = 1
		}
		for _, snapshot := range m.Snapshots {
			if snapshot.Slot == slot {
				return strings.HasPrefix(id, "clear-") || snapshot.State != "loading"
			}
		}
		return strings.HasPrefix(id, "browse-")
	}
	return true
}
