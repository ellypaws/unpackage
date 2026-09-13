package session

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ellypaws/unpackage/pkg/store"
)

var weekdays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// statsRange takes an optional trailing day count or "all" and returns the remaining arguments.
func (s *Session) statsRange(args []string) (store.StatsFilter, []string) {
	f := store.StatsFilter{Guilds: slices.Clone(s.Filter.Guilds), ExcludedGuilds: slices.Clone(s.Filter.ExcludedGuilds)}
	days := 0
	if n := len(args); n > 0 {
		last := strings.ToLower(args[n-1])
		if _, err := strconv.Atoi(last); err == nil || last == "all" {
			days = RangeDays(last)
			args = args[:n-1]
		}
	}
	if days > 0 {
		f.From, f.Until = RangeWindow(days, s.Today)
	}
	return f, args
}

func rangeText(f store.StatsFilter) string {
	if f.From.IsZero() {
		return "all time"
	}
	return f.From.Format(time.DateOnly) + " to " + f.Until.AddDate(0, 0, -1).Format(time.DateOnly)
}

func metricText(metric store.Metric, v int) string {
	if !metric.Duration() {
		return strconv.Itoa(v)
	}
	return fmt.Sprintf("%dh %02dm", v/3600, v%3600/60)
}

func (s *Session) Statistics(ctx context.Context, a []string, w io.Writer) error {
	f, args := s.statsRange(a[1:])
	switch a[0] {
	case "summary":
		if len(args) != 0 {
			return fmt.Errorf("summary [DAYS|all]")
		}
		st, err := s.Store.Stats(ctx, f)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "range\t%s\n", rangeText(f))
		for metric := range store.MetricCount {
			fmt.Fprintf(w, "%s\t%s\n", metric.Key(), metricText(metric, st.Totals[metric]))
		}
		fmt.Fprintf(w, "servers-joined\t%d\n", st.Joined)
		return nil
	case "leaders":
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("leaders servers|channels|people|games|platforms|emoji [METRIC] [DAYS|all]")
		}
		entity := store.Entity(strings.ToLower(args[0]))
		if !slices.Contains(store.Entities, entity) {
			return fmt.Errorf("unknown entity %q", args[0])
		}
		metric := entity.Metrics()[0]
		if len(args) == 2 {
			parsed, ok := store.ParseMetric(args[1])
			if !ok || !slices.Contains(entity.Metrics(), parsed) {
				return fmt.Errorf("unknown metric %q for %s", args[1], entity)
			}
			metric = parsed
		}
		st, err := s.Store.Stats(ctx, f)
		if err != nil {
			return err
		}
		leaders := slices.Clone(st.Leaders(entity))
		leaders = slices.DeleteFunc(leaders, func(l store.Leader) bool { return l.Values[metric] == 0 })
		store.SortLeaders(leaders, metric)
		for i, l := range leaders {
			if err := ctx.Err(); err != nil {
				return err
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", i+1, l.ID, Safe(l.Name), metricText(metric, l.Values[metric]))
		}
		return nil
	case "heatmap":
		if len(args) > 1 {
			return fmt.Errorf("heatmap [METRIC] [DAYS|all]")
		}
		metric := store.MetricMessages
		if len(args) == 1 {
			parsed, ok := store.ParseMetric(args[0])
			if !ok {
				return fmt.Errorf("unknown metric %q", args[0])
			}
			metric = parsed
		}
		st, err := s.Store.Stats(ctx, f)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s by weekday and local hour, %s\n", metric.Label(), rangeText(f))
		var header strings.Builder
		header.WriteString("day")
		for hour := range 24 {
			fmt.Fprintf(&header, "\t%02d", hour)
		}
		fmt.Fprintln(w, header.String())
		for day := range 7 {
			var line strings.Builder
			line.WriteString(weekdays[day])
			for hour := range 24 {
				v := st.Weekly[metric][day][hour]
				if metric.Duration() {
					v /= 60
				}
				fmt.Fprintf(&line, "\t%d", v)
			}
			fmt.Fprintln(w, line.String())
		}
		if metric.Duration() {
			fmt.Fprintln(w, "Values are minutes.")
		}
		return nil
	}
	return fmt.Errorf("unknown command %q; see help", a[0])
}
