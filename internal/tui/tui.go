// Package tui provides a bubbletea-based terminal UI for reviewing
// PR threads. It has two screens: a thread list and a detail view.
package tui

import (
	"fmt"
	"time"

	"github.com/acardace/gh-review/internal/github"
	"github.com/acardace/gh-review/internal/model"
	"github.com/charmbracelet/bubbles/spinner"
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
	screenLoading screen = iota
	screenList
	screenDetail
)

// -- Messages -----------------------------------------------------------------

type statusMsg struct {
	text string
	err  error
}

type commentPostedMsg struct {
	threadIndex int
	comment     model.Comment
}

// clientReadyMsg is sent when the API client is initialized.
type clientReadyMsg struct {
	client *github.Client
	err    error
}

// prLoadedMsg is sent when PR info is fetched.
type prLoadedMsg struct {
	pr  *model.PRInfo
	err error
}

// threadsLoadedMsg is sent when threads are fetched.
type threadsLoadedMsg struct {
	threads []model.Thread
	err     error
}

// -- Model --------------------------------------------------------------------

// Model is the top-level bubbletea model.
type Model struct {
	client      *github.Client
	pr          *model.PRInfo
	threads     []model.Thread
	currentUser string
	prNum       int // 0 = auto-detect

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

	// Loading state
	spinner    spinner.Model
	loadingMsg string
	loadingErr error

	width, height int
}

// Run starts the TUI immediately with a loading screen, then fetches
// all data progressively in the background.
func Run(prNum int) error {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = dimStyle

	m := Model{
		screen:     screenLoading,
		prNum:      prNum,
		spinner:    s,
		loadingMsg: "Connecting...",
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		initClient,
	)
}

// -- Loading commands ---------------------------------------------------------

func initClient() tea.Msg {
	client, err := github.NewClient()
	return clientReadyMsg{client, err}
}

func fetchPR(client *github.Client, prNum int) tea.Cmd {
	return func() tea.Msg {
		pr, err := client.GetPRInfo(prNum)
		return prLoadedMsg{pr, err}
	}
}

func fetchThreads(client *github.Client, prNum int) tea.Cmd {
	return func() tea.Msg {
		threads, err := client.GetThreads(prNum)
		return threadsLoadedMsg{threads, err}
	}
}

// -- Update -------------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.width = msg.Width
		m.list.height = msg.Height
		m.updateDetailSize()
		return m, nil

	case tea.KeyMsg:
		// Allow quit during loading
		if m.screen == screenLoading {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// Update spinner in status text if we're loading threads on the list screen.
		if m.screen == screenList && m.threads == nil {
			m.status = m.spinner.View() + " Loading threads..."
		}
		return m, cmd

	case clientReadyMsg:
		if msg.err != nil {
			m.loadingErr = msg.err
			return m, nil
		}
		m.client = msg.client
		m.loadingMsg = "Fetching PR..."
		return m, fetchPR(m.client, m.prNum)

	case prLoadedMsg:
		if msg.err != nil {
			m.loadingErr = msg.err
			return m, nil
		}
		m.pr = msg.pr
		m.currentUser = m.client.CurrentUser()
		// Switch to list view immediately with empty threads.
		// Show "loading threads..." status while fetching.
		m.screen = screenList
		m.list = newListView(nil, m.pr, m.currentUser)
		m.list.width = m.width
		m.list.height = m.height
		m.status = m.spinner.View() + " Loading threads..."
		return m, tea.Batch(m.spinner.Tick, fetchThreads(m.client, m.pr.Number))

	case threadsLoadedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Error loading threads: %v", msg.err)
			return m, nil
		}
		m.threads = msg.threads
		m.list.threads = m.threads
		m.list.clampCursor()
		m.status = ""
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

// -- View ---------------------------------------------------------------------

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.screen == screenLoading {
		return m.viewLoading()
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

func (m Model) viewLoading() string {
	if m.loadingErr != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.\n", m.loadingErr)
	}
	return fmt.Sprintf("\n  %s %s\n", m.spinner.View(), m.loadingMsg)
}

// -- Subview updates ----------------------------------------------------------

func (m *Model) updateDetailSize() {
	detailHeight := m.height
	if m.composing {
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
			m.detail = newDetailView(t, m.pr, m.currentUser, m.width, m.height, m.list.hideBots)
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
			if m.screen == screenList {
				m.screen = screenDetail
				m.detail = newDetailView(t, m.pr, m.currentUser, m.width, m.height, m.list.hideBots)
			}
			m.updateDetailSize()
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

// -- Thread mutations ---------------------------------------------------------

func (m *Model) resolveThreadFn(t *model.Thread) func() tea.Msg {
	return m.toggleResolveFn(t, true)
}

func (m *Model) unresolveThreadFn(t *model.Thread) func() tea.Msg {
	return m.toggleResolveFn(t, false)
}

func (m *Model) toggleResolveFn(t *model.Thread, resolve bool) func() tea.Msg {
	return func() tea.Msg {
		if t.ThreadNodeID == "" {
			return statusMsg{err: fmt.Errorf("no thread ID for #%d", t.Index)}
		}
		if t.Resolved == resolve {
			state := "resolved"
			if !resolve {
				state = "open"
			}
			return statusMsg{text: fmt.Sprintf("#%d is already %s", t.Index, state)}
		}
		var err error
		if resolve {
			err = m.client.ResolveThread(t.ThreadNodeID)
		} else {
			err = m.client.UnresolveThread(t.ThreadNodeID)
		}
		if err != nil {
			return statusMsg{err: err}
		}
		t.Resolved = resolve
		verb := "Resolved"
		if !resolve {
			verb = "Unresolved"
		}
		return statusMsg{text: fmt.Sprintf("✓ %s #%d", verb, t.Index)}
	}
}

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

func (m *Model) postComment(t *model.Thread, body string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ReplyToThread(m.pr.Number, t.RootID, body); err != nil {
			return statusMsg{err: fmt.Errorf("posting comment: %w", err)}
		}

		return commentPostedMsg{
			threadIndex: t.Index,
			comment: model.Comment{
				User:      m.currentUser,
				UserType:  "User",
				Body:      body,
				CreatedAt: time.Now(),
			},
		}
	}
}
