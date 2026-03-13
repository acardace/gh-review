package tui

import (
	"fmt"
	"strings"

	"github.com/acardace/gh-review/internal/model"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// listView is the thread list screen.
type listView struct {
	threads       []model.Thread
	pr            *model.PRInfo
	currentUser   string
	cursor        int
	offset        int // scroll offset for the list
	showResolved  bool
	hideBots      bool
	needsReply    bool // only show threads awaiting your reply
	width, height int
}

func newListView(threads []model.Thread, pr *model.PRInfo, currentUser string) listView {
	return listView{threads: threads, pr: pr, currentUser: currentUser}
}

func (l listView) visible() []int {
	var indices []int
	for i, t := range l.threads {
		if !l.showResolved && t.Resolved {
			continue
		}
		if l.hideBots && model.AllBots(t.Comments) {
			continue
		}
		if l.needsReply {
			if !l.threadNeedsReply(&t) {
				continue
			}
		}
		indices = append(indices, i)
	}
	return indices
}

func (l listView) threadNeedsReply(t *model.Thread) bool {
	return t.NeedsReply(l.currentUser)
}

func (l *listView) update(msg tea.Msg) (viewAction, tea.Cmd) {
	vis := l.visible()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return actionQuit, nil

		// Filters — always available, even with empty list
		case "tab":
			l.needsReply = !l.needsReply
			l.clampCursor()
		case "a":
			l.showResolved = !l.showResolved
			l.clampCursor()
		case "b":
			l.hideBots = !l.hideBots
			l.clampCursor()

		// Navigation and actions — only when there are visible threads
		case "up", "k":
			if len(vis) > 0 && l.cursor > 0 {
				l.cursor--
				l.ensureVisible()
			}
		case "down", "j":
			if len(vis) > 0 && l.cursor < len(vis)-1 {
				l.cursor++
				l.ensureVisible()
			}
		case "enter", "l":
			if len(vis) > 0 {
				return actionOpenDetail, nil
			}
		case "r":
			if len(vis) > 0 {
				return actionResolve, nil
			}
		case "u":
			if len(vis) > 0 {
				return actionUnresolve, nil
			}
		case "c":
			if len(vis) > 0 {
				return actionComment, nil
			}
		case "g", "home":
			l.cursor = 0
			l.offset = 0
		case "G", "end":
			if len(vis) > 0 {
				l.cursor = len(vis) - 1
				l.ensureVisible()
			}
		}
	}
	return actionNone, nil
}

func (l *listView) clampCursor() {
	vis := l.visible()
	if l.cursor >= len(vis) {
		l.cursor = max(0, len(vis)-1)
	}
	l.offset = 0
}

func (l *listView) ensureVisible() {
	listHeight := l.listHeight()
	if listHeight <= 0 {
		return
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+listHeight {
		l.offset = l.cursor - listHeight + 1
	}
}

func (l listView) listHeight() int {
	// header(2) + status bar(2) = 4 lines overhead, each item is 2 lines
	return max(1, (l.height-4)/2)
}

func (l listView) selectedThread() *model.Thread {
	vis := l.visible()
	if len(vis) == 0 || l.cursor >= len(vis) {
		return nil
	}
	return &l.threads[vis[l.cursor]]
}

func (l listView) view() string {
	var b strings.Builder

	vis := l.visible()

	open, resolved := 0, 0
	for _, t := range l.threads {
		if t.Resolved {
			resolved++
		} else {
			open++
		}
	}

	// Header
	var filters []string
	if l.showResolved {
		filters = append(filters, "all")
	} else {
		filters = append(filters, "open")
	}
	if l.hideBots {
		filters = append(filters, "no bots")
	}
	if l.needsReply {
		filters = append(filters, "needs reply")
	}
	header := titleStyle.Render(fmt.Sprintf(" PR #%d: %s  (%d open, %d resolved) [%s] ",
		l.pr.Number, l.pr.Title, open, resolved, strings.Join(filters, ", ")))
	b.WriteString(header + "\n\n")

	if len(vis) == 0 {
		b.WriteString(dimStyle.Render("  No threads to show."))
		b.WriteString("\n")
		return b.String()
	}

	// Render visible items
	listH := l.listHeight()
	end := min(l.offset+listH, len(vis))

	for vi := l.offset; vi < end; vi++ {
		idx := vis[vi]
		t := &l.threads[idx]

		cursor := "  "
		style := normalStyle
		if vi == l.cursor {
			cursor = "▸ "
			style = selectedStyle
		}

		badge := openBadge
		if t.Resolved {
			badge = resolvedBadge
		}

		// Who spoke last?
		var turnTag string
		if l.threadNeedsReply(t) {
			turnTag = lipgloss.NewStyle().Foreground(colorCyan).Render("  ← needs reply")
		}

		// First line: index, file:line, status, turn indicator
		line1 := fmt.Sprintf("%s%s  %s:%d  %s  (%d)%s",
			cursor,
			style.Render(fmt.Sprintf("#%d", t.Index)),
			style.Render(t.Path),
			t.Line,
			badge,
			len(t.Comments),
			turnTag,
		)

		// Second line: last comment preview
		preview := ""
		if len(t.Comments) > 0 {
			last := t.Comments[len(t.Comments)-1]
			body := strings.Join(strings.Fields(last.Body), " ")
			author := dimStyle.Render(fmt.Sprintf("@%s: ", last.User))

			maxBody := l.width - lipgloss.Width(author) - 6
			if maxBody > 0 && len(body) > maxBody {
				body = body[:maxBody-1] + "…"
			}
			preview = "    " + author + body
		}

		b.WriteString(line1 + "\n")
		b.WriteString(preview + "\n")
	}

	return b.String()
}

func (l listView) statusBar() string {
	keys := "j/k: navigate  enter: view  c: comment  r: resolve  u: unresolve  tab: needs reply  a: all/open  b: bots  q: quit"
	return statusBarStyle.Width(l.width).Render(keys)
}
