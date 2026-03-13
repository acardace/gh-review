package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/acardace/gh-review/internal/github"
	"github.com/acardace/gh-review/internal/model"
	"github.com/spf13/cobra"
)

// registerCompletions sets up dynamic shell completions for flags that
// take thread indexes (--reply, --resolve). When the user presses tab,
// the completion function fetches current PR threads and lists them
// with the first comment body as description.
func registerCompletions(root *cobra.Command, _ *options) {
	root.RegisterFlagCompletionFunc("reply", completeOpenThreads)
	root.RegisterFlagCompletionFunc("resolve", completeOpenThreads)
	root.RegisterFlagCompletionFunc("unresolve", completeResolvedThreads)
	root.AddCommand(completionCmd())
}

func completeResolvedThreads(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeThreads(func(t *model.Thread) bool { return t.Resolved })
}

func completeOpenThreads(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeThreads(func(t *model.Thread) bool { return !t.Resolved })
}

func completeThreads(filter func(*model.Thread) bool) ([]string, cobra.ShellCompDirective) {
	threads, err := fetchCurrentThreads()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var completions []string
	for _, t := range threads {
		if !filter(&t) || len(t.Comments) == 0 {
			continue
		}
		c := t.Comments[0]
		body := strings.Join(strings.Fields(c.Body), " ")
		desc := fmt.Sprintf("[#%d %s:%d] @%s: %s", t.Index, t.Path, t.Line, c.User, body)
		completions = append(completions, fmt.Sprintf("%d\t%s", t.Index, desc))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func fetchCurrentThreads() ([]model.Thread, error) {
	client, err := github.NewClient()
	if err != nil {
		return nil, err
	}
	pr, err := client.GetPRInfo(0)
	if err != nil {
		return nil, err
	}
	return client.GetThreads(pr.Number)
}

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate or install shell completions",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:       "generate [bash|zsh|fish]",
			Short:     "Print completion script to stdout",
			Args:      cobra.ExactArgs(1),
			ValidArgs: []string{"bash", "zsh", "fish"},
			RunE: func(c *cobra.Command, args []string) error {
				return generateCompletion(c.Root(), os.Stdout, args[0])
			},
		},
		&cobra.Command{
			Use:   "install",
			Short: "Install completions for the current shell",
			Long: `Detects your shell and writes the completion script to the
standard location:

  fish:  ~/.config/fish/completions/gh-review.fish
  bash:  ~/.local/share/bash-completion/completions/gh-review
  zsh:   ~/.zsh/completions/_gh-review (adds to fpath)`,
			RunE: func(c *cobra.Command, _ []string) error {
				return installCompletion(c.Root())
			},
		},
	)

	return cmd
}

func generateCompletion(root *cobra.Command, w *os.File, shell string) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(w, true)
	case "zsh":
		return root.GenZshCompletion(w)
	case "fish":
		return root.GenFishCompletion(w, true)
	default:
		return fmt.Errorf("unsupported shell: %s (use bash, zsh, or fish)", shell)
	}
}

func installCompletion(root *cobra.Command) error {
	shell := detectShell()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("detecting home directory: %w", err)
	}

	var path string
	switch shell {
	case "fish":
		path = filepath.Join(home, ".config", "fish", "completions", "gh-review.fish")
	case "bash":
		path = filepath.Join(home, ".local", "share", "bash-completion", "completions", "gh-review")
	case "zsh":
		path = filepath.Join(home, ".zsh", "completions", "_gh-review")
	default:
		return fmt.Errorf("unsupported shell %q; use 'completion generate' and source it manually", shell)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	if err := generateCompletion(root, f, shell); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Installed %s completions to %s\n", shell, path)

	switch shell {
	case "bash":
		fmt.Fprintln(os.Stdout, "Completions will be loaded on next login, or run:")
		fmt.Fprintf(os.Stdout, "  source %s\n", path)
	case "zsh":
		fmt.Fprintln(os.Stdout, "Add to your .zshrc if not already present:")
		fmt.Fprintf(os.Stdout, "  fpath=(~/.zsh/completions $fpath)\n  autoload -Uz compinit && compinit\n")
	}

	return nil
}

func detectShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return filepath.Base(s)
	}
	return ""
}
