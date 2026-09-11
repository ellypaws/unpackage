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
		dates, err := session.Dates(m.DayInput.Value(), m.Session.Today)
		return err == nil && (f.From != "" || f.Until != "" || slices.ContainsFunc(dates, func(day string) bool { return !slices.Contains(f.Dates, day) }))
	case "dates-clear":
		return len(f.Dates) > 0 || f.From != "" || f.Until != ""
	case "clear":
		return len(f.Dates) > 0 || len(f.Guilds) > 0 || f.From != "" || f.Until != "" || f.Search != "" || f.Media != "" || f.Mode != "auto" || f.DateBefore != 0 || f.DateAfter != 0 || m.DayInput.Value() != "" || m.SearchInput.Value() != ""
	case "previous":
		return m.Offset > 0 && !m.Loading
	case "next":
		return m.Offset+m.pageSize() < m.resultCount() && !m.Loading
	case "servers-prev":
		return m.ServerOffset > 0
	case "servers-next":
		return m.ServerOffset+m.serverPageSize() < len(m.filteredServers())
	case "servers-clear":
		return len(f.Guilds) > 0
	case "search-apply":
		return m.SearchInput.Value() != f.Search
	case "servers-search":
		return m.ServerInput.Value() != m.ServerSearch
	case "command-run":
		return strings.TrimSpace(m.Input.Value()) != ""
	case "request-save":
		return strings.TrimSpace(m.RequestInput.Value()) != "" && len(f.Guilds) > 0 && (m.RequestScope != "filtered" || m.resultCount() > 0)
	case "draft":
		return len(f.Guilds) > 0
	case "margin-apply":
		before, beforeErr := session.Margin(m.BeforeInput.Value())
		after, afterErr := session.Margin(m.AfterInput.Value())
		return beforeErr == nil && afterErr == nil && (before != f.DateBefore || after != f.DateAfter)
	case "margin-clear":
		return f.DateBefore != 0 || f.DateAfter != 0 || strings.Trim(m.BeforeInput.Value(), " 0") != "" || strings.Trim(m.AfterInput.Value(), " 0") != ""
	case "cal-clear":
		return m.Calendar != nil && len(m.Calendar.Dates) > 0
	case "cal-apply":
		return m.Calendar != nil && (!slices.Equal(m.Calendar.Dates, f.Dates) || f.From != "" || f.Until != "")
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
