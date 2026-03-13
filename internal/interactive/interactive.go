// Package interactive provides the step-through review mode.
// It walks through open threads one by one, prompting the user
// to skip, resolve, unresolve, or comment.
package interactive

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/acardace/gh-review/internal/editor"
	"github.com/acardace/gh-review/internal/github"
	"github.com/acardace/gh-review/internal/model"
	"github.com/acardace/gh-review/internal/render"
)

// Run starts the interactive review loop over the given threads.
// Only unresolved threads (optionally filtered by skipBots) are presented.
func Run(w io.Writer, client *github.Client, prNum int, threads []model.Thread, skipBots bool) error {
	var open []model.Thread
	for _, t := range threads {
		if t.Resolved {
			continue
		}
		if skipBots && model.AllBots(t.Comments) {
			continue
		}
		open = append(open, t)
	}

	if len(open) == 0 {
		fmt.Fprintf(w, "\n%sNo open threads to review.%s\n\n", render.C.Dim, render.C.RST)
		return nil
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Fprintf(w, "\n%s%sInteractive review: %d open thread(s)%s\n",
		render.C.Bold, render.C.Blu, len(open), render.C.RST)
	fmt.Fprintf(w, "%sCommands: [s]kip  [r]esolve  [u]nresolve  [c]omment  [q]uit%s\n",
		render.C.Dim, render.C.RST)
	render.HR(w)

	for i := range open {
		t := &open[i]
		fmt.Fprintf(w, "\n%s%s── Thread %d/%d (index #%d) ──%s\n",
			render.C.Bold, render.C.Wht, i+1, len(open), t.Index, render.C.RST)

		render.Thread(w, t, skipBots)

		for {
			fmt.Fprintf(w, "\n  %s[s]kip  [r]esolve  [u]nresolve  [c]omment  [q]uit%s > ",
				render.C.Bold, render.C.RST)

			input, err := reader.ReadString('\n')
			if err != nil {
				return err
			}

			switch cmd := strings.TrimSpace(strings.ToLower(input)); cmd {
			case "s", "skip", "":
				fmt.Fprintf(w, "  %s→ Skipped%s\n", render.C.Dim, render.C.RST)
				goto next

			case "r", "resolve":
				if err := confirmAndMutate(w, reader, t, "Resolve", client.ResolveThread); err != nil {
					fmt.Fprintf(w, "  %sError: %v%s\n", render.C.Red, err, render.C.RST)
				}

			case "u", "unresolve":
				if err := confirmAndMutate(w, reader, t, "Unresolve", client.UnresolveThread); err != nil {
					fmt.Fprintf(w, "  %sError: %v%s\n", render.C.Red, err, render.C.RST)
				}

			case "c", "comment":
				body, err := editor.EditReply(t)
				if err != nil {
					fmt.Fprintf(w, "  %sError opening editor: %v%s\n", render.C.Red, err, render.C.RST)
					continue
				}
				if body == "" {
					fmt.Fprintf(w, "  %s→ Aborted (empty message)%s\n", render.C.Dim, render.C.RST)
					continue
				}
				if err := client.ReplyToThread(prNum, t.RootID, body); err != nil {
					fmt.Fprintf(w, "  %sError posting comment: %v%s\n", render.C.Red, err, render.C.RST)
					continue
				}
				fmt.Fprintf(w, "  %s✓ Comment posted%s\n", render.C.Grn, render.C.RST)

			case "q", "quit":
				fmt.Fprintf(w, "\n%s→ Stopped. Remaining threads unchanged.%s\n\n",
					render.C.Dim, render.C.RST)
				return nil

			default:
				fmt.Fprintf(w, "  %sUnknown command: %q (use s/r/u/c/q)%s\n",
					render.C.Yel, cmd, render.C.RST)
			}
		}
	next:
	}

	fmt.Fprintf(w, "\n%s✓ All open threads reviewed.%s\n\n", render.C.Grn, render.C.RST)
	return nil
}

// confirmAndMutate asks for confirmation, then calls mutateFn with the thread's node ID.
func confirmAndMutate(w io.Writer, reader *bufio.Reader, t *model.Thread, verb string, mutateFn func(string) error) error {
	if t.ThreadNodeID == "" {
		return fmt.Errorf("no thread ID available")
	}

	fmt.Fprintf(w, "  %s%s thread #%d? [y/N]%s > ", render.C.Yel, verb, t.Index, render.C.RST)
	confirm, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if c := strings.TrimSpace(strings.ToLower(confirm)); c != "y" && c != "yes" {
		fmt.Fprintf(w, "  %s→ Cancelled%s\n", render.C.Dim, render.C.RST)
		return nil
	}

	if err := mutateFn(t.ThreadNodeID); err != nil {
		return err
	}

	resolved := verb == "Resolve"
	t.Resolved = resolved
	fmt.Fprintf(w, "  %s✓ %sd%s\n", render.C.Grn, verb, render.C.RST)
	return nil
}
