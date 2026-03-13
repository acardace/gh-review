// Package cmd implements the CLI entry point for gh-review.
package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/acardace/gh-review/internal/editor"
	"github.com/acardace/gh-review/internal/github"
	"github.com/acardace/gh-review/internal/interactive"
	"github.com/acardace/gh-review/internal/model"
	"github.com/acardace/gh-review/internal/render"
	"github.com/acardace/gh-review/internal/tui"
	"github.com/spf13/cobra"
)

// options holds all CLI flag values.
type options struct {
	noBots          bool
	showResolved    bool
	interactive     bool
	printMode       bool
	replyThread     int
	replyBody       string
	resolveThread   int
	unresolveThread int
}

func (o *options) hasActionFlag() bool {
	return o.resolveThread > 0 || o.unresolveThread > 0 ||
		o.replyThread > 0 || o.interactive || o.printMode
}

func NewRootCmd() *cobra.Command {
	var opts options

	root := &cobra.Command{
		Use:   "gh-review [PR_NUMBER]",
		Short: "Review PR comments in the terminal",
		Long: `Show and interact with PR review comments for the current branch.

Opens a full-screen TUI by default. Use --print for plain text output.
Resolved threads are hidden by default. Thread indexes are stable
regardless of filters, so --reply and --resolve always target the
right thread.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.OutOrStdout(), args, &opts)
		},
	}

	f := root.Flags()
	f.BoolVar(&opts.noBots, "no-bot", false, "hide bot comments")
	f.BoolVar(&opts.showResolved, "resolved", false, "include resolved threads")
	f.BoolVarP(&opts.interactive, "interactive", "i", false, "step through open threads (skip/resolve/reply)")
	f.IntVar(&opts.replyThread, "reply", 0, "reply to thread by stable index number")
	f.StringVarP(&opts.replyBody, "body", "b", "", "reply body text (used with --reply; opens $EDITOR if omitted)")
	f.IntVar(&opts.resolveThread, "resolve", 0, "resolve a thread by stable index number")
	f.IntVar(&opts.unresolveThread, "unresolve", 0, "unresolve a thread by stable index number")
	f.BoolVarP(&opts.printMode, "print", "p", false, "plain text output (no TUI)")

	// --reply, --resolve, --unresolve, -i, and -p are mutually exclusive modes.
	root.MarkFlagsMutuallyExclusive("reply", "resolve", "unresolve", "interactive", "print")
	// --body only makes sense with --reply.
	root.MarkFlagsRequiredTogether("body", "reply")
	// --resolved doesn't apply in interactive mode (only shows open).
	root.MarkFlagsMutuallyExclusive("resolved", "interactive")

	registerCompletions(root, &opts)

	return root
}

func run(w io.Writer, args []string, opts *options) error {
	prNum := 0
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[0])
		}
		prNum = n
	}

	// TUI mode: launch immediately, data loads in background.
	if !opts.hasActionFlag() {
		return tui.Run(prNum)
	}

	// Non-TUI modes need data upfront.
	client, err := github.NewClient()
	if err != nil {
		return err
	}

	pr, err := client.GetPRInfo(prNum)
	if err != nil {
		return err
	}

	threads, err := client.GetThreads(pr.Number)
	if err != nil {
		return fmt.Errorf("fetching threads: %w", err)
	}

	render.PRHeader(w, pr)

	switch {
	case opts.resolveThread > 0:
		return handleResolve(w, client, threads, opts)
	case opts.unresolveThread > 0:
		return handleUnresolve(w, client, threads, opts)
	case opts.replyThread > 0:
		return handleReply(w, client, pr.Number, threads, opts)
	case opts.interactive:
		issueComments, err := client.GetIssueComments(pr.Number)
		if err != nil {
			return fmt.Errorf("fetching comments: %w", err)
		}
		render.IssueComments(w, issueComments, opts.noBots)
		return interactive.Run(w, client, pr.Number, threads, opts.noBots)
	case opts.printMode:
		issueComments, err := client.GetIssueComments(pr.Number)
		if err != nil {
			return fmt.Errorf("fetching comments: %w", err)
		}
		return handleList(w, pr, issueComments, threads, opts)
	default:
		return nil // unreachable
	}
}

func handleResolve(w io.Writer, client *github.Client, threads []model.Thread, opts *options) error {
	return handleToggleResolve(w, client, threads, opts.resolveThread, true, opts.noBots)
}

func handleUnresolve(w io.Writer, client *github.Client, threads []model.Thread, opts *options) error {
	return handleToggleResolve(w, client, threads, opts.unresolveThread, false, opts.noBots)
}

func handleToggleResolve(w io.Writer, client *github.Client, threads []model.Thread, idx int, resolve, noBots bool) error {
	thread := findThread(threads, idx)
	if thread == nil {
		return fmt.Errorf("thread #%d not found (valid range: 1-%d)", idx, len(threads))
	}
	if thread.ThreadNodeID == "" {
		return fmt.Errorf("thread #%d has no GraphQL node ID", idx)
	}

	if thread.Resolved == resolve {
		state := "resolved"
		if !resolve {
			state = "open"
		}
		fmt.Fprintf(w, "\n  %sThread #%d is already %s.%s\n\n", render.C.Dim, thread.Index, state, render.C.RST)
		return nil
	}

	render.Thread(w, thread, noBots)

	var err error
	verb := "Resolved"
	if resolve {
		err = client.ResolveThread(thread.ThreadNodeID)
	} else {
		verb = "Unresolved"
		err = client.UnresolveThread(thread.ThreadNodeID)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  %s✓ %s thread #%d (%s:%d)%s\n\n",
		render.C.Grn, verb, thread.Index, thread.Path, thread.Line, render.C.RST)
	return nil
}

func handleReply(w io.Writer, client *github.Client, prNum int, threads []model.Thread, opts *options) error {
	thread := findThread(threads, opts.replyThread)
	if thread == nil {
		return fmt.Errorf("thread #%d not found (valid range: 1-%d)", opts.replyThread, len(threads))
	}

	body := opts.replyBody
	if body == "" {
		var err error
		body, err = editor.EditReply(thread)
		if err != nil {
			return fmt.Errorf("editor: %w", err)
		}
		if body == "" {
			fmt.Fprintf(w, "\n  %s→ Aborted (empty message)%s\n\n", render.C.Dim, render.C.RST)
			return nil
		}
	}

	render.Thread(w, thread, opts.noBots)

	if err := client.ReplyToThread(prNum, thread.RootID, body); err != nil {
		return fmt.Errorf("replying: %w", err)
	}

	fmt.Fprintf(w, "\n  %s✓ Reply posted to thread #%d (%s:%d)%s\n\n",
		render.C.Grn, thread.Index, thread.Path, thread.Line, render.C.RST)
	return nil
}

func handleList(w io.Writer, pr *model.PRInfo, issueComments []model.Comment, threads []model.Thread, opts *options) error {
	render.IssueComments(w, issueComments, opts.noBots)

	open, resolved := countThreads(threads)
	total := len(threads)

	if total == 0 {
		fmt.Fprintf(w, "\n%sNo review comments.%s\n\n", render.C.Dim, render.C.RST)
		return nil
	}

	render.ThreadsSummary(w, total, open, resolved, opts.showResolved)

	for i := range threads {
		t := &threads[i]
		if !opts.showResolved && t.Resolved {
			continue
		}
		if opts.noBots && model.AllBots(t.Comments) {
			continue
		}
		render.Thread(w, t, opts.noBots)
	}

	render.Footer(w, pr.Number, total, open, resolved, opts.showResolved)
	return nil
}

func findThread(threads []model.Thread, idx int) *model.Thread {
	for i := range threads {
		if threads[i].Index == idx {
			return &threads[i]
		}
	}
	return nil
}

func countThreads(threads []model.Thread) (open, resolved int) {
	for _, t := range threads {
		if t.Resolved {
			resolved++
		} else {
			open++
		}
	}
	return
}
