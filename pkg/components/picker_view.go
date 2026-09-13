package components

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/ellypaws/unpackage/pkg/session"
)

func (p *Picker) Resize(width, height int) tea.Cmd {
	p.Height = height
	y := 0
	if len(p.bounds) > 0 {
		y = p.bounds[0].Y
	}
	p.bounds = p.columnWidths(width - 6)
	for i := range p.bounds {
		p.bounds[i].Y = y
	}
	return p.CountVisible()
}

func (p *Picker) button(z *zone.Manager, id, label, hover, focus string, active bool) string {
	if !p.Enabled(id) {
		return DisabledButton(label)
	}
	p.Actions = append(p.Actions, id)
	return Button(z, id, label, hover, focus, active)
}

func (p Picker) columnWidths(width int) []columnBounds {
	n := len(p.Columns)
	if n == 0 {
		return nil
	}
	first := max(0, n-max(1, (width-22)/3+1))
	active := n - 1
	if p.expanded >= first && p.expanded < n {
		active = p.expanded
	}
	count := n - first
	sizes := make([]int, count)
	for i := range sizes {
		sizes[i] = 3
	}
	remaining := width - count*3
	for _, col := range []int{active, n - 1} {
		add := min(remaining, 30-sizes[col-first])
		sizes[col-first] += add
		remaining -= add
	}
	for remaining > 0 {
		for i := len(sizes) - 1; i >= 0 && remaining > 0; i-- {
			sizes[i]++
			remaining--
		}
	}
	var bounds []columnBounds
	x := 3
	for i, size := range sizes {
		bounds = append(bounds, columnBounds{Index: first + i, X: x, Width: size})
		x += size
	}
	return bounds
}

// Coordinates are relative to the picker frame, independent of buffered zone scans.
func (p *Picker) Mouse(msg tea.MouseMsg) (string, tea.Cmd) {
	for _, bound := range p.bounds {
		if msg.X < bound.X || msg.X >= bound.X+bound.Width || msg.Y < bound.Y || msg.Y >= bound.Y+p.Height+1 {
			continue
		}
		col := bound.Index
		id := fmt.Sprintf("pick-column-%d", col)
		if bound.Width >= 16 && msg.Y > bound.Y {
			indices := p.Columns[col].Matches
			row := p.Columns[col].Offset + msg.Y - bound.Y - 1
			if row < len(indices) {
				id = fmt.Sprintf("pick-entry-%d-%d", col, indices[row])
			}
		}
		p.expanded = col
		width := 0
		for _, b := range p.bounds {
			width += b.Width
		}
		p.bounds = p.columnWidths(width)
		for i := range p.bounds {
			p.bounds[i].Y = bound.Y
		}
		if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
			step := 3
			if msg.Button == tea.MouseButtonWheelUp {
				step = -3
			}
			p.Columns[col].Offset = max(0, min(max(0, len(p.Columns[col].Matches)-p.Height), p.Columns[col].Offset+step))
		}
		return id, p.CountVisible()
	}
	return "", nil
}

func (p *Picker) View(z *zone.Manager, width int, hover, focus string, frame int) string {
	inner := width - 6
	p.Actions = nil
	var b strings.Builder
	busy := p.Loading || len(p.pending) > 0
	b.WriteByte('\n')
	for _, v := range []struct{ id, label string }{{"pick-back", "‹ Back"}, {"pick-forward", "Forward ›"}, {"pick-up", "Up"}, {"pick-home", "Home"}} {
		b.WriteString(p.button(z, v.id, v.label, hover, focus, false) + " ")
	}
	b.WriteString("\n\n")
	action := p.button(z, "pick-open", "→", hover, focus, false)
	p.Actions = append(p.Actions, "pick-input")
	b.WriteString(InputField(z, "pick-input", &p.Path, inner, hover, focus, action, p.hint) + "\n")
	status := ""
	if p.Loading {
		status = Working("Reading folders and packages…", frame, inner)
	} else if len(p.pending) > 0 {
		status = Working(fmt.Sprintf("Inspecting %d folders…", len(p.pending)), frame, inner)
	} else if p.Err != "" {
		status = lipgloss.NewStyle().Foreground(Deleted).Render(Fit(p.Err, inner))
	}
	b.WriteString(status + "\n")
	p.bounds = p.columnWidths(inner)
	var columns []string
	for i := range p.bounds {
		bound := &p.bounds[i]
		bound.Y = 1 + strings.Count(b.String(), "\n")
		columns = append(columns, p.columnView(z, *bound, hover, focus, frame))
	}
	if len(columns) == 0 {
		b.WriteString(strings.Repeat("\n", p.Height))
	} else {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, columns...))
	}
	name := filepath.Base(p.selected)
	if p.selected == "" {
		name = filepath.Base(p.Dir)
	}
	label := "Choose this package (" + Fit(name, max(1, inner-35)) + ")"
	b.WriteString("\n\n" + p.button(z, "pick-use", label, hover, focus, true) + " " + p.button(z, "pick-close", "Cancel", hover, focus, false) + "\n")
	return TitledBox("Choose a package", b.String(), width, 2, lipgloss.RoundedBorder(), GradientColor(.46), "", frame, busy)
}

func (p *Picker) columnView(z *zone.Manager, bound columnBounds, hover, focus string, frame int) string {
	col, width := bound.Index, bound.Width
	column := p.Columns[col]
	last := col == len(p.Columns)-1
	color := Text
	if !last && col != p.expanded {
		age := min(5, len(p.Columns)-1-col)
		color = Fade(Text, age)
	}
	style := lipgloss.NewStyle().Foreground(color)
	name := filepath.Base(column.Path)
	if filepath.Dir(column.Path) == column.Path {
		name = column.Path
	}
	name = session.Safe(name)
	columnID := fmt.Sprintf("pick-column-%d", col)
	p.Actions = append(p.Actions, columnID)
	headerText := Fit(name, width-2)
	if width < 16 {
		headerText = ansi.Truncate(name, width-1, "")
	}
	header := style.Bold(true).Render(headerText)
	lines := []string{z.Mark(columnID, lipgloss.NewStyle().Width(width-1).Render(header))}
	indices := column.Matches
	for row := range p.Height {
		index := column.Offset + row
		line := ""
		if index < len(indices) {
			entryIndex := indices[index]
			entry := column.Entries[entryIndex]
			selected := last && equalPath(filepath.Join(column.Path, entry.Name), p.selected)
			if !last {
				selected = equalPath(filepath.Join(column.Path, entry.Name), p.Columns[col+1].Path)
			}
			id := fmt.Sprintf("pick-entry-%d-%d", col, entryIndex)
			rowStyle := style
			if selected {
				rowStyle = rowStyle.Foreground(Accent).Bold(true).Background(Surface)
			}
			if hover == id || focus == id {
				rowStyle = rowStyle.Foreground(Brightness(Saturation(GradientColor(.64), .04), .08)).Background(SurfaceHover).Underline(true)
			}
			meta := ""
			metaStyle := MutedStyle
			if entry.Dir {
				path := filepath.Join(column.Path, entry.Name)
				if p.pending[path] {
					meta = Spinner(frame)
					metaStyle = lipgloss.NewStyle()
				} else if p.packages[path] {
					meta = "Package"
					metaStyle = lipgloss.NewStyle().Foreground(Accent).Bold(true)
				} else if count, ok := p.counts[path]; ok && count > 0 {
					meta = fmt.Sprintf("+%d", count)
				}
			} else {
				meta = fmt.Sprintf("%.1f MB", float64(entry.Size)/1e6)
			}
			label := session.Safe(entry.Name)
			if entry.Dir {
				label += "/"
			}
			if lipgloss.Width(label)+lipgloss.Width(meta)+3 > width {
				meta = ""
			}
			if width < 16 {
				label = ansi.Truncate(label, width-1, "")
			} else {
				label = Fit(label, width-2)
			}
			query := ""
			if last {
				query = p.query
			}
			line = fuzzyHighlight(label, query, rowStyle)
			gap := max(0, width-1-lipgloss.Width(line)-lipgloss.Width(meta))
			line += rowStyle.Render(strings.Repeat(" ", gap)) + metaStyle.Render(meta)
			if width >= 16 {
				p.Actions = append(p.Actions, id)
				line = z.Mark(id, line)
			}
		} else if row == 0 && width >= 16 {
			label := "No folders or ZIPs"
			if p.query != "" && last {
				label = "No matches"
			}
			line = style.Render(Fit(label, width-2))
		}
		lines = append(lines, lipgloss.NewStyle().Width(width-1).Render(line))
	}
	position := float64(col) / float64(max(1, len(p.Columns)-1))
	border := Brightness(Saturation(GradientColor(position), -.14), -.44)
	return lipgloss.NewStyle().BorderRight(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).Render(strings.Join(lines, "\n"))
}

func fuzzyHighlight(label, query string, style lipgloss.Style) string {
	positions, _ := fuzzyMatch(label, query)
	mark := style.Foreground(lipgloss.Color("#191720")).Background(lipgloss.Color("#E8BE79")).Bold(true)
	var b strings.Builder
	for i, r := range []rune(label) {
		if slices.Contains(positions, i) {
			b.WriteString(mark.Render(string(r)))
		} else {
			b.WriteString(style.Render(string(r)))
		}
	}
	return ansi.Truncate(b.String(), lipgloss.Width(label), "")
}
