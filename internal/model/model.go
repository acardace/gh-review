// Package model defines the core data types for PR review threads.
// These types are independent of rendering or API transport so they
// can be reused across CLI, TUI, or any other frontend.
package model

import "time"

// Comment represents a single review or issue comment.
type Comment struct {
	ID        int64
	User      string
	UserType  string // "User" or "Bot"
	Body      string
	CreatedAt time.Time
	HTMLURL   string
}

// IsBot returns true if the comment was left by a bot account.
func (c *Comment) IsBot() bool {
	return c.UserType == "Bot"
}

// Thread is a review comment thread anchored to a file location.
// Index is the stable 1-based position in the full sorted thread
// list — it never changes regardless of display filters.
type Thread struct {
	Index        int    // stable 1-based index
	RootID       int64  // REST API id of root comment
	ThreadNodeID string // GraphQL node id (for resolve/unresolve mutations)
	Path         string
	Line         int
	DiffHunk     string
	Resolved     bool
	Comments     []Comment
}

// PRInfo holds metadata about the pull request.
type PRInfo struct {
	Number int
	Title  string
	URL    string
	State  string
	Author string
	Base   string
	Head   string
}

// AllBots reports whether every comment in the slice was left by a bot.
func AllBots(comments []Comment) bool {
	for i := range comments {
		if !comments[i].IsBot() {
			return false
		}
	}
	return true
}

// NeedsReply reports whether the last comment in the thread was left
// by someone other than currentUser, indicating a reply is expected.
func (t *Thread) NeedsReply(currentUser string) bool {
	if currentUser == "" || len(t.Comments) == 0 {
		return false
	}
	return t.Comments[len(t.Comments)-1].User != currentUser
}
