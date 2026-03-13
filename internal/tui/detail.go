package tui

import (
	"fmt"
	"strings"

	"github.com/acardace/gh-review/internal/model"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// detailView shows a single thread with its diff hunk and all comments.
type detailView struct {
	thread        *model.Thread
	scroll        int
	hideBots      bool
	lines         []string // pre-rendered lines
	width, height int
}

func newDetailView(t *model.Thread, width, height int, hideBots bool) detailView {
	d := detailView{thread: t, width: width, height: height, hideBots: hideBots}
	d.lines = d.render()
	return d
}

func (d *detailView) update(msg tea.Msg) (viewAction, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace", "h":
			return actionBack, nil
		case "ctrl+c":
			return actionQuit, nil
		case "up", "k":
			if d.scroll > 0 {
				d.scroll--
			}
		case "down", "j":
			maxScroll := max(0, len(d.lines)-d.contentHeight())
			if d.scroll < maxScroll {
				d.scroll++
			}
		case "g", "home":
			d.scroll = 0
		case "G", "end":
			d.scroll = max(0, len(d.lines)-d.contentHeight())
		case "r":
			return actionResolve, nil
		case "u":
			return actionUnresolve, nil
		case "c":
			return actionComment, nil
		case "pgdown", "ctrl+d":
			half := d.contentHeight() / 2
			maxScroll := max(0, len(d.lines)-d.contentHeight())
			d.scroll = min(d.scroll+half, maxScroll)
		case "pgup", "ctrl+u":
			half := d.contentHeight() / 2
			d.scroll = max(d.scroll-half, 0)
		}
	}
	return actionNone, nil
}

func (d detailView) contentHeight() int {
	return max(1, d.height-3) // header line + status bar (2 lines)
}

func (d detailView) view() string {
	var b strings.Builder

	// Header
	status := openBadge
	if d.thread.Resolved {
		status = resolvedBadge
	}
	header := headerStyle.Width(d.width).Render(
		fmt.Sprintf("#%d  %s:%d  %s", d.thread.Index, d.thread.Path, d.thread.Line, status),
	)
	b.WriteString(header + "\n")

	// Scrollable content
	ch := d.contentHeight()
	end := min(d.scroll+ch, len(d.lines))
	for i := d.scroll; i < end; i++ {
		b.WriteString(d.lines[i] + "\n")
	}

	// Pad remaining lines
	rendered := end - d.scroll
	for i := rendered; i < ch; i++ {
		b.WriteString("\n")
	}

	return b.String()
}

func (d detailView) statusBar() string {
	pos := ""
	if len(d.lines) > 0 {
		pct := 0
		if len(d.lines) > d.contentHeight() {
			pct = d.scroll * 100 / (len(d.lines) - d.contentHeight())
		}
		pos = fmt.Sprintf(" %d%% ", pct)
	}
	keys := "j/k: scroll  c: comment  r: resolve  u: unresolve  esc: back  q: quit" + pos
	return statusBarStyle.Width(d.width).Render(keys)
}

// render pre-builds the detail content as plain lines.
func (d detailView) render() []string {
	var lines []string

	// Diff hunk
	if d.thread.DiffHunk != "" {
		lines = append(lines, dimStyle.Render("┌─ diff context"))
		for _, dl := range strings.Split(d.thread.DiffHunk, "\n") {
			var styled string
			switch {
			case strings.HasPrefix(dl, "+"):
				styled = diffAddStyle.Render("│ " + dl)
			case strings.HasPrefix(dl, "-"):
				styled = diffRemoveStyle.Render("│ " + dl)
			case strings.HasPrefix(dl, "@@"):
				styled = diffHunkStyle.Render("│ " + dl)
			default:
				styled = diffContextStyle.Render("│ " + dl)
			}
			lines = append(lines, styled)
		}
		lines = append(lines, dimStyle.Render("└──"), "")
	}

	// Comments
	for _, c := range d.thread.Comments {
		if d.hideBots && c.IsBot() {
			continue
		}
		ts := dateStyle.Render(c.CreatedAt.Format("2006-01-02 15:04"))
		if c.IsBot() {
			lines = append(lines, botStyle.Render(c.User)+" "+dimStyle.Render("[bot]")+"  "+ts)
		} else {
			lines = append(lines, authorStyle.Render(c.User)+"  "+ts)
		}
		lines = append(lines, "")
		lines = append(lines, renderBody(c.Body, d.width)...)
		lines = append(lines, "")
	}

	return lines
}

// renderBody converts markdown-like comment body to styled lines.
// Non-code text is word-wrapped to fit within the terminal width.
// Code blocks are left unwrapped to preserve formatting.
func renderBody(body string, width int) []string {
	var lines []string
	inCode := false
	// 2 chars indent
	wrapWidth := width - 2
	if wrapWidth < 20 {
		wrapWidth = 20
	}

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			if inCode {
				lines = append(lines, dimStyle.Render("  └──"))
			} else {
				lang := strings.TrimPrefix(line, "```")
				if lang != "" {
					lines = append(lines, dimStyle.Render("  ┌─ "+lang))
				} else {
					lines = append(lines, dimStyle.Render("  ┌──"))
				}
			}
			inCode = !inCode
			continue
		}

		if inCode {
			lines = append(lines, codeBlockStyle.Render("  │ "+line))
		} else {
			for _, wrapped := range wordWrap(line, wrapWidth) {
				lines = append(lines, "  "+highlightInline(wrapped))
			}
		}
	}

	if inCode {
		lines = append(lines, dimStyle.Render("  └──"))
	}

	return lines
}

// wordWrap breaks a line into multiple lines at word boundaries,
// using terminal-aware character widths (handles emoji, CJK, etc.).
// Returns at least one line (empty string for empty input).
func wordWrap(s string, width int) []string {
	if runewidth.StringWidth(s) <= width {
		return []string{s}
	}

	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{s}
	}

	var lines []string
	line := words[0]
	lineWidth := runewidth.StringWidth(line)

	for _, word := range words[1:] {
		ww := runewidth.StringWidth(word)
		if lineWidth+1+ww <= width {
			line += " " + word
			lineWidth += 1 + ww
		} else {
			lines = append(lines, line)
			line = word
			lineWidth = ww
		}
	}
	lines = append(lines, line)
	return lines
}

// highlightInline highlights `code` spans.
func highlightInline(s string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(s, '`')
		if start == -1 {
			b.WriteString(s)
			break
		}
		end := strings.IndexByte(s[start+1:], '`')
		if end == -1 {
			b.WriteString(s)
			break
		}
		end += start + 1
		b.WriteString(s[:start])
		b.WriteString(inlineCodeStyle.Render(s[start : end+1]))
		s = s[end+1:]
	}
	return b.String()
}
