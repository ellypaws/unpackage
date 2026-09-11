package components

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

type Entry struct {
	Name string
	Dir  bool
	Size int64
}
type DirectoryMsg struct {
	Path       string
	Entries    []Entry
	Err        error
	Generation int
}
type PickedMsg struct{ Path string }
type CompletionMsg struct {
	Value      string
	Candidates []string
	Err        error
}
type Picker struct {
	Path                               textinput.Model
	Dir                                string
	Entries                            []Entry
	Cursor, Offset, Height, Generation int
	Err                                string
	Loading                            bool
	history                            []string
	future                             []string
}

func NewPicker(start string) Picker {
	t := textinput.New()
	t.Prompt = "Path › "
	t.CharLimit = 4096
	t.SetValue(start)
	t.Focus()
	return Picker{Path: t, Dir: start, Height: 12}
}
func CleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "& ")
	p = strings.Trim(p, "\"'")
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if h, e := os.UserHomeDir(); e == nil {
			p = filepath.Join(h, strings.TrimLeft(p[1:], "/\\"))
		}
	}
	return p
}
func (p *Picker) Navigate(dest string) tea.Cmd {
	dest = CleanPath(dest)
	p.Generation++
	gen := p.Generation
	p.Loading = true
	p.Err = ""
	return func() tea.Msg {
		abs, e := filepath.Abs(dest)
		if e != nil {
			return DirectoryMsg{Generation: gen, Err: e}
		}
		st, e := os.Stat(abs)
		if e != nil {
			return DirectoryMsg{Generation: gen, Err: e}
		}
		if !st.IsDir() {
			if strings.EqualFold(filepath.Ext(abs), ".zip") {
				return PickedMsg{abs}
			}
			return DirectoryMsg{Generation: gen, Err: fmt.Errorf("choose a folder or ZIP")}
		}
		es, e := os.ReadDir(abs)
		var out []Entry
		for _, v := range es {
			if v.Type()&os.ModeSymlink != 0 {
				continue
			}
			if v.IsDir() || strings.EqualFold(filepath.Ext(v.Name()), ".zip") {
				size := int64(0)
				if info, er := v.Info(); er == nil {
					size = info.Size()
				}
				out = append(out, Entry{v.Name(), v.IsDir(), size})
			}
		}
		slices.SortFunc(out, func(a, b Entry) int {
			if a.Dir != b.Dir {
				if a.Dir {
					return -1
				}
				return 1
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		return DirectoryMsg{Path: abs, Entries: out, Err: e, Generation: gen}
	}
}
func (p *Picker) Apply(m DirectoryMsg) {
	if m.Generation != p.Generation {
		return
	}
	p.Loading = false
	if m.Err != nil {
		p.Err = m.Err.Error()
		return
	}
	if p.Dir != m.Path && p.Dir != "" {
		p.history = append(p.history, p.Dir)
	}
	p.Dir = m.Path
	p.Path.SetValue(m.Path)
	p.Entries = m.Entries
	p.Cursor = 0
	p.Offset = 0
}
func (p *Picker) Action(id string) tea.Cmd {
	switch id {
	case "pick-up":
		return p.Navigate(filepath.Dir(p.Dir))
	case "pick-home":
		h, _ := os.UserHomeDir()
		return p.Navigate(h)
	case "pick-open":
		return p.Navigate(p.Path.Value())
	case "pick-use":
		dest := p.Dir
		return func() tea.Msg { return PickedMsg{dest} }
	case "pick-back":
		if len(p.history) > 0 {
			v := p.history[len(p.history)-1]
			p.history = p.history[:len(p.history)-1]
			p.future = append(p.future, p.Dir)
			p.Dir = ""
			return p.Navigate(v)
		}
	case "pick-forward":
		if len(p.future) > 0 {
			v := p.future[len(p.future)-1]
			p.future = p.future[:len(p.future)-1]
			return p.Navigate(v)
		}
	}
	var n int
	if _, e := fmt.Sscanf(id, "entry-%d", &n); e == nil && n >= 0 && n < len(p.Entries) {
		return p.Navigate(filepath.Join(p.Dir, p.Entries[n].Name))
	}
	return nil
}
func (p *Picker) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up":
			p.Cursor = max(0, p.Cursor-1)
			p.Offset = min(p.Offset, p.Cursor)
			return nil
		case "down":
			p.Cursor = min(max(0, len(p.Entries)-1), p.Cursor+1)
			p.Offset = max(p.Offset, p.Cursor-p.Height+1)
			return nil
		case "alt+up":
			return p.Action("pick-up")
		case "ctrl+o":
			return p.Action("pick-use")
		case "enter":
			return p.Navigate(p.Path.Value())
		case "right":
			if len(p.Entries) > 0 {
				return p.Action(fmt.Sprintf("entry-%d", p.Cursor))
			}
		case "tab":
			v := CleanPath(p.Path.Value())
			return func() tea.Msg {
				parent := filepath.Dir(v)
				prefix := filepath.Base(v)
				if strings.HasSuffix(v, string(filepath.Separator)) {
					parent = v
					prefix = ""
				}
				es, e := os.ReadDir(parent)
				var matches []string
				for _, entry := range es {
					if strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(prefix)) && (entry.IsDir() || strings.EqualFold(filepath.Ext(entry.Name()), ".zip")) {
						matches = append(matches, filepath.Join(parent, entry.Name()))
					}
				}
				return CompletionMsg{v, matches, e}
			}
		case "alt+left":
			return p.Action("pick-back")
		case "alt+right":
			return p.Action("pick-forward")
		}
	}
	var cmd tea.Cmd
	p.Path, cmd = p.Path.Update(msg)
	return cmd
}
func (p Picker) View(z *zone.Manager, w int, hover, focus string) string {
	inner := w - 6
	var b strings.Builder
	b.WriteString(Title.Render("Choose a package") + "\n\n")
	for _, v := range []struct{ id, label string }{{"pick-back", "‹ Back"}, {"pick-forward", "Forward ›"}, {"pick-up", "Up"}, {"pick-home", "Home"}, {"pick-open", "Go"}} {
		b.WriteString(Button(z, v.id, v.label, hover, focus, false) + " ")
	}
	b.WriteString("\n\n")
	input := p.Path
	input.Width = max(10, inner-4)
	input.Prompt = ""
	inputStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Border).Padding(0, 1).Width(inner - 2)
	if hover == "pick-input" || focus == "pick-input" {
		inputStyle = inputStyle.BorderForeground(Accent)
	}
	b.WriteString(z.Mark("pick-input", inputStyle.Render(input.View())) + "\n\n")
	if p.Loading {
		b.WriteString("Loading…\n")
	} else if p.Err != "" {
		b.WriteString(Warn.Render(Fit(p.Err, inner)) + "\n")
	}
	for i := p.Offset; i < min(len(p.Entries), p.Offset+p.Height); i++ {
		v := p.Entries[i]
		suffix := "/"
		if !v.Dir {
			suffix = fmt.Sprintf("  %.1f MB", float64(v.Size)/1e6)
		}
		label := Fit(v.Name+suffix, inner-2)
		b.WriteString(Button(z, fmt.Sprintf("entry-%d", i), label, hover, focus, i == p.Cursor) + "\n")
	}
	if len(p.Entries) == 0 && !p.Loading {
		b.WriteString("No folders or ZIPs\n")
	}
	b.WriteString("\n" + Button(z, "pick-use", "Open folder", hover, focus, true) + "  " + Button(z, "pick-close", "Cancel", hover, focus, false))
	return lipgloss.NewStyle().Width(w-2).Border(lipgloss.RoundedBorder()).BorderForeground(Border).Padding(1, 2).Render(b.String())
}
