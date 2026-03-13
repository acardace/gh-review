// Package editor provides git-style editor interaction for composing
// replies. It opens $EDITOR with a temporary file pre-populated with
// context (commented out), and returns the uncommented text.
package editor

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/acardace/gh-review/internal/model"
)

// EditReply opens the user's editor with thread context shown as
// # comments. The user writes their reply above the comment block.
// Lines starting with # are stripped from the result, exactly like
// git commit messages.
//
// Returns the reply body, or empty string if the user aborted
// (saved an empty/comment-only file).
func EditReply(thread *model.Thread) (string, error) {
	tmp, err := os.CreateTemp("", "gh-review-*.md")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)

	if _, err := tmp.WriteString(buildTemplate(thread)); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	tmp.Close()

	if err := openEditor(path); err != nil {
		return "", fmt.Errorf("editor failed: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading temp file: %w", err)
	}

	return stripComments(string(data)), nil
}

// openEditor launches the user's preferred editor ($EDITOR, $VISUAL, or vi).
func openEditor(path string) error {
	editor := cmp.Or(os.Getenv("EDITOR"), os.Getenv("VISUAL"), "vi")

	// Split in case of args (e.g. "code --wait").
	parts := append(strings.Fields(editor), path)

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildTemplate creates the editor buffer with thread context as # comments.
func buildTemplate(thread *model.Thread) string {
	var b strings.Builder

	b.WriteString("\n")
	writeln := func(s string) { b.WriteString("# " + s + "\n") }

	writeln("────────────────────────────────────────────────")
	writeln("Write your reply above. Lines starting with '#'")
	writeln("will be ignored (like git commit messages).")
	writeln("Save an empty file to abort.")
	writeln("────────────────────────────────────────────────")
	writeln("")
	writeln(fmt.Sprintf("Thread #%d — %s:%d", thread.Index, thread.Path, thread.Line))
	writeln("")

	if thread.DiffHunk != "" {
		writeln("Diff context:")
		for _, line := range strings.Split(thread.DiffHunk, "\n") {
			writeln("  " + line)
		}
		writeln("")
	}

	for _, c := range thread.Comments {
		tag := ""
		if c.IsBot() {
			tag = " [bot]"
		}
		writeln(fmt.Sprintf("%s%s  %s", c.User, tag, c.CreatedAt.Format("2006-01-02 15:04")))
		writeln("")
		for _, line := range strings.Split(c.Body, "\n") {
			writeln("  " + line)
		}
		writeln("")
	}

	return b.String()
}

// stripComments removes lines starting with # and trims the result.
func stripComments(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
