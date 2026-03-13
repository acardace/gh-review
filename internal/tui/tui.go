// Package tui provides a bubbletea-based terminal UI for reviewing
// PR threads. It has two screens: a thread list and a detail view.
package tui

import (
	"fmt"
	"io"

	"github.com/acardace/gh-review/internal/editor"
	"github.com/acardace/gh-review/internal/github"
	"github.com/acardace/gh-review/internal/model"
	tea "github.com/charmbracelet/bubbletea"
)

type viewAction int

const (
	actionNone viewAction = iota
	actionQuit
	actionBack
	actionOpenDetail
	actionResolve
	actionUnresolve
	actionComment
)

type screen int

const (
	screenList screen = iota
	screenDetail
)

// statusMsg is sent after an async operation to show feedback.
type statusMsg struct {
	text string
	err  error
}

// Model is the top-level bubbletea model.
type Model struct {
	client  *github.Client
	pr      *model.PRInfo
	threads []model.Thread

	screen     screen
	list       listView
	detail     detailView
	status     string         // ephemeral status message
	confirming bool           // waiting for y/n on resolve/unresolve
	confirmMsg string         // what we're confirming
	confirmFn  func() tea.Msg // action to run on "y"

	width, height int
}

// New creates the TUI model with pre-fetched data.
func New(client *github.Client, pr *model.PRInfo, threads []model.Thread) Model {
	return Model{
		client:  client,
		pr:      pr,
		threads: threads,
		screen:  screenList,
		list:    newListView(threads),
	}
}

// Run starts the bubbletea program.
func Run(client *github.Client, pr *model.PRInfo, threads []model.Thread) error {
	m := New(client, pr, threads)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.width = msg.Width
		m.list.height = msg.Height
		m.detail.width = msg.Width
		m.detail.height = msg.Height
		if m.screen == screenDetail {
			m.detail.lines = m.detail.render()
		}
		return m, nil

	case statusMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.status = msg.text
		}
		return m, nil
	}

	// Handle confirmation prompt
	if m.confirming {
		return m.updateConfirm(msg)
	}

	switch m.screen {
	case screenList:
		return m.updateList(msg)
	case screenDetail:
		return m.updateDetail(msg)
	}
	return m, nil
}

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y":
		m.confirming = false
		m.confirmMsg = ""
		fn := m.confirmFn
		m.confirmFn = nil
		return m, fn
	default:
		// any other key cancels
		m.confirming = false
		m.confirmMsg = ""
		m.confirmFn = nil
		m.status = "Cancelled"
	}
	return m, nil
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	action, cmd := m.list.update(msg)
	return m.handleAction(action, cmd, m.list.selectedThread())
}

func (m Model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	action, cmd := m.detail.update(msg)
	return m.handleAction(action, cmd, m.detail.thread)
}

func (m Model) handleAction(action viewAction, cmd tea.Cmd, t *model.Thread) (tea.Model, tea.Cmd) {
	switch action {
	case actionQuit:
		return m, tea.Quit
	case actionBack:
		m.screen = screenList
		m.status = ""
	case actionOpenDetail:
		if t != nil {
			m.screen = screenDetail
			m.detail = newDetailView(t, m.width, m.height, m.list.hideBots)
			m.status = ""
		}
	case actionResolve:
		if t != nil {
			return m.askConfirm(
				fmt.Sprintf("Resolve thread #%d? [y/N]", t.Index),
				m.resolveThreadFn(t),
			)
		}
	case actionUnresolve:
		if t != nil {
			return m.askConfirm(
				fmt.Sprintf("Unresolve thread #%d? [y/N]", t.Index),
				m.unresolveThreadFn(t),
			)
		}
	case actionComment:
		if t != nil {
			return m, m.commentOnThread(t)
		}
	}
	return m, cmd
}

func (m Model) askConfirm(msg string, fn func() tea.Msg) (tea.Model, tea.Cmd) {
	m.confirming = true
	m.confirmMsg = msg
	m.confirmFn = fn
	m.status = ""
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var content, bar string
	switch m.screen {
	case screenList:
		content = m.list.view()
		bar = m.list.statusBar()
	case screenDetail:
		content = m.detail.view()
		bar = m.detail.statusBar()
	}

	if m.confirming {
		bar = statusBarStyle.Width(m.width).Render(m.confirmMsg)
	} else if m.status != "" {
		bar = statusBarStyle.Width(m.width).Render(m.status)
	}

	return content + bar
}

func (m *Model) resolveThreadFn(t *model.Thread) func() tea.Msg {
	return func() tea.Msg {
		if t.ThreadNodeID == "" {
			return statusMsg{err: fmt.Errorf("no thread ID for #%d", t.Index)}
		}
		if t.Resolved {
			return statusMsg{text: fmt.Sprintf("#%d is already resolved", t.Index)}
		}
		if err := m.client.ResolveThread(t.ThreadNodeID); err != nil {
			return statusMsg{err: err}
		}
		t.Resolved = true
		return statusMsg{text: fmt.Sprintf("✓ Resolved #%d", t.Index)}
	}
}

func (m *Model) unresolveThreadFn(t *model.Thread) func() tea.Msg {
	return func() tea.Msg {
		if t.ThreadNodeID == "" {
			return statusMsg{err: fmt.Errorf("no thread ID for #%d", t.Index)}
		}
		if !t.Resolved {
			return statusMsg{text: fmt.Sprintf("#%d is already open", t.Index)}
		}
		if err := m.client.UnresolveThread(t.ThreadNodeID); err != nil {
			return statusMsg{err: err}
		}
		t.Resolved = false
		return statusMsg{text: fmt.Sprintf("✓ Unresolved #%d", t.Index)}
	}
}

// commentOnThread suspends the TUI, opens $EDITOR, posts the comment, resumes.
func (m *Model) commentOnThread(t *model.Thread) tea.Cmd {
	ep := &editorProcess{thread: t, client: m.client, prNum: m.pr.Number}
	return tea.Exec(ep, func(err error) tea.Msg {
		if err != nil {
			return statusMsg{err: err}
		}
		return statusMsg{text: "✓ Comment posted"}
	})
}

// editorProcess implements tea.ExecCommand for the editor flow.
type editorProcess struct {
	thread *model.Thread
	client *github.Client
	prNum  int
}

func (e *editorProcess) Run() error {
	body, err := editor.EditReply(e.thread)
	if err != nil {
		return err
	}
	if body == "" {
		return fmt.Errorf("aborted (empty message)")
	}
	return e.client.ReplyToThread(e.prNum, e.thread.RootID, body)
}

func (e *editorProcess) SetStdin(_ io.Reader)  {}
func (e *editorProcess) SetStdout(_ io.Writer) {}
func (e *editorProcess) SetStderr(_ io.Writer) {}
