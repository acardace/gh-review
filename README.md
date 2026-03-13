# gh-review

A `gh` CLI extension for reviewing pull request comments in the terminal.

It groups review comments into threads, shows diff context, and lets you
comment, resolve, or unresolve threads without leaving the terminal.
Opens a full-screen TUI by default. Resolved threads are hidden by
default. Thread indexes are stable regardless of filters.

## Install

```bash
# As a gh extension
gh extension install acardace/gh-review

# Or build from source
go install github.com/acardace/gh-review@latest

# Shell completions (fish/bash/zsh)
gh-review completion install
```

## Usage

```
gh-review [PR_NUMBER] [flags]
```

If `PR_NUMBER` is omitted, the PR for the current branch is used.
Running with no flags opens the TUI.

### Flags

| Flag | Description |
|---|---|
| `-p`, `--print` | Plain text output (no TUI) |
| `--resolved` | Include resolved threads |
| `--no-bot` | Hide bot comments |
| `-i`, `--interactive` | Step through open threads one by one |
| `--reply NUM` | Comment on a thread by index |
| `--resolve NUM` | Resolve a thread by index |
| `--unresolve NUM` | Unresolve a thread by index |
| `-b`, `--body TEXT` | Comment body (used with `--reply`; opens `$EDITOR` if omitted) |

### Examples

```bash
# Open TUI for the current branch's PR
gh-review

# Plain text listing
gh-review -p

# Plain text with resolved threads
gh-review -p --resolved

# Comment on thread #3 (opens $EDITOR with thread context)
gh-review --reply 3

# Comment inline
gh-review --reply 3 -b "Fixed in latest push"

# Resolve thread #2
gh-review --resolve 2

# Unresolve thread #1
gh-review --unresolve 1

# Interactive review: walk through each open thread
gh-review -i

# Specific PR number
gh-review 415
```

### TUI

The default mode. Colors use the terminal's ANSI palette, so they
respect your light/dark theme.

**List view** -- threads with status and first comment preview.

- `j`/`k` -- navigate
- `Enter` -- open thread detail
- `c` -- comment (opens `$EDITOR`)
- `r` -- resolve (asks for confirmation)
- `u` -- unresolve (asks for confirmation)
- `Tab` -- toggle resolved threads
- `b` -- toggle bot comments
- `q` -- quit

**Detail view** -- diff hunk and all comments in a scrollable viewport.

- `j`/`k` -- scroll
- `Ctrl+d`/`Ctrl+u` -- half-page scroll
- `c` -- comment
- `r` -- resolve
- `u` -- unresolve
- `Esc` -- back to list
- `q` -- quit

### Interactive mode

Interactive mode (`-i`) presents each open thread and prompts for an action:

- **s** -- skip
- **r** -- resolve (asks for confirmation)
- **u** -- unresolve (asks for confirmation)
- **c** -- comment (opens `$EDITOR`)
- **q** -- quit

After commenting, you stay on the same thread so you can also resolve it.

### Editor

When commenting without `-b`, `$EDITOR` opens with the thread context shown
as `#`-prefixed lines (like `git commit`). Write your comment above the
context, save, and quit. Lines starting with `#` are stripped. Saving an
empty file aborts.

### Shell completions

`--reply`, `--resolve`, and `--unresolve` support tab completion with thread
descriptions showing the author and first comment:

```
$ gh-review --reply <TAB>
1  [#1 main.go:68] @reviewer: This silently tries multiple formats...
2  [#2 main.go:471] @reviewer: Nice simplification! Though note that...

$ gh-review --unresolve <TAB>
3  [#3 main.go:560] @reviewer: Consider using a proper flag parsing...
```

`--unresolve` only shows resolved threads; `--reply` and `--resolve` only
show open threads.

## Requirements

- [gh](https://cli.github.com/) (authenticated — gh-review reads its auth config)
- `git` (for current branch detection)
- Go 1.22+ (build only)
