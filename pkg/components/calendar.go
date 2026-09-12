package components

import (
	"fmt"
	"slices"
	"strings"
	"time"

	zone "github.com/lrstanley/bubblezone"
)

type Calendar struct {
	Month, Cursor time.Time
	Dates         []string
}

func NewCalendar(now time.Time, dates []string) Calendar {
	return Calendar{Month: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), Cursor: now, Dates: slices.Clone(dates)}
}
func (c *Calendar) Toggle(day string) {
	i := slices.Index(c.Dates, day)
	if i >= 0 {
		c.Dates = slices.Delete(c.Dates, i, i+1)
	} else {
		c.Dates = append(c.Dates, day)
	}
	slices.Sort(c.Dates)
}
func (c Calendar) View(z *zone.Manager, hover, focus string, canApply bool) string {
	var b strings.Builder
	b.WriteString(Button(z, "cal-prev", "‹", hover, focus, false) + " " + c.Month.Format("January 2006") + " " + Button(z, "cal-next", "›", hover, focus, false) + "\n\n Mo   Tu   We   Th   Fr   Sa   Su\n")
	first := (int(c.Month.Weekday()) + 6) % 7
	days := c.Month.AddDate(0, 1, -1).Day()
	for range first {
		b.WriteString("     ")
	}
	for d := 1; d <= days; d++ {
		date := c.Month.AddDate(0, 0, d-1).Format(time.DateOnly)
		id := "date-" + date
		f := focus
		if date == c.Cursor.Format(time.DateOnly) {
			f = id
		}
		b.WriteString(Button(z, id, fmt.Sprintf("%2d", d), hover, f, slices.Contains(c.Dates, date)) + " ")
		if (first+d)%7 == 0 {
			b.WriteByte('\n')
		}
	}
	apply, clear := DisabledButton("Apply"), DisabledButton("Clear")
	if canApply {
		apply = Button(z, "cal-apply", "Apply", hover, focus, false)
	}
	if len(c.Dates) > 0 {
		clear = Button(z, "cal-clear", "Clear", hover, focus, false)
	}
	b.WriteString("\n\n" + apply + clear + Button(z, "cal-close", "Cancel", hover, focus, false) + "\n\nArrows move, Space selects, Enter applies, Esc cancels\n")
	b.WriteString(fmt.Sprintf("%d days selected", len(c.Dates)))
	return b.String()
}
