// Package github provides API access to PR review threads using the gh CLI.
// It shells out to `gh` for authentication and API calls, keeping the
// dependency footprint minimal.
package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/acardace/gh-review/internal/model"
)

// Client talks to the GitHub API via the gh CLI.
type Client struct {
	repo string // "owner/repo"
}

// NewClient creates a client, auto-detecting the repo from the current directory.
func NewClient() (*Client, error) {
	out, err := ghExec("repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	if err != nil {
		return nil, fmt.Errorf("not inside a GitHub repo: %w", err)
	}
	return &Client{repo: strings.TrimSpace(string(out))}, nil
}

// Repo returns the owner/repo string.
func (c *Client) Repo() string { return c.repo }

// CurrentUser returns the authenticated user's login.
func (c *Client) CurrentUser() (string, error) {
	out, err := ghExec("api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

const prFields = "number,title,url,state,author,baseRefName,headRefName"

// GetPRInfo fetches PR metadata. If prNum is 0, auto-detects from current branch.
func (c *Client) GetPRInfo(prNum int) (*model.PRInfo, error) {
	args := []string{"pr", "view"}
	if prNum > 0 {
		args = append(args, strconv.Itoa(prNum))
	}
	args = append(args, "--json", prFields)

	out, err := ghExec(args...)
	if err != nil {
		return nil, fmt.Errorf("no PR found: %w", err)
	}

	var raw struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		State       string `json:"state"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing PR info: %w", err)
	}

	return &model.PRInfo{
		Number: raw.Number,
		Title:  raw.Title,
		URL:    raw.URL,
		State:  raw.State,
		Author: raw.Author.Login,
		Base:   raw.BaseRefName,
		Head:   raw.HeadRefName,
	}, nil
}

// -- REST API types -----------------------------------------------------------

type reviewComment struct {
	ID          int64  `json:"id"`
	InReplyToID *int64 `json:"in_reply_to_id"`
	Path        string `json:"path"`
	Line        *int   `json:"line"`
	OrigLine    *int   `json:"original_line"`
	DiffHunk    string `json:"diff_hunk"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
	HTMLURL     string `json:"html_url"`
	User        struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type issueComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
	User      struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// -- GraphQL types ------------------------------------------------------------

type gqlThreadInfo struct {
	RootCommentID int64
	ThreadNodeID  string
	IsResolved    bool
}

// -- Public API ---------------------------------------------------------------

// GetIssueComments fetches top-level PR conversation comments.
func (c *Client) GetIssueComments(prNum int) ([]model.Comment, error) {
	out, err := ghExec("api", fmt.Sprintf("repos/%s/issues/%d/comments", c.repo, prNum), "--paginate")
	if err != nil {
		return nil, fmt.Errorf("fetching issue comments: %w", err)
	}

	var raw []issueComment
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing issue comments: %w", err)
	}

	comments := make([]model.Comment, 0, len(raw))
	for _, r := range raw {
		comments = append(comments, toModelComment(r.ID, r.User.Login, r.User.Type, r.Body, r.CreatedAt, r.HTMLURL))
	}
	return comments, nil
}

// GetThreads fetches review comments and thread metadata in parallel,
// then assembles them into sorted threads with stable indexes.
func (c *Client) GetThreads(prNum int) ([]model.Thread, error) {
	type commentsResult struct {
		comments []reviewComment
		err      error
	}
	type infosResult struct {
		infos []gqlThreadInfo
		err   error
	}

	commentsCh := make(chan commentsResult, 1)
	infosCh := make(chan infosResult, 1)

	// Fetch REST comments and GraphQL thread infos concurrently.
	go func() {
		out, err := ghExec("api", fmt.Sprintf("repos/%s/pulls/%d/comments", c.repo, prNum), "--paginate")
		if err != nil {
			commentsCh <- commentsResult{err: fmt.Errorf("fetching review comments: %w", err)}
			return
		}
		var raw []reviewComment
		if err := json.Unmarshal(out, &raw); err != nil {
			commentsCh <- commentsResult{err: fmt.Errorf("parsing review comments: %w", err)}
			return
		}
		commentsCh <- commentsResult{comments: raw}
	}()

	go func() {
		infos, err := c.fetchThreadInfos(prNum)
		if err != nil {
			infosCh <- infosResult{err: fmt.Errorf("fetching thread metadata: %w", err)}
			return
		}
		infosCh <- infosResult{infos: infos}
	}()

	cr := <-commentsCh
	if cr.err != nil {
		return nil, cr.err
	}
	ir := <-infosCh
	if ir.err != nil {
		return nil, ir.err
	}

	return buildThreads(cr.comments, ir.infos), nil
}

// ResolveThread resolves a review thread via GraphQL mutation.
func (c *Client) ResolveThread(threadNodeID string) error {
	return c.mutateThread("resolveReviewThread", threadNodeID)
}

// UnresolveThread unresolves a review thread via GraphQL mutation.
func (c *Client) UnresolveThread(threadNodeID string) error {
	return c.mutateThread("unresolveReviewThread", threadNodeID)
}

// ReplyToThread posts a reply to a review comment thread.
func (c *Client) ReplyToThread(prNum int, rootCommentID int64, body string) error {
	_, err := ghExec("api",
		fmt.Sprintf("repos/%s/pulls/%d/comments/%d/replies", c.repo, prNum, rootCommentID),
		"-f", "body="+body,
	)
	return err
}

// -- Internal helpers ---------------------------------------------------------

func (c *Client) mutateThread(mutation, threadNodeID string) error {
	query := fmt.Sprintf(`mutation { %s(input: {threadId: %q}) { thread { isResolved } } }`, mutation, threadNodeID)
	_, err := ghExec("api", "graphql", "-f", "query="+query)
	return err
}

func (c *Client) fetchThreadInfos(prNum int) ([]gqlThreadInfo, error) {
	owner, repoName := splitRepo(c.repo)
	var all []gqlThreadInfo
	var cursor string

	for {
		afterClause := "null"
		if cursor != "" {
			afterClause = fmt.Sprintf("%q", cursor)
		}

		query := fmt.Sprintf(`query {
			repository(owner: %q, name: %q) {
				pullRequest(number: %d) {
					reviewThreads(first: 100, after: %s) {
						pageInfo { hasNextPage endCursor }
						nodes {
							id
							isResolved
							comments(first: 1) { nodes { databaseId } }
						}
					}
				}
			}
		}`, owner, repoName, prNum, afterClause)

		out, err := ghExec("api", "graphql", "-f", "query="+query)
		if err != nil {
			return all, fmt.Errorf("fetching thread infos: %w", err)
		}

		var resp struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
							Nodes []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
								Comments   struct {
									Nodes []struct {
										DatabaseID int64 `json:"databaseId"`
									} `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			return all, fmt.Errorf("parsing thread infos: %w", err)
		}

		for _, n := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			all = append(all, gqlThreadInfo{
				RootCommentID: n.Comments.Nodes[0].DatabaseID,
				ThreadNodeID:  n.ID,
				IsResolved:    n.IsResolved,
			})
		}

		if !resp.Data.Repository.PullRequest.ReviewThreads.PageInfo.HasNextPage {
			break
		}
		cursor = resp.Data.Repository.PullRequest.ReviewThreads.PageInfo.EndCursor
	}

	return all, nil
}

// buildThreads groups raw comments into threads using GraphQL metadata.
func buildThreads(rawComments []reviewComment, infos []gqlThreadInfo) []model.Thread {
	infoByRoot := make(map[int64]gqlThreadInfo, len(infos))
	for _, ti := range infos {
		infoByRoot[ti.RootCommentID] = ti
	}

	type threadBuild struct {
		root    reviewComment
		replies []reviewComment
		info    gqlThreadInfo
	}

	roots := make(map[int64]*threadBuild)
	var rootOrder []int64

	for i := range rawComments {
		rc := &rawComments[i]
		if rc.InReplyToID == nil {
			rootOrder = append(rootOrder, rc.ID)
			roots[rc.ID] = &threadBuild{
				root: *rc,
				info: infoByRoot[rc.ID],
			}
		}
	}

	for i := range rawComments {
		rc := &rawComments[i]
		if rc.InReplyToID != nil {
			if tb, ok := roots[*rc.InReplyToID]; ok {
				tb.replies = append(tb.replies, *rc)
			}
		}
	}

	result := make([]model.Thread, 0, len(rootOrder))
	for _, rootID := range rootOrder {
		tb := roots[rootID]

		line := derefOr(tb.root.Line, derefOr(tb.root.OrigLine, 0))

		all := make([]reviewComment, 0, 1+len(tb.replies))
		all = append(all, tb.root)
		all = append(all, tb.replies...)
		sort.Slice(all, func(i, j int) bool {
			return all[i].CreatedAt < all[j].CreatedAt
		})

		comments := make([]model.Comment, 0, len(all))
		for _, rc := range all {
			comments = append(comments, toModelComment(rc.ID, rc.User.Login, rc.User.Type, rc.Body, rc.CreatedAt, rc.HTMLURL))
		}

		result = append(result, model.Thread{
			RootID:       rootID,
			ThreadNodeID: tb.info.ThreadNodeID,
			Path:         tb.root.Path,
			Line:         line,
			DiffHunk:     tb.root.DiffHunk,
			Resolved:     tb.info.IsResolved,
			Comments:     comments,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Comments[0].CreatedAt.Before(result[j].Comments[0].CreatedAt)
	})

	for i := range result {
		result[i].Index = i + 1
	}

	return result
}

func toModelComment(id int64, user, userType, body, createdAt, htmlURL string) model.Comment {
	t, _ := time.Parse(time.RFC3339, createdAt)
	return model.Comment{
		ID:        id,
		User:      user,
		UserType:  userType,
		Body:      body,
		CreatedAt: t,
		HTMLURL:   htmlURL,
	}
}

func derefOr(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

// ghExec runs `gh` with the given arguments and returns stdout.
func ghExec(args ...string) ([]byte, error) {
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func splitRepo(repo string) (owner, name string) {
	if i := strings.IndexByte(repo, '/'); i >= 0 {
		return repo[:i], repo[i+1:]
	}
	return repo, ""
}
