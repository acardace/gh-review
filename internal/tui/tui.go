// Package tui provides a bubbletea-based terminal UI for reviewing
// PR threads. It has two screens: a thread list and a detail view.
package tui

import (
	"fmt"
	"time"

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

// commentPostedMsg is sent after a comment is successfully posted.
type commentPostedMsg struct {
	threadIndex int
	comment     model.Comment
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

	// Inline compose
	composing     bool
	compose       composeView
	composeThread *model.Thread

	width, height int
}

// New creates the TUI model with pre-fetched data.
func New(client *github.Client, pr *model.PRInfo, threads []model.Thread) Model {
	user, _ := client.CurrentUser()
	return Model{
		client:  client,
		pr:      pr,
		threads: threads,
		screen:  screenList,
		list:    newListView(threads, pr, user),
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
		m.updateDetailSize()
		return m, nil

	case statusMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.status = msg.text
		}
		return m, nil

	case commentPostedMsg:
		m.appendComment(msg)
		m.status = "✓ Comment posted"
		return m, nil
	}

	// Composing takes priority over everything
	if m.composing {
		return m.updateCompose(msg)
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

func (m *Model) updateDetailSize() {
	detailHeight := m.height
	if m.composing {
		// Reserve space for the compose box: border(2) + header line(1) + textarea lines + padding
		detailHeight = m.height - composeHeight - 5
		if detailHeight < 5 {
			detailHeight = 5
		}
	}
	m.detail.width = m.width
	m.detail.height = detailHeight
	if m.screen == screenDetail {
		m.detail.lines = m.detail.render()
	}
}

func (m Model) updateCompose(msg tea.Msg) (tea.Model, tea.Cmd) {
	action, cmd := m.compose.update(msg)
	switch action {
	case composeSubmit:
		body := m.compose.value()
		m.composing = false
		m.updateDetailSize()
		return m, m.postComment(m.composeThread, body)
	case composeCancel:
		m.composing = false
		m.composeThread = nil
		m.status = "Cancelled"
		m.updateDetailSize()
	}
	return m, cmd
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
			m.detail = newDetailView(t, m.pr, m.list.currentUser, m.width, m.height, m.list.hideBots)
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
			m.composing = true
			m.composeThread = t
			m.compose = newComposeView(m.width)
			m.status = ""
			// If we're on the list, switch to detail first
			if m.screen == screenList {
				m.screen = screenDetail
				m.detail = newDetailView(t, m.pr, m.list.currentUser, m.width, m.height, m.list.hideBots)
			}
			m.updateDetailSize()
			// Scroll detail to bottom so context is visible
			maxScroll := max(0, len(m.detail.lines)-m.detail.contentHeight())
			m.detail.scroll = maxScroll
			return m, m.compose.textarea.Focus()
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

	if m.composing {
		content += m.compose.view() + "\n"
		bar = statusBarStyle.Width(m.width).Render("ctrl+s: submit  esc: cancel")
	} else if m.confirming {
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

// appendComment adds the posted comment to the thread and re-renders
// the detail view if we're looking at that thread.
func (m *Model) appendComment(msg commentPostedMsg) {
	for i := range m.threads {
		if m.threads[i].Index == msg.threadIndex {
			m.threads[i].Comments = append(m.threads[i].Comments, msg.comment)
			if m.screen == screenDetail && m.detail.thread.Index == msg.threadIndex {
				m.detail.thread = &m.threads[i]
				m.detail.lines = m.detail.render()
				maxScroll := max(0, len(m.detail.lines)-m.detail.contentHeight())
				m.detail.scroll = maxScroll
			}
			break
		}
	}
}

// postComment sends the comment to GitHub and returns a commentPostedMsg.
func (m *Model) postComment(t *model.Thread, body string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ReplyToThread(m.pr.Number, t.RootID, body); err != nil {
			return statusMsg{err: fmt.Errorf("posting comment: %w", err)}
		}

		user, _ := m.client.CurrentUser()
		return commentPostedMsg{
			threadIndex: t.Index,
			comment: model.Comment{
				User:      user,
				UserType:  "User",
				Body:      body,
				CreatedAt: time.Now(),
			},
		}
	}
}
