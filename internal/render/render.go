// Package render provides terminal rendering for PR review data.
// All output goes through an io.Writer so it can be used from CLI
// or captured for testing / TUI integration.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/acardace/gh-review/internal/model"
	"golang.org/x/term"
)

// Style holds ANSI escape codes for terminal formatting.
// All fields are empty strings when color is disabled.
type Style struct {
	Bold, Dim, UL, RST string
	Red, Grn, Yel, Blu string
	Mag, Cyn, Wht      string
}

// C is the global style used by all render functions.
// It is initialized by Init (or DetectStyle for auto-detection).
var C Style

func init() {
	C = DetectStyle(os.Stdout)
}

// DetectStyle returns a Style based on whether w is a terminal.
func DetectStyle(w *os.File) Style {
	if !term.IsTerminal(int(w.Fd())) {
		return Style{}
	}
	return Style{
		Bold: "\033[1m", Dim: "\033[2m", UL: "\033[4m", RST: "\033[0m",
		Red: "\033[31m", Grn: "\033[32m", Yel: "\033[33m", Blu: "\033[34m",
		Mag: "\033[35m", Cyn: "\033[36m", Wht: "\033[37m",
	}
}

// termWidth returns the current terminal width, defaulting to 80.
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// HR prints a horizontal rule.
func HR(w io.Writer) {
	fmt.Fprintln(w, strings.Repeat("─", termWidth()))
}

// PRHeader prints the PR metadata header.
func PRHeader(w io.Writer, pr *model.PRInfo) {
	fmt.Fprintln(w)
	HR(w)
	fmt.Fprintf(w, "%sPR #%d: %s%s\n", C.Bold, pr.Number, pr.Title, C.RST)
	fmt.Fprintf(w, "%s%s%s\n", C.Dim, pr.URL, C.RST)
	fmt.Fprintf(w, "%s%s -> %s  |  author: %s  |  state: %s%s\n",
		C.Dim, pr.Head, pr.Base, pr.Author, pr.State, C.RST)
	HR(w)
}

// IssueComments prints the general conversation comments section.
func IssueComments(w io.Writer, comments []model.Comment, skipBots bool) {
	var visible []model.Comment
	for i := range comments {
		if skipBots && comments[i].IsBot() {
			continue
		}
		visible = append(visible, comments[i])
	}
	if len(visible) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%sGENERAL COMMENTS (%d)%s\n", C.Bold, C.Blu, len(visible), C.RST)
	HR(w)

	for i := range visible {
		fmt.Fprintln(w)
		CommentHeader(w, &visible[i], "  ")
		fmt.Fprintln(w)
		Body(w, visible[i].Body, "  ")
		fmt.Fprintln(w)
		HR(w)
	}
}

// ThreadsSummary prints the review comments section header.
func ThreadsSummary(w io.Writer, total, open, resolved int, showResolved bool) {
	fmt.Fprintln(w)
	if showResolved {
		fmt.Fprintf(w, "%s%sREVIEW COMMENTS (%d threads: %d open, %d resolved)%s\n",
			C.Bold, C.Blu, total, open, resolved, C.RST)
	} else {
		fmt.Fprintf(w, "%s%sREVIEW COMMENTS (%d open threads)%s  %s(%d resolved hidden, use --resolved to show)%s\n",
			C.Bold, C.Blu, open, C.RST, C.Dim, resolved, C.RST)
	}
	HR(w)
}

// Thread prints a single thread with all its comments.
func Thread(w io.Writer, t *model.Thread, skipBots bool) {
	statusTag := fmt.Sprintf("%s[open]%s", C.Yel, C.RST)
	if t.Resolved {
		statusTag = fmt.Sprintf("%s[resolved]%s", C.Dim, C.RST)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s%s[%d] %s%s%s%s:%d%s  %s  %s(%d comment(s))%s\n",
		C.Bold, C.Wht, t.Index, C.UL, t.Path, C.RST, C.Bold, t.Line, C.RST,
		statusTag, C.Dim, len(t.Comments), C.RST)
	fmt.Fprintln(w)

	DiffHunk(w, t.DiffHunk)

	for i := range t.Comments {
		if skipBots && t.Comments[i].IsBot() {
			continue
		}
		CommentHeader(w, &t.Comments[i], "    ")
		fmt.Fprintln(w)
		Body(w, t.Comments[i].Body, "  ")
		fmt.Fprintln(w)
	}

	HR(w)
}

// DiffHunk prints the last 8 lines of a diff hunk with coloring.
func DiffHunk(w io.Writer, hunk string) {
	if hunk == "" {
		return
	}

	lines := strings.Split(hunk, "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}

	fmt.Fprintf(w, "%s  ┌─ diff context%s\n", C.Dim, C.RST)
	for _, dl := range lines {
		switch {
		case strings.HasPrefix(dl, "+"):
			fmt.Fprintf(w, "%s  │ %s%s\n", C.Grn, dl, C.RST)
		case strings.HasPrefix(dl, "-"):
			fmt.Fprintf(w, "%s  │ %s%s\n", C.Red, dl, C.RST)
		case strings.HasPrefix(dl, "@@"):
			fmt.Fprintf(w, "%s  │ %s%s\n", C.Cyn, dl, C.RST)
		default:
			fmt.Fprintf(w, "%s  │ %s%s\n", C.Dim, dl, C.RST)
		}
	}
	fmt.Fprintf(w, "%s  └──%s\n", C.Dim, C.RST)
	fmt.Fprintln(w)
}

// CommentHeader prints the author line for a comment.
func CommentHeader(w io.Writer, c *model.Comment, indent string) {
	ts := c.CreatedAt.Format("2006-01-02 15:04")
	if c.IsBot() {
		fmt.Fprintf(w, "%s%s%s%s%s %s[bot]%s  %s%s%s\n",
			indent, C.Yel, C.Bold, c.User, C.RST, C.Dim, C.RST, C.Dim, ts, C.RST)
	} else {
		fmt.Fprintf(w, "%s%s%s%s%s  %s%s%s\n",
			indent, C.Grn, C.Bold, c.User, C.RST, C.Dim, ts, C.RST)
	}
}

// Body renders a comment body with code block and inline code highlighting.
func Body(w io.Writer, body string, indent string) {
	inCodeBlock := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				fmt.Fprintf(w, "%s%s└──%s\n", indent, C.Dim, C.RST)
			} else {
				lang := strings.TrimPrefix(line, "```")
				if lang != "" {
					fmt.Fprintf(w, "%s%s┌─ %s%s\n", indent, C.Dim, lang, C.RST)
				} else {
					fmt.Fprintf(w, "%s%s┌──%s\n", indent, C.Dim, C.RST)
				}
			}
			inCodeBlock = !inCodeBlock
			continue
		}

		if inCodeBlock {
			fmt.Fprintf(w, "%s%s│ %s%s\n", indent, C.Cyn, line, C.RST)
		} else {
			fmt.Fprintf(w, "%s%s\n", indent, highlightInlineCode(line))
		}
	}

	if inCodeBlock {
		fmt.Fprintf(w, "%s%s└──%s\n", indent, C.Dim, C.RST)
	}
}

// highlightInlineCode replaces `code` with colored version.
func highlightInlineCode(s string) string {
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
		b.WriteString(C.Mag)
		b.WriteString(s[start+1 : end])
		b.WriteString(C.RST)
		s = s[end+1:]
	}
	return b.String()
}

// Footer prints the summary line.
func Footer(w io.Writer, prNum, total, open, resolved int, showResolved bool) {
	fmt.Fprintln(w)
	if showResolved {
		fmt.Fprintf(w, "%sDone. %d thread(s) from PR #%d (%d open, %d resolved).%s\n",
			C.Dim, total, prNum, open, resolved, C.RST)
	} else {
		fmt.Fprintf(w, "%sDone. %d open thread(s) from PR #%d (%d resolved hidden).%s\n",
			C.Dim, open, prNum, resolved, C.RST)
	}
	fmt.Fprintln(w)
}
