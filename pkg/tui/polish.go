package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ellypaws/unpackage/pkg/clipboard"
	"github.com/ellypaws/unpackage/pkg/components"
)

const (
	noticeHold    = 6 * time.Second
	noticeFade    = 1200 * time.Millisecond
	arrivalGlow   = 2500 * time.Millisecond
	standingSweep = 800 * time.Millisecond
)

// animating reports whether a short-lived transition needs frames faster than the idle tick.
func (m *Model) animating(now time.Time) bool {
	if m.Notice != "" && !persistentNotice(noticeKind(m.Notice)) {
		if age := now.Sub(m.NoticeAt); age >= noticeHold && age < noticeHold+noticeFade {
			return true
		}
	}
	if now.Sub(m.SafetyStandingAt) < standingSweep {
		return true
	}
	for _, at := range m.SafetyArrivals {
		if now.Sub(at) < arrivalGlow {
			return true
		}
	}
	return false
}

func noticeKind(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "error") || strings.Contains(lower, "cannot") || strings.Contains(lower, "has no ") || strings.Contains(lower, "exceeds") || strings.Contains(lower, "failed"):
		return "error"
	case strings.HasPrefix(lower, "stopping") || strings.HasPrefix(lower, "choose ") || strings.HasPrefix(lower, "no new") || strings.HasPrefix(lower, "no exact"):
		return "warn"
	case strings.HasPrefix(lower, "applied ") || strings.HasPrefix(lower, "saved ") || strings.HasPrefix(lower, "copied ") || strings.HasPrefix(lower, "cleared ") || strings.HasPrefix(lower, "safety hub updated") || strings.Contains(lower, " new violation") || strings.Contains(lower, " new notice"):
		return "success"
	}
	return "info"
}

// persistentNotice keeps errors and warnings on screen until the next notice replaces them.
func persistentNotice(kind string) bool { return kind == "error" || kind == "warn" }

// styledNotice adds a glyph so the outcome reads without color, and fades transient notices once they have been read.
func (m *Model) styledNotice(text string, width int) string {
	kind := noticeKind(text)
	color := map[string]lipgloss.Color{"error": components.Deleted, "warn": components.GroupDM, "success": components.Green, "info": components.Accent}[kind]
	prefix := map[string]string{"error": "✕ ", "success": "✓ "}[kind]
	if !persistentNotice(kind) && !m.NoticeAt.IsZero() {
		age := time.Since(m.NoticeAt)
		if age >= noticeHold+noticeFade {
			return ""
		}
		if age > noticeHold {
			color = components.Blend(color, components.Surface, float64(age-noticeHold)/float64(noticeFade))
		}
	}
	style := lipgloss.NewStyle().Foreground(color)
	if kind == "error" || kind == "success" {
		style = style.Bold(true)
	}
	return style.Render(components.Fit(prefix+text, width))
}

type hint struct{ key, label string }

// keyHints lists the keys that act on whatever currently has focus.
func (m *Model) keyHints() []hint {
	input := slices.Contains([]string{"days-input", "search-input", "servers-input", "request-path", "margin-before", "margin-after"}, m.Focus)
	switch {
	case m.Menu != nil:
		return []hint{{"↑↓", "choose"}, {"Enter", "apply"}, {"Esc", "close"}}
	case m.Detail != nil:
		return []hint{{"PgUp/PgDn", "scroll"}, {"Esc", "back"}, {"Ctrl+Tab", "tabs"}}
	case input && m.Focus == "search-input":
		return []hint{{"Enter", "search"}, {"↑↓", "suggestions"}, {"Tab", "next"}}
	case input:
		return []hint{{"Enter", "apply"}, {"Tab", "next"}, {"Esc", "leave"}}
	}
	switch m.Tab {
	case tabInvestigate:
		if _, ok := messageIndex(m.Focus); ok {
			return []hint{{"Enter", "details"}, {"↑↓", "move"}, {"PgUp/PgDn", "page"}}
		}
		return []hint{{"Tab", "move"}, {"Ctrl+O/N", "packages"}, {"Ctrl+Tab", "tabs"}, {"F1", "help"}}
	case tabViolations:
		if m.guideVisible() {
			return []hint{{"PgUp/PgDn", "scroll"}, {"Tab", "browsers"}, {"Ctrl+Tab", "tabs"}}
		}
		if m.SafetyDetail && !m.wideViolations() {
			return []hint{{"PgUp/PgDn", "scroll"}, {"↑↓", "next violation"}, {"Esc", "list"}}
		}
		return []hint{{"↑↓", "select"}, {"Enter", "open"}, {"PgUp/PgDn", "details"}, {"Ctrl+Tab", "tabs"}}
	case tabStats:
		if m.Focus == "heat" {
			return []hint{{"←→↑↓", "cells"}, {"Tab", "move"}}
		}
		return []hint{{"Tab", "move"}, {"↑↓", "scroll"}, {"Ctrl+Tab", "tabs"}}
	case tabConsole:
		return []hint{{"Tab", "complete"}, {"↑↓", "history"}, {"PgUp/PgDn", "scroll"}}
	case tabLog:
		return []hint{{"PgUp/PgDn", "scroll"}, {"Ctrl+Tab", "tabs"}}
	}
	return nil
}

// footer puts the status on the left and as many key hints as fit on the right.
func (m *Model) footer(width int) string {
	status := m.statusLine(width)
	key := lipgloss.NewStyle().Foreground(components.Text)
	label := lipgloss.NewStyle().Foreground(components.Muted)
	hints := m.keyHints()
	for len(hints) > 0 {
		var parts []string
		for _, h := range hints {
			parts = append(parts, key.Render(h.key)+" "+label.Render(h.label))
		}
		text := strings.Join(parts, "   ")
		gap := width - lipgloss.Width(status) - lipgloss.Width(text)
		if gap >= 3 {
			return status + strings.Repeat(" ", gap) + text
		}
		hints = hints[:len(hints)-1]
	}
	return status
}

// copyText writes to the clipboard off the UI goroutine and reports the outcome as a notice.
func (m *Model) copyText(text, label string) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return copiedMsg{Label: label, Err: clipboard.Write(ctx, text)}
	}
}

// milestone is one dated point on a violation timeline.
type milestone struct {
	label, date string
	at          time.Time
	done        bool
	optional    bool
	color       lipgloss.Color
}

// timeline draws milestones evenly along a track: filled diamonds have happened, hollow ones are ahead.
func timeline(marks []milestone, width int, base lipgloss.Color) string {
	span := func(mk milestone) int { return max(lipgloss.Width(mk.label), lipgloss.Width(mk.date)) }
	fits := func() bool {
		spacing := (width - 1) / max(1, len(marks)-1)
		for i := range len(marks) - 2 {
			if span(marks[i]) >= spacing {
				return false
			}
		}
		return span(marks[len(marks)-2])+1+span(marks[len(marks)-1]) <= spacing+1
	}
	for len(marks) > 2 && !fits() {
		i := slices.IndexFunc(marks[1:len(marks)-1], func(mk milestone) bool { return mk.optional })
		if i < 0 {
			marks = slices.Delete(marks, len(marks)-2, len(marks)-1)
			continue
		}
		marks = slices.Delete(marks, i+1, i+2)
	}
	if len(marks) < 2 || width < 24 {
		return ""
	}
	positions := make([]int, len(marks))
	for i := range marks {
		positions[i] = i * (width - 1) / (len(marks) - 1)
	}
	plain := os.Getenv("NO_COLOR") != ""
	paint := func(color lipgloss.Color, text string, bold bool) string {
		if plain {
			return text
		}
		return lipgloss.NewStyle().Foreground(color).Bold(bold).Render(text)
	}
	var track strings.Builder
	segment := 0
	for x := range width {
		if segment < len(marks) && x == positions[segment] {
			glyph, color := "◇", components.Muted
			if marks[segment].done {
				glyph, color = "◆", marks[segment].color
			}
			track.WriteString(paint(color, glyph, marks[segment].done))
			segment++
			continue
		}
		next := marks[min(segment, len(marks)-1)]
		if next.done {
			track.WriteString(paint(components.Blend(base, components.Surface, .35), "━", false))
		} else {
			track.WriteString(paint(components.Border, "─", false))
		}
	}
	row := func(text func(milestone) string, style func(milestone) lipgloss.Style) string {
		var b strings.Builder
		used := 0
		for i, mk := range marks {
			last := i == len(marks)-1
			room := width - used - 1
			if !last {
				room = positions[i+1] - positions[i] - 1
			}
			if i == len(marks)-2 {
				room -= max(0, lipgloss.Width(text(marks[i+1]))-1)
			}
			value := components.Fit(text(mk), max(1, room))
			start := positions[i]
			if last {
				start = max(used+1, width-lipgloss.Width(value))
			}
			b.WriteString(strings.Repeat(" ", max(0, start-used)))
			b.WriteString(style(mk).Render(value))
			used = max(used, start) + lipgloss.Width(value)
		}
		return b.String()
	}
	labels := row(func(mk milestone) string { return mk.label }, func(mk milestone) lipgloss.Style {
		if mk.done {
			return lipgloss.NewStyle().Foreground(components.Text).Bold(true)
		}
		return lipgloss.NewStyle().Foreground(components.Muted)
	})
	dates := row(func(mk milestone) string { return mk.date }, func(milestone) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(components.Muted)
	})
	return track.String() + "\n" + labels + "\n" + dates
}
