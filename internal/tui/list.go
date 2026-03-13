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
	cursor        int
	offset        int // scroll offset for the list
	showResolved  bool
	hideBots      bool
	width, height int
}

func newListView(threads []model.Thread) listView {
	return listView{threads: threads}
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
		indices = append(indices, i)
	}
	return indices
}

func (l *listView) update(msg tea.Msg) (viewAction, tea.Cmd) {
	vis := l.visible()
	if len(vis) == 0 {
		if msg, ok := msg.(tea.KeyMsg); ok && (msg.String() == "q" || msg.String() == "ctrl+c") {
			return actionQuit, nil
		}
		return actionNone, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return actionQuit, nil
		case "up", "k":
			if l.cursor > 0 {
				l.cursor--
				l.ensureVisible()
			}
		case "down", "j":
			if l.cursor < len(vis)-1 {
				l.cursor++
				l.ensureVisible()
			}
		case "enter", "l":
			return actionOpenDetail, nil
		case "r":
			return actionResolve, nil
		case "u":
			return actionUnresolve, nil
		case "c":
			return actionComment, nil
		case "tab":
			l.showResolved = !l.showResolved
			vis = l.visible()
			if l.cursor >= len(vis) {
				l.cursor = max(0, len(vis)-1)
			}
			l.offset = 0
		case "b":
			l.hideBots = !l.hideBots
			vis = l.visible()
			if l.cursor >= len(vis) {
				l.cursor = max(0, len(vis)-1)
			}
			l.offset = 0
		case "g", "home":
			l.cursor = 0
			l.offset = 0
		case "G", "end":
			l.cursor = len(vis) - 1
			l.ensureVisible()
		}
	}
	return actionNone, nil
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
	header := titleStyle.Render(fmt.Sprintf(" Review Threads (%d open, %d resolved) [%s] ",
		open, resolved, strings.Join(filters, ", ")))
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

		// First line: index, file:line, status
		line1 := fmt.Sprintf("%s%s  %s:%d  %s  (%d)",
			cursor,
			style.Render(fmt.Sprintf("#%d", t.Index)),
			style.Render(t.Path),
			t.Line,
			badge,
			len(t.Comments),
		)

		// Second line: first comment preview
		preview := ""
		if len(t.Comments) > 0 {
			c := t.Comments[0]
			body := strings.Join(strings.Fields(c.Body), " ")
			author := dimStyle.Render(fmt.Sprintf("@%s: ", c.User))

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
	keys := "j/k: navigate  enter: view  c: comment  r: resolve  u: unresolve  tab: toggle resolved  b: toggle bots  q: quit"
	return statusBarStyle.Width(l.width).Render(keys)
}
