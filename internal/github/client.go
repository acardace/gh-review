// Package github provides API access to PR review threads using go-gh.
// It reads authentication from gh's config, so gh must be installed and
// authenticated, but no subprocess is spawned for API calls.
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/acardace/gh-review/internal/model"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

var linkNextRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Client talks to the GitHub API via go-gh.
type Client struct {
	rest        *api.RESTClient
	gql         *api.GraphQLClient
	repo        repository.Repository // effective repo (switches to upstream for forks)
	currentUser string                // cached authenticated user login
	userCh      chan string           // background user fetch channel
	allRemotes  []remoteInfo          // all parsed git remotes
}

// NewClient creates a client, auto-detecting the repo from the current
// directory. It parses git remotes locally and fetches the current user
// in parallel with any subsequent API call the caller makes.
func NewClient() (*Client, error) {
	repo, err := repository.Current()
	if err != nil {
		return nil, fmt.Errorf("not inside a GitHub repo: %w", err)
	}

	opts := api.ClientOptions{Host: repo.Host}

	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating REST client: %w", err)
	}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating GraphQL client: %w", err)
	}

	c := &Client{rest: rest, gql: gql, repo: repo}

	// Parse git remotes (local, no API call).
	allRemotes, _ := gitRemotes()
	c.allRemotes = allRemotes

	// Start fetching current user in the background.
	c.userCh = make(chan string, 1)
	go func() {
		user, _ := c.fetchCurrentUser()
		c.userCh <- user
	}()

	return c, nil
}

// resolveCurrentUser blocks until the background user fetch completes
// and caches the result. Subsequent calls return immediately.
func (c *Client) resolveCurrentUser() string {
	if c.currentUser != "" {
		return c.currentUser
	}
	if c.userCh != nil {
		c.currentUser = <-c.userCh
		c.userCh = nil // done, don't read again
	}
	return c.currentUser
}

// CurrentUser returns the authenticated user login, blocking on first
// call until the background fetch completes.
func (c *Client) CurrentUser() string {
	return c.resolveCurrentUser()
}

// Repo returns the "owner/repo" string.
func (c *Client) Repo() string {
	return c.repo.Owner + "/" + c.repo.Name
}

// fetchCurrentUser makes a single API call to get the authenticated user.
func (c *Client) fetchCurrentUser() (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := c.rest.Get("user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// -- PR info ------------------------------------------------------------------

// GetPRInfo fetches PR metadata. If prNum is 0, auto-detects from current branch.
func (c *Client) GetPRInfo(prNum int) (*model.PRInfo, error) {
	if prNum == 0 {
		return c.getPRForCurrentBranch()
	}

	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"html_url"`
		State  string `json:"state"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}

	path := fmt.Sprintf("repos/%s/%s/pulls/%d", c.repo.Owner, c.repo.Name, prNum)
	if err := c.rest.Get(path, &raw); err != nil {
		return nil, fmt.Errorf("fetching PR #%d: %w", prNum, err)
	}

	return &model.PRInfo{
		Number: raw.Number,
		Title:  raw.Title,
		URL:    raw.URL,
		State:  raw.State,
		Author: raw.User.Login,
		Base:   raw.Base.Ref,
		Head:   raw.Head.Ref,
	}, nil
}

// getPRForCurrentBranch detects the current git branch and finds the
// matching open PR. It builds an ordered list of repos to try:
// non-user remotes first (upstream, where PRs usually live in fork
// workflows), then the current repo as fallback.
func (c *Client) getPRForCurrentBranch() (*model.PRInfo, error) {
	branch, err := currentBranch()
	if err != nil {
		return nil, fmt.Errorf("detecting current branch: %w", err)
	}

	user := c.resolveCurrentUser()
	headOwner := user
	if headOwner == "" {
		headOwner = c.repo.Owner
	}

	// Build search order: non-user remotes first, then current repo.
	type candidate struct{ owner, name string }
	seen := make(map[string]bool)
	var candidates []candidate

	for _, r := range c.allRemotes {
		if r.owner == user {
			continue
		}
		key := r.owner + "/" + r.name
		if !seen[key] {
			seen[key] = true
			candidates = append(candidates, candidate{r.owner, r.name})
		}
	}

	// Add current repo last (covers non-fork workflows or self-PRs).
	key := c.repo.Owner + "/" + c.repo.Name
	if !seen[key] {
		candidates = append(candidates, candidate{c.repo.Owner, c.repo.Name})
	}

	for _, cand := range candidates {
		if pr, err := c.searchPR(cand.owner, cand.name, headOwner, branch); err == nil {
			c.repo = repository.Repository{
				Host:  c.repo.Host,
				Owner: cand.owner,
				Name:  cand.name,
			}
			return pr, nil
		}
	}

	return nil, fmt.Errorf("no open PR found for branch %q", branch)
}

// prSearchResult is the shape returned by the pulls search endpoint.
type prSearchResult struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Base   struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// searchPR queries a repo for an open PR matching the given head owner:branch.
func (c *Client) searchPR(repoOwner, repoName, headOwner, branch string) (*model.PRInfo, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls?head=%s:%s&state=open&per_page=1",
		repoOwner, repoName, headOwner, branch)

	var prs []prSearchResult
	if err := c.rest.Get(path, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no PR found")
	}

	pr := prs[0]
	return &model.PRInfo{
		Number: pr.Number,
		Title:  pr.Title,
		URL:    pr.URL,
		State:  pr.State,
		Author: pr.User.Login,
		Base:   pr.Base.Ref,
		Head:   pr.Head.Ref,
	}, nil
}

// -- Comments & Threads -------------------------------------------------------

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

// GetIssueComments fetches top-level PR conversation comments.
func (c *Client) GetIssueComments(prNum int) ([]model.Comment, error) {
	path := fmt.Sprintf("repos/%s/%s/issues/%d/comments?per_page=100", c.repo.Owner, c.repo.Name, prNum)

	var raw []issueComment
	if err := paginateREST(c, path, &raw); err != nil {
		return nil, fmt.Errorf("fetching issue comments: %w", err)
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

	go func() {
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=100", c.repo.Owner, c.repo.Name, prNum)
		var raw []reviewComment
		err := paginateREST(c, path, &raw)
		commentsCh <- commentsResult{raw, err}
	}()

	go func() {
		infos, err := c.fetchThreadInfos(prNum)
		infosCh <- infosResult{infos, err}
	}()

	cr := <-commentsCh
	if cr.err != nil {
		return nil, fmt.Errorf("fetching review comments: %w", cr.err)
	}
	ir := <-infosCh
	if ir.err != nil {
		return nil, fmt.Errorf("fetching thread metadata: %w", ir.err)
	}

	return buildThreads(cr.comments, ir.infos), nil
}

// -- Mutations ----------------------------------------------------------------

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
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/comments/%d/replies", c.repo.Owner, c.repo.Name, prNum, rootCommentID)
	payload := strings.NewReader(fmt.Sprintf(`{"body":%q}`, body))
	return c.rest.Post(path, payload, nil)
}

// -- Internal helpers ---------------------------------------------------------

func (c *Client) mutateThread(mutation, threadNodeID string) error {
	query := fmt.Sprintf(`mutation { %s(input: {threadId: %q}) { thread { isResolved } } }`, mutation, threadNodeID)
	return c.gql.Do(query, nil, nil)
}

type gqlThreadInfo struct {
	RootCommentID int64
	ThreadNodeID  string
	IsResolved    bool
}

func (c *Client) fetchThreadInfos(prNum int) ([]gqlThreadInfo, error) {
	var all []gqlThreadInfo
	var cursor *string

	for {
		afterClause := "null"
		if cursor != nil {
			afterClause = fmt.Sprintf("%q", *cursor)
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
		}`, c.repo.Owner, c.repo.Name, prNum, afterClause)

		var resp struct {
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
		}

		if err := c.gql.Do(query, nil, &resp); err != nil {
			return all, fmt.Errorf("fetching thread infos: %w", err)
		}

		for _, n := range resp.Repository.PullRequest.ReviewThreads.Nodes {
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			all = append(all, gqlThreadInfo{
				RootCommentID: n.Comments.Nodes[0].DatabaseID,
				ThreadNodeID:  n.ID,
				IsResolved:    n.IsResolved,
			})
		}

		if !resp.Repository.PullRequest.ReviewThreads.PageInfo.HasNextPage {
			break
		}
		endCursor := resp.Repository.PullRequest.ReviewThreads.PageInfo.EndCursor
		cursor = &endCursor
	}

	return all, nil
}

// -- Pagination ---------------------------------------------------------------

// paginateREST fetches all pages of a REST endpoint and appends results to out.
func paginateREST[T any](c *Client, path string, out *[]T) error {
	for path != "" {
		resp, err := c.rest.Request("GET", path, nil)
		if err != nil {
			return err
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}
		if err != nil {
			return err
		}

		if err := paginateAppend(out, data); err != nil {
			return err
		}

		path = nextPageURL(resp)
	}
	return nil
}

func paginateAppend[T any](out *[]T, data []byte) error {
	var page []T
	if err := json.Unmarshal(data, &page); err != nil {
		return err
	}
	*out = append(*out, page...)
	return nil
}

func nextPageURL(resp *http.Response) string {
	for _, m := range linkNextRE.FindAllStringSubmatch(resp.Header.Get("Link"), -1) {
		if len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

// -- Thread building ----------------------------------------------------------

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

// -- Git helpers --------------------------------------------------------------

// currentBranch returns the current git branch name.
func currentBranch() (string, error) {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "", fmt.Errorf("detached HEAD, no branch")
	}
	return branch, nil
}

type remoteInfo struct {
	remoteName string
	owner      string
	name       string
}

// gitRemotes parses `git remote -v` to extract owner/name for each remote.
func gitRemotes() ([]remoteInfo, error) {
	out, err := exec.Command("git", "remote", "-v").Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var remotes []remoteInfo
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if seen[name] {
			continue
		}
		seen[name] = true

		owner, repo := parseRemoteURL(fields[1])
		if owner != "" && repo != "" {
			remotes = append(remotes, remoteInfo{remoteName: name, owner: owner, name: repo})
		}
	}
	return remotes, nil
}

// parseRemoteURL extracts owner/name from SSH or HTTPS git URLs.
func parseRemoteURL(url string) (owner, name string) {
	// SSH: git@github.com:owner/repo.git
	if strings.Contains(url, ":") && strings.Contains(url, "@") {
		parts := strings.SplitN(url, ":", 2)
		if len(parts) == 2 {
			return parseOwnerName(parts[1])
		}
	}
	// HTTPS: https://github.com/owner/repo.git
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(url, prefix) {
			path := strings.TrimPrefix(url, prefix)
			if i := strings.IndexByte(path, '/'); i >= 0 {
				return parseOwnerName(path[i+1:])
			}
		}
	}
	return "", ""
}

// parseOwnerName splits "owner/repo.git" into owner and repo.
func parseOwnerName(path string) (string, string) {
	path = strings.TrimSuffix(path, ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok {
		return "", ""
	}
	return owner, name
}
