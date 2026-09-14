package components

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"

	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

var highlights struct {
	sync.Mutex
	query   string
	pattern *regexp.Regexp
}

func highlightPattern(query string) *regexp.Regexp {
	highlights.Lock()
	defer highlights.Unlock()
	if highlights.query == query {
		return highlights.pattern
	}
	highlights.query = query
	highlights.pattern = nil
	q, err := store.ParseSearch(query)
	if err != nil {
		return nil
	}
	var patterns []string
	for _, t := range q.Tokens {
		if t.Exclude {
			continue
		}
		switch t.Key {
		case "":
			patterns = append(patterns, "(?i:"+regexp.QuoteMeta(t.Value)+")")
		case "regex":
			patterns = append(patterns, "(?:"+t.Value+")")
		}
	}
	if len(patterns) > 0 {
		highlights.pattern, _ = regexp.Compile(strings.Join(patterns, "|"))
	}
	return highlights.pattern
}

func Highlight(text, query string, style lipgloss.Style) string {
	text = session.Safe(text)
	pattern := highlightPattern(query)
	if pattern == nil {
		return style.Render(text)
	}
	matches := pattern.FindAllStringIndex(text, -1)
	mark := style.Foreground(lipgloss.Color("#191720")).Background(lipgloss.Color("#E8BE79")).Bold(true)
	var out strings.Builder
	start := 0
	for _, match := range matches {
		out.WriteString(style.Render(text[start:match[0]]))
		out.WriteString(mark.Render(text[match[0]:match[1]]))
		start = match[1]
	}
	out.WriteString(style.Render(text[start:]))
	return out.String()
}

func SearchExcerpt(text, query string, width int) string {
	text = session.Safe(text)
	if pattern := highlightPattern(query); pattern != nil {
		match := pattern.FindStringIndex(text)
		if match != nil && lipgloss.Width(text[:match[0]]) > width/2 {
			prefix := []rune(text[:match[0]])
			text = "…" + string(prefix[max(0, len(prefix)-width/3):]) + text[match[0]:]
		}
	}
	return Fit(text, width)
}
