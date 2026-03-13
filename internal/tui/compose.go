package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const composeHeight = 8

// composeView is an inline multi-line text input for writing comments.
type composeView struct {
	textarea textarea.Model
	active   bool
}

func newComposeView(width int) composeView {
	ta := textarea.New()
	ta.Placeholder = "Write your comment..."
	ta.Prompt = "│ "
	ta.SetWidth(width - 4)
	ta.SetHeight(composeHeight)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // no limit
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.Focus()

	return composeView{
		textarea: ta,
		active:   true,
	}
}

func (c *composeView) update(msg tea.Msg) (composeAction, tea.Cmd) {
	if !c.active {
		return composeNone, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.Type {
		case tea.KeyCtrlS:
			body := c.textarea.Value()
			if body == "" {
				return composeCancel, nil
			}
			c.active = false
			return composeSubmit, nil
		case tea.KeyEsc:
			c.active = false
			return composeCancel, nil
		}
	}

	var cmd tea.Cmd
	c.textarea, cmd = c.textarea.Update(msg)
	return composeNone, cmd
}

func (c composeView) view() string {
	if !c.active {
		return ""
	}

	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorMagenta).
		Padding(0, 1)

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorMagenta).
		Render("Comment")

	hint := dimStyle.Render("  ctrl+s: submit  esc: cancel")

	return border.Render(header + hint + "\n" + c.textarea.View())
}

func (c composeView) value() string {
	return c.textarea.Value()
}

type composeAction int

const (
	composeNone composeAction = iota
	composeSubmit
	composeCancel
)
