package session

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ellypaws/unpackage/pkg/safety"
)

// ReadSafety parses a saved Safety Hub or safety notice response.
func ReadSafety(path string) (safety.Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return safety.Report{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil {
		return safety.Report{}, err
	}
	report, ok, err := safety.Parse(data)
	if err != nil {
		return safety.Report{}, err
	}
	if !ok {
		return safety.Report{}, fmt.Errorf("file has no Safety Hub data or safety notices")
	}
	return report, nil
}

// FilterViolations adds every incident second and flagged message ID in the report to the
// exact-message filter and clears day-based date filters, which it replaces.
func (s *Session) FilterViolations(report safety.Report) (added int) {
	seconds := maps.Clone(s.Filter.IncidentSeconds)
	if seconds == nil {
		seconds = map[int64]int64{}
	}
	before := len(seconds) + len(s.Filter.IncidentIDs)
	maps.Copy(seconds, report.Seconds())
	ids := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(s.Filter.IncidentIDs), report.MessageIDs()...))))
	if len(seconds) == 0 {
		seconds = nil
	}
	s.Filter.IncidentSeconds = seconds
	s.Filter.IncidentIDs = ids
	s.Filter.Dates = nil
	s.Filter.From = ""
	s.Filter.Until = ""
	s.Filter.DateBefore = 0
	s.Filter.DateAfter = 0
	s.Filter.Offset = 0
	return len(seconds) + len(ids) - before
}

// MergeSummary describes what a paste added in one line.
func MergeSummary(result safety.Result, report safety.Report) string {
	var parts []string
	if result.Hub {
		parts = append(parts, "Safety Hub updated")
	}
	if result.Classifications > 0 {
		parts = append(parts, Plural(result.Classifications, "new violation", "new violations"))
	}
	if result.Notices > 0 {
		parts = append(parts, Plural(result.Notices, "new notice", "new notices"))
	}
	if len(parts) == 0 {
		parts = append(parts, "No new violations")
	}
	return strings.Join(parts, ", ") + ", " + Plural(len(report.Violations), "violation", "violations") + " total"
}

func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (s *Session) violations(arg string, w io.Writer) error {
	switch arg {
	case "":
		return writeViolations(w, s.Safety, s.Today)
	case "clear":
		s.Safety = safety.Report{}
		s.Filter.IncidentSeconds = nil
		s.Filter.IncidentIDs = nil
		s.Filter.Offset = 0
		fmt.Fprintln(w, "Cleared violations")
		return nil
	}
	report, err := ReadSafety(arg)
	if err != nil {
		return err
	}
	result := s.Safety.Merge(report)
	s.FilterViolations(s.Safety)
	fmt.Fprintln(w, MergeSummary(result, s.Safety))
	return nil
}

func writeViolations(w io.Writer, report safety.Report, now time.Time) error {
	if len(report.Violations) == 0 && !report.HubLoaded {
		_, err := fmt.Fprintln(w, "No violations loaded. Use violations \"response.json\" or Paste from clipboard in Violations.")
		return err
	}
	if report.HubLoaded {
		fmt.Fprintf(w, "Account standing: %s\n", safety.StandingLabel(report.Standing))
	}
	for _, v := range report.Violations {
		at, exact := v.Incident()
		when := "classified " + at.In(time.Local).Format(time.DateOnly)
		if exact {
			when = "incident " + at.In(time.Local).Format("2006-01-02 15:04:05")
		}
		if at.IsZero() {
			when = "time unknown"
		}
		fields := []string{Safe(v.Title()), safety.StateLabels[v.State(now)], when}
		if c := v.Classification; c != nil {
			for _, a := range c.Actions {
				fields = append(fields, safety.ActionLabel(a.Type))
			}
			if len(c.Flagged) > 0 {
				fields = append(fields, Plural(len(c.Flagged), "flagged message", "flagged messages"))
			}
		}
		fmt.Fprintf(w, "%s\t%s\n", v.ID, strings.Join(fields, "\t"))
	}
	return nil
}
