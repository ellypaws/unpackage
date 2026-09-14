package tui

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ellypaws/unpackage/pkg/components"
	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/store"
)

type commandPrompt struct {
	input      textinput.Model
	catalog    store.SearchCatalog
	history    []string
	index      int
	done, quit bool
}

func (m *commandPrompt) Init() tea.Cmd { return textinput.Blink }
func (m *commandPrompt) View() string {
	if m.quit {
		return ""
	}
	if m.done {
		return "› " + session.Safe(m.input.Value()) + "\n"
	}
	return m.input.View()
}
func (m *commandPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.input.Width = max(8, size.Width-4)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "ctrl+d":
			m.quit = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		case "tab":
			if s := m.input.CurrentSuggestion(); s != "" {
				m.input.SetValue(s)
				m.input.CursorEnd()
			}
		case "up":
			if len(m.history) > 0 {
				m.index = max(0, m.index-1)
				m.input.SetValue(m.history[m.index])
				m.input.CursorEnd()
			}
		case "down":
			m.index = min(len(m.history), m.index+1)
			m.input.SetValue("")
			if m.index < len(m.history) {
				m.input.SetValue(m.history[m.index])
				m.input.CursorEnd()
			}
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.input.SetSuggestions(session.CompleteCommand(m.input.Value(), m.catalog, m.history))
			return m, cmd
		}
		m.input.SetSuggestions(session.CompleteCommand(m.input.Value(), m.catalog, m.history))
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func ReadCommand(ctx context.Context, s *session.Session, history []string) (string, error) {
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 65536
	input.ShowSuggestions = true
	input.CompletionStyle = lipgloss.NewStyle().Foreground(components.Muted)
	input.Focus()
	m := &commandPrompt{input: input, catalog: s.Store.SearchCatalog(ctx), history: history, index: len(history)}
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(os.Stderr), tea.WithoutSignalHandler()).Run()
	if err != nil {
		return "", err
	}
	if m.quit {
		return "", io.EOF
	}
	return strings.TrimSpace(m.input.Value()), nil
}
