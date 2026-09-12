package components

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type Entry struct {
	Name string
	Dir  bool
	Size int64
}

type Column struct {
	Path    string
	Entries []Entry
	Matches []int
	Offset  int
}

type DirectoryMsg struct {
	Columns                []Column
	Query, Selected, Input string
	Err                    error
	Generation             uint64
}

type DirectoryCountMsg struct {
	Path       string
	Count      int
	Package    bool
	Err        error
	Generation uint64
}

type PickedMsg struct {
	Path       string
	Generation uint64
}

type pickerClickMsg struct {
	ID                        string
	Generation, ClickRevision uint64
}

type columnBounds struct{ Index, X, Y, Width int }

type Picker struct {
	Path            textinput.Model
	Dir             string
	Columns         []Column
	Cursor, Height  int
	Generation      uint64
	Err             string
	Loading         bool
	Actions         []string
	query, selected string
	hint            string
	pendingAction   string
	expanded        int
	bounds          []columnBounds
	history, future []string
	historyMove     bool
	parent          context.Context
	ctx             context.Context
	cancel          context.CancelFunc
	counts          map[string]int
	packages        map[string]bool
	pending         map[string]bool
	clickID         string
	clickAt         time.Time
	clickRevision   uint64
}

var pickerGeneration atomic.Uint64

const doubleClickInterval = 450 * time.Millisecond

func NewPicker(ctx context.Context, start string) Picker {
	t := textinput.New()
	t.Prompt = ""
	t.CharLimit = 4096
	t.PlaceholderStyle = MutedStyle
	t.SetValue(start)
	t.CursorEnd()
	t.Focus()
	return Picker{Path: t, Height: 12, expanded: -1, parent: ctx, counts: map[string]int{}, packages: map[string]bool{}, pending: map[string]bool{}}
}

func CleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "& ")
	p = strings.Trim(p, "\"'")
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if h, e := os.UserHomeDir(); e == nil {
			p = h + p[1:]
		}
	}
	return p
}

func directoryPath(path string) string {
	if path != "" && !os.IsPathSeparator(path[len(path)-1]) {
		return path + string(filepath.Separator)
	}
	return path
}

func (p *Picker) Close() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p Picker) Busy() bool {
	return p.Loading || len(p.pending) > 0
}

func (p *Picker) Navigate(dest string) tea.Cmd {
	dest = CleanPath(dest)
	if !strings.EqualFold(filepath.Ext(dest), ".zip") {
		dest = directoryPath(dest)
	}
	p.Path.SetValue(dest)
	p.Path.CursorEnd()
	return p.resolve(true)
}

func (p *Picker) resolve(navigate bool) tea.Cmd {
	p.Close()
	p.ctx, p.cancel = context.WithCancel(p.parent)
	p.Generation = pickerGeneration.Add(1)
	p.pending = map[string]bool{}
	p.Loading = true
	p.Err = ""
	p.selected = ""
	p.hint = ""
	p.pendingAction = ""
	p.clickID = ""
	p.clickRevision++
	input, gen, ctx := p.Path.Value(), p.Generation, p.ctx
	cache := make(map[string][]Entry, len(p.Columns))
	for _, col := range p.Columns {
		cache[col.Path] = col.Entries
	}
	return func() tea.Msg { return resolveDirectory(ctx, input, gen, cache, navigate) }
}

func readEntries(ctx context.Context, path string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.ReadDir(128)
		for _, entry := range batch {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				entries = append(entries, Entry{Name: entry.Name(), Dir: true})
			} else if strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
				v := Entry{Name: entry.Name()}
				if info, e := entry.Info(); e == nil {
					v.Size = info.Size()
				}
				entries = append(entries, v)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries, nil
}

func resolveDirectory(ctx context.Context, input string, gen uint64, cache map[string][]Entry, navigate bool) (m DirectoryMsg) {
	m = DirectoryMsg{Input: input, Generation: gen}
	defer func() {
		for i := range m.Columns {
			query := ""
			if i == len(m.Columns)-1 {
				query = m.Query
			}
			m.Columns[i].Matches = matchingEntries(m.Columns[i].Entries, query, false)
		}
	}()
	value := CleanPath(input)
	if value == "" {
		return m
	}
	trailing := os.IsPathSeparator(value[len(value)-1])
	abs, err := filepath.Abs(value)
	if err != nil {
		m.Err = err
		return m
	}
	root := filepath.VolumeName(abs) + string(filepath.Separator)
	parts := strings.FieldsFunc(strings.TrimPrefix(abs, root), func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) })
	path := root
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			m.Err = err
			return m
		}
		entries, ok := cache[path]
		if !ok {
			entries, err = readEntries(ctx, path)
		}
		if err != nil {
			m.Err = err
			return m
		}
		m.Columns = append(m.Columns, Column{Path: path, Entries: entries})
		if i == len(parts) {
			m.Selected = path
			return m
		}
		if i == len(parts)-1 && !trailing {
			m.Query = parts[i]
			for _, entry := range entries {
				if equalPath(entry.Name, parts[i]) {
					m.Selected = filepath.Join(path, entry.Name)
					if navigate && entry.Dir {
						path = m.Selected
						m.Query = ""
					}
					break
				}
			}
			if m.Query != "" {
				return m
			}
			continue
		}
		matches := matchingEntries(entries, parts[i], true)
		if len(matches) == 0 {
			m.Query = parts[i]
			m.Err = fmt.Errorf("no directory matches %q", parts[i])
			return m
		}
		path = filepath.Join(path, entries[matches[0]].Name)
	}
}

func equalPath(a, b string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Contiguous matches win over subsequences, with exact names first.
func fuzzyMatch(name, query string) ([]int, int) {
	n, q := []rune(name), []rune(query)
	if len(q) == 0 {
		return nil, 0
	}
	for i := range n {
		n[i] = unicode.ToLower(n[i])
	}
	for i := range q {
		q[i] = unicode.ToLower(q[i])
	}
	for start := 0; start+len(q) <= len(n); start++ {
		if slices.Equal(n[start:start+len(q)], q) {
			positions := make([]int, len(q))
			for i := range q {
				positions[i] = start + i
			}
			return positions, start*2 + len(n) - len(q)
		}
	}
	var positions []int
	for i, r := range n {
		if r == q[len(positions)] {
			positions = append(positions, i)
		}
		if len(positions) == len(q) {
			return positions, 1000 + positions[len(positions)-1] - positions[0] + len(n)
		}
	}
	return nil, -1
}

func matchingEntries(entries []Entry, query string, dirsOnly bool) []int {
	if query == "" {
		indices := make([]int, 0, len(entries))
		for i, entry := range entries {
			if !dirsOnly || entry.Dir {
				indices = append(indices, i)
			}
		}
		return indices
	}
	type match struct{ index, score int }
	var matches []match
	for i, entry := range entries {
		if dirsOnly && !entry.Dir {
			continue
		}
		_, score := fuzzyMatch(entry.Name, query)
		if score >= 0 {
			score++
			if equalPath(entry.Name, query) {
				score = 0
			}
			matches = append(matches, match{i, score})
		}
	}
	slices.SortStableFunc(matches, func(a, b match) int { return a.score - b.score })
	indices := make([]int, len(matches))
	for i, match := range matches {
		indices[i] = match.index
	}
	return indices
}

func (p *Picker) Apply(m DirectoryMsg) tea.Cmd {
	if m.Generation != p.Generation || m.Input != p.Path.Value() {
		return nil
	}
	p.Loading = false
	if m.Err != nil {
		p.Err = m.Err.Error()
	}
	if len(m.Columns) == 0 {
		p.Columns, p.Dir, p.query, p.selected = nil, "", "", ""
		return nil
	}
	dir := m.Columns[len(m.Columns)-1].Path
	if dir != p.Dir {
		if p.Dir != "" && !p.historyMove {
			p.history = append(p.history, p.Dir)
			p.future = nil
		}
		p.expanded = -1
		p.bounds = nil
	}
	p.historyMove = false
	for i := range m.Columns {
		for _, old := range p.Columns {
			if old.Path == m.Columns[i].Path {
				m.Columns[i].Offset = old.Offset
				break
			}
		}
		if i+1 < len(m.Columns) {
			child := filepath.Base(m.Columns[i+1].Path)
			index := slices.IndexFunc(m.Columns[i].Entries, func(e Entry) bool { return e.Name == child })
			m.Columns[i].Offset = max(0, index-p.Height/2)
		}
	}
	p.Dir, p.Columns, p.query, p.selected = dir, m.Columns, m.Query, m.Selected
	p.Cursor = 0
	p.Columns[len(p.Columns)-1].Offset = 0
	p.suggest()
	if p.pendingAction != "" {
		action := p.pendingAction
		p.pendingAction = ""
		if action == "tab" {
			if completion := p.completion(); completion != "" {
				return p.Navigate(completion)
			}
		} else if p.Enabled(action) {
			return p.Action(action)
		}
	}
	return p.CountVisible()
}

func (p Picker) completion() string {
	if p.Loading || p.query == "" || len(p.Columns) == 0 {
		return ""
	}
	col := p.Columns[len(p.Columns)-1]
	matches := col.Matches
	if len(matches) == 0 {
		return ""
	}
	entry := col.Entries[matches[0]]
	path := filepath.Join(col.Path, entry.Name)
	if entry.Dir {
		path = directoryPath(path)
	}
	return path
}

func (p *Picker) suggest() {
	p.hint = ""
	completion := p.completion()
	if completion == "" || completion == p.Path.Value() {
		return
	}
	p.hint = "→ " + filepath.Base(filepath.Clean(completion))
}

func (p Picker) Enabled(id string) bool {
	switch id {
	case "pick-use":
		return !p.Loading && p.Err == "" && p.selected != ""
	case "pick-open":
		return !p.Loading && strings.TrimSpace(p.Path.Value()) != "" && (p.Err != "" || p.Dir == "" || p.query != "" && p.completion() != p.Path.Value())
	case "pick-up":
		return p.Dir != "" && filepath.Dir(p.Dir) != p.Dir
	case "pick-back":
		return len(p.history) > 0
	case "pick-forward":
		return len(p.future) > 0
	}
	return true
}

func (p *Picker) Action(id string) tea.Cmd {
	if !p.Enabled(id) {
		return nil
	}
	switch id {
	case "pick-up":
		return p.Navigate(filepath.Dir(p.Dir))
	case "pick-home":
		h, err := os.UserHomeDir()
		if err != nil {
			p.Err = err.Error()
			return nil
		}
		return p.Navigate(h)
	case "pick-open":
		if completion := p.completion(); completion != "" {
			return p.Navigate(completion)
		}
		return p.Navigate(p.Path.Value())
	case "pick-use":
		dest, gen := p.selected, p.Generation
		return func() tea.Msg { return PickedMsg{Path: dest, Generation: gen} }
	case "pick-back":
		dest := p.history[len(p.history)-1]
		p.history = p.history[:len(p.history)-1]
		p.future = append(p.future, p.Dir)
		p.historyMove = true
		return p.Navigate(dest)
	case "pick-forward":
		dest := p.future[len(p.future)-1]
		p.future = p.future[:len(p.future)-1]
		p.history = append(p.history, p.Dir)
		p.historyMove = true
		return p.Navigate(dest)
	}
	var col, row int
	if _, err := fmt.Sscanf(id, "pick-entry-%d-%d", &col, &row); err == nil && col >= 0 && col < len(p.Columns) && row >= 0 && row < len(p.Columns[col].Entries) {
		return p.Navigate(filepath.Join(p.Columns[col].Path, p.Columns[col].Entries[row].Name))
	}
	if _, err := fmt.Sscanf(id, "pick-column-%d", &col); err == nil && col >= 0 && col < len(p.Columns) {
		p.expanded = col
	}
	return p.CountVisible()
}

func (p *Picker) MouseAction(id string) tea.Cmd {
	var col, row int
	if _, err := fmt.Sscanf(id, "pick-entry-%d-%d", &col, &row); err != nil || col < 0 || col >= len(p.Columns) || row < 0 || row >= len(p.Columns[col].Entries) || !p.Columns[col].Entries[row].Dir {
		return p.Action(id)
	}

	path := filepath.Join(p.Columns[col].Path, p.Columns[col].Entries[row].Name)
	now := time.Now()
	if p.clickID == id && now.Sub(p.clickAt) <= doubleClickInterval {
		p.clickID = ""
		p.clickRevision++
		gen := p.Generation
		return func() tea.Msg { return PickedMsg{Path: path, Generation: gen} }
	}

	p.selected = path
	p.clickID = id
	p.clickAt = now
	p.clickRevision++
	revision, gen := p.clickRevision, p.Generation
	return tea.Tick(doubleClickInterval, func(time.Time) tea.Msg {
		return pickerClickMsg{ID: id, Generation: gen, ClickRevision: revision}
	})
}

func (p *Picker) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(pickerClickMsg); ok {
		if click.Generation != p.Generation || click.ClickRevision != p.clickRevision || click.ID != p.clickID {
			return nil
		}
		p.clickID = ""
		return p.Action(click.ID)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "down":
			if len(p.Columns) == 0 {
				return nil
			}
			col := len(p.Columns) - 1
			step := 1
			if k.String() == "up" {
				step = -1
			}
			p.Cursor = max(0, min(len(p.Columns[col].Matches)-1, p.Cursor+step))
			p.Columns[col].Offset = min(p.Columns[col].Offset, p.Cursor)
			p.Columns[col].Offset = max(p.Columns[col].Offset, p.Cursor-p.Height+1)
			matches := p.Columns[col].Matches
			if len(matches) > 0 {
				p.selected = filepath.Join(p.Columns[col].Path, p.Columns[col].Entries[matches[p.Cursor]].Name)
			}
			return p.CountVisible()
		case "alt+up":
			return p.Action("pick-up")
		case "alt+left":
			return p.Action("pick-back")
		case "alt+right":
			return p.Action("pick-forward")
		case "ctrl+o":
			return p.Action("pick-use")
		case "enter":
			if p.Loading {
				p.pendingAction = "pick-open"
				return nil
			}
			return p.Action("pick-open")
		case "tab":
			if p.Loading {
				p.pendingAction = "tab"
				return nil
			}
			if completion := p.completion(); completion != "" {
				return p.Navigate(completion)
			}
			return nil
		case "alt+down":
			if len(p.Columns) > 0 {
				col := len(p.Columns) - 1
				matches := p.Columns[col].Matches
				if p.Cursor < len(matches) {
					return p.Action(fmt.Sprintf("pick-entry-%d-%d", col, matches[p.Cursor]))
				}
			}
			return nil
		}
		if k.Paste {
			value := CleanPath(string(k.Runes))
			if filepath.IsAbs(value) {
				p.Path.SetValue(value)
				p.Path.CursorEnd()
				return p.resolve(false)
			}
		}
	}
	previous := p.Path.Value()
	var cmd tea.Cmd
	p.Path, cmd = p.Path.Update(msg)
	if p.Path.Value() != previous {
		return tea.Batch(cmd, p.resolve(false))
	}
	return cmd
}

func (p *Picker) CountVisible() tea.Cmd {
	if p.Loading || p.ctx == nil || p.ctx.Err() != nil {
		return nil
	}
	var commands []tea.Cmd
	for col := len(p.Columns) - 1; col >= 0 && len(p.pending) < 4; col-- {
		if len(p.bounds) > 0 && !slices.ContainsFunc(p.bounds, func(b columnBounds) bool { return b.Index == col && b.Width >= 24 }) {
			continue
		}
		column := p.Columns[col]
		indices := column.Matches
		for _, index := range indices[min(column.Offset, len(indices)):min(len(indices), column.Offset+p.Height)] {
			entry := column.Entries[index]
			path := filepath.Join(column.Path, entry.Name)
			if _, done := p.counts[path]; done || !entry.Dir || p.pending[path] {
				continue
			}
			if len(p.pending) == 4 {
				break
			}
			p.pending[path] = true
			ctx, gen := p.ctx, p.Generation
			commands = append(commands, func() tea.Msg {
				count, candidate, err := inspectDirectory(ctx, path)
				return DirectoryCountMsg{Path: path, Count: count, Package: candidate, Err: err, Generation: gen}
			})
		}
	}
	return tea.Batch(commands...)
}

func inspectDirectory(ctx context.Context, path string) (int, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	count := 0
	messagesDir := ""
	for {
		if err := ctx.Err(); err != nil {
			return count, false, err
		}
		entries, err := f.ReadDir(128)
		for _, entry := range entries {
			if entry.IsDir() {
				count++
				if strings.EqualFold(entry.Name(), "Messages") {
					messagesDir = entry.Name()
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, false, err
		}
	}
	if messagesDir == "" {
		return count, false, nil
	}

	messages, err := os.Open(filepath.Join(path, messagesDir))
	if err != nil {
		return count, false, err
	}
	defer messages.Close()
	for {
		if err := ctx.Err(); err != nil {
			return count, false, err
		}
		entries, err := messages.ReadDir(128)
		if slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
			return !entry.IsDir() && strings.EqualFold(entry.Name(), "index.json")
		}) {
			return count, true, nil
		}
		if err == io.EOF {
			return count, false, nil
		}
		if err != nil {
			return count, false, err
		}
	}
}

func (p *Picker) ApplyCount(m DirectoryCountMsg) tea.Cmd {
	if m.Generation != p.Generation {
		return nil
	}
	delete(p.pending, m.Path)
	if m.Err != nil {
		p.counts[m.Path] = -1
	} else {
		p.counts[m.Path] = m.Count
		p.packages[m.Path] = m.Package
	}
	return p.CountVisible()
}
