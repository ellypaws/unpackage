package components

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ellypaws/unpackage/pkg/session"
)

func Highlight(text, query string, style lipgloss.Style) string {
	text = session.Safe(text)
	if query == "" {
		return style.Render(text)
	}
	matches := regexp.MustCompile("(?i)"+regexp.QuoteMeta(query)).FindAllStringIndex(text, -1)
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
	if query != "" {
		match := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query)).FindStringIndex(text)
		if match != nil && lipgloss.Width(text[:match[0]]) > width/2 {
			prefix := []rune(text[:match[0]])
			text = "…" + string(prefix[max(0, len(prefix)-width/3):]) + text[match[0]:]
		}
	}
	return Fit(text, width)
}
