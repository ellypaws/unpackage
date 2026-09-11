package components

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/ellypaws/unpackage/pkg/session"
)

var Accent = lipgloss.Color("#B59AF6")
var Muted = lipgloss.Color("#A4A6B5")
var Warn = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8BE79"))
var Deleted = lipgloss.Color("#F7768E")
var Title = lipgloss.NewStyle().Bold(true).Foreground(Accent)
var Border = lipgloss.Color("#57516D")
var Surface = lipgloss.Color("#252331")
var Text = lipgloss.Color("#E1DDEB")

func Fit(s string, w int) string { return ansi.Truncate(session.Safe(s), max(1, w), "…") }
func Button(z *zone.Manager, id, label, hover, focus string, active bool) string {
	st := lipgloss.NewStyle().Foreground(Text).Background(Surface).Padding(0, 1)
	prefix := ""
	if active {
		st = st.Foreground(Accent).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			prefix = "*"
		}
	}
	if id == hover || id == focus {
		st = st.Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#37445E")).Underline(true).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			prefix = ">"
		}
	}
	return z.Mark(id, st.Render(prefix+label))
}

func Tab(z *zone.Manager, id, label, hover, focus string, active bool, padding int) string {
	b := lipgloss.Border{Top: "─", Bottom: "─", Left: "│", Right: "│", TopLeft: "╭", TopRight: "╮", BottomLeft: "┴", BottomRight: "┴"}
	style := lipgloss.NewStyle().Border(b).BorderForeground(Border).Foreground(Muted).Padding(0, padding)
	if active {
		b.Bottom = " "
		b.BottomLeft = "┘"
		b.BottomRight = "└"
		style = style.Border(b).Foreground(Accent).Bold(true)
	}
	if hover == id || focus == id {
		style = style.Foreground(lipgloss.Color("#FFB3E3")).BorderForeground(Accent).Underline(true).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			label = ">" + label
		}
	}
	return z.Mark(id, style.Render(label))
}
func Rule(frac float64, w int) string {
	w = max(0, w)
	n := float64(w) * max(0, min(1, frac))
	var b strings.Builder
	for i := 0; i < w; i++ {
		glyph := "─"
		color := "#454957"
		if float64(i) < n {
			glyph = "━"
			h := float64(i)/float64(max(1, w))*150 + 190
			color = hsv(h)
		}
		if os.Getenv("NO_COLOR") != "" {
			b.WriteString(glyph)
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph))
		}
	}
	return b.String()
}
func hsv(h float64) string {
	c := 0.58
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return fmt.Sprintf("#%02x%02x%02x", int((r+.32)*255), int((g+.32)*255), int((b+.32)*255))
}
func Spark(ns []int) string {
	peak := 1
	for _, n := range ns {
		peak = max(peak, n)
	}
	var b strings.Builder
	glyphs := []rune("▁▂▃▄▅▆▇█")
	for _, n := range ns {
		b.WriteRune(glyphs[min(7, n*7/peak)])
	}
	return b.String()
}
