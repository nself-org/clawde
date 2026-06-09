// github.go — GitHub API client for discourse intelligence.
//
// Purpose:    GitHubClient lists issues, PRs, commits, and PR review comments via
//             the GitHub REST v3 API (Link-header pagination) and the GraphQL v4
//             API (cursor pagination). A token-bucket rate limiter caps requests
//             at 70 req/min. On HTTP 429/403 (rate/abuse) the client backs off
//             exponentially starting at 60s, honoring the Retry-After header
//             exactly when present.
// Inputs:     CLAWDE_GITHUB_TOKEN (from env, never hardcoded); owner/repo args.
// Outputs:    Normalized []Issue / []PR / []Commit / []ReviewComment.
// Constraints: File ≤500 lines. Real token only from env. HTTP transport is an
//             interface seam (httptest mock in tests).
//
// SPORT: REGISTRY-FUNCTIONS.md → discourse.GitHubClient.
package discourse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	// githubRateLimit caps requests at 70 per minute (canonical).
	githubRateLimit = 70
	// defaultBaseURL is the GitHub REST v3 root; overridable for httptest.
	defaultBaseURL = "https://api.github.com"
	// defaultGraphQL is the GitHub GraphQL v4 endpoint; overridable for httptest.
	defaultGraphQL = "https://api.github.com/graphql"
	// backoffStart is the initial exponential backoff on 429/403 (canonical 60s).
	backoffStart = 60 * time.Second
	// maxBackoffRetries bounds the retry loop.
	maxBackoffRetries = 5
	// perPage is the REST page size.
	perPage = 100
)

// doer is the HTTP transport seam; *http.Client satisfies it. Tests inject mocks.
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// GitHubClient talks to the GitHub REST + GraphQL APIs under a shared rate limiter.
type GitHubClient struct {
	token    string
	baseURL  string
	graphQL  string
	http     doer
	limiter  *rate.Limiter
	sleep    func(time.Duration) // injectable for fast backoff tests
}

// NewGitHubClient builds a client from a token. baseURL/graphQL default to the
// public API; pass overrides for httptest. The limiter is fixed at 70/min.
func NewGitHubClient(token string, opts ...Option) *GitHubClient {
	c := &GitHubClient{
		token:   token,
		baseURL: defaultBaseURL,
		graphQL: defaultGraphQL,
		http:    &http.Client{Timeout: 30 * time.Second},
		// 70/min ≈ one token every 60/70 s; burst of 70 allows initial fan-out.
		limiter: rate.NewLimiter(rate.Limit(float64(githubRateLimit)/60.0), githubRateLimit),
		sleep:   time.Sleep,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option mutates a GitHubClient at construction.
type Option func(*GitHubClient)

// WithBaseURL overrides the REST root (httptest).
func WithBaseURL(u string) Option { return func(c *GitHubClient) { c.baseURL = u } }

// WithGraphQLURL overrides the GraphQL endpoint (httptest).
func WithGraphQLURL(u string) Option { return func(c *GitHubClient) { c.graphQL = u } }

// WithHTTPClient injects a custom transport (httptest mock).
func WithHTTPClient(d doer) Option { return func(c *GitHubClient) { c.http = d } }

// WithSleep injects a sleep fn so backoff tests run instantly.
func WithSleep(f func(time.Duration)) Option { return func(c *GitHubClient) { c.sleep = f } }

// WithLimiter overrides the rate limiter (tests verifying throttle behavior).
func WithLimiter(l *rate.Limiter) Option { return func(c *GitHubClient) { c.limiter = l } }

// do performs a request under the rate limiter, retrying on 429/403 with
// exponential backoff (start 60s) honoring Retry-After exactly. Returns the body
// bytes and the response (caller may read Link header).
func (c *GitHubClient) do(ctx context.Context, method, url, body string) ([]byte, *http.Response, error) {
	backoff := backoffStart
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, nil, err
		}
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rdr)
		if err != nil {
			return nil, nil, err
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, nil, err
		}
		// Rate-limit / abuse responses → backoff and retry.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if attempt >= maxBackoffRetries {
				return nil, nil, fmt.Errorf("github: rate limited after %d retries: %s", attempt, strings.TrimSpace(string(b)))
			}
			wait := backoff
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, perr := strconv.Atoi(strings.TrimSpace(ra)); perr == nil {
					wait = time.Duration(secs) * time.Second // honored exactly
				}
			}
			c.sleep(wait)
			backoff *= 2
			continue
		}
		b, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, resp, rerr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, resp, fmt.Errorf("github: http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		return b, resp, nil
	}
}

// nextLink parses an RFC-5988 Link header and returns the rel="next" URL, or "".
var linkRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(linkHeader string) string {
	m := linkRe.FindStringSubmatch(linkHeader)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// ── REST listing (Link-header pagination) ───────────────────────────────────

type restIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct{} `json:"pull_request"` // present ⇒ it's a PR, skip for issues
}

// ListIssues returns all issues for owner/repo (excluding PRs) via REST,
// following Link-header next pages.
func (c *GitHubClient) ListIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues?state=all&per_page=%d", c.baseURL, owner, repo, perPage)
	var out []Issue
	for url != "" {
		b, resp, err := c.do(ctx, http.MethodGet, url, "")
		if err != nil {
			return nil, err
		}
		var page []restIssue
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("github: decode issues: %w", err)
		}
		for _, ri := range page {
			if ri.PullRequest != nil {
				continue // /issues includes PRs; skip them here
			}
			out = append(out, Issue{
				Number: ri.Number, Title: ri.Title, Body: ri.Body, State: ri.State,
				URL: ri.HTMLURL, Author: ri.User.Login, Repo: owner + "/" + repo,
				CreatedAt: ri.CreatedAt, UpdatedAt: ri.UpdatedAt,
			})
		}
		url = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

type restPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// ListPRs returns all pull requests for owner/repo via REST (Link pagination).
// Linked issues are extracted from the PR body via closing keywords.
func (c *GitHubClient) ListPRs(ctx context.Context, owner, repo string) ([]PR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls?state=all&per_page=%d", c.baseURL, owner, repo, perPage)
	var out []PR
	for url != "" {
		b, resp, err := c.do(ctx, http.MethodGet, url, "")
		if err != nil {
			return nil, err
		}
		var page []restPR
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("github: decode prs: %w", err)
		}
		for _, rp := range page {
			out = append(out, PR{
				Number: rp.Number, Title: rp.Title, Body: rp.Body, State: rp.State,
				URL: rp.HTMLURL, Author: rp.User.Login, Repo: owner + "/" + repo,
				CreatedAt: rp.CreatedAt, UpdatedAt: rp.UpdatedAt,
				LinkedIssues: ExtractLinkedIssues(rp.Body),
			})
		}
		url = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

type restCommit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Files []struct {
		Filename string `json:"filename"`
	} `json:"files"`
}

// ListCommits returns commits for owner/repo via REST (Link pagination). The
// list endpoint omits per-commit files; ChangedFiles is populated only when the
// payload includes them (single-commit fetches do).
func (c *GitHubClient) ListCommits(ctx context.Context, owner, repo string) ([]Commit, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits?per_page=%d", c.baseURL, owner, repo, perPage)
	var out []Commit
	for url != "" {
		b, resp, err := c.do(ctx, http.MethodGet, url, "")
		if err != nil {
			return nil, err
		}
		var page []restCommit
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("github: decode commits: %w", err)
		}
		for _, rc := range page {
			files := make([]string, 0, len(rc.Files))
			for _, f := range rc.Files {
				files = append(files, f.Filename)
			}
			out = append(out, Commit{
				SHA: rc.SHA, Message: rc.Commit.Message, Author: rc.Commit.Author.Name,
				URL: rc.HTMLURL, Repo: owner + "/" + repo,
				CommittedAt: rc.Commit.Author.Date, ChangedFiles: files,
			})
		}
		url = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

type restReviewComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPRReviewComments returns inline review comments for a PR via REST.
func (c *GitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, pr int) ([]ReviewComment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=%d", c.baseURL, owner, repo, pr, perPage)
	var out []ReviewComment
	for url != "" {
		b, resp, err := c.do(ctx, http.MethodGet, url, "")
		if err != nil {
			return nil, err
		}
		var page []restReviewComment
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("github: decode review comments: %w", err)
		}
		for _, rc := range page {
			out = append(out, ReviewComment{
				ID: rc.ID, PRNumber: pr, Body: rc.Body, Author: rc.User.Login,
				URL: rc.HTMLURL, Repo: owner + "/" + repo, Path: rc.Path, CreatedAt: rc.CreatedAt,
			})
		}
		url = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

// ── GraphQL listing (cursor pagination) ─────────────────────────────────────

const issuesGraphQLQuery = `query($owner:String!,$repo:String!,$cursor:String){repository(owner:$owner,name:$repo){issues(first:100,after:$cursor){pageInfo{hasNextPage endCursor} nodes{number title body state url createdAt updatedAt author{login}}}}}`

type gqlIssuesResp struct {
	Data struct {
		Repository struct {
			Issues struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					Number    int       `json:"number"`
					Title     string    `json:"title"`
					Body      string    `json:"body"`
					State     string    `json:"state"`
					URL       string    `json:"url"`
					CreatedAt time.Time `json:"createdAt"`
					UpdatedAt time.Time `json:"updatedAt"`
					Author    struct {
						Login string `json:"login"`
					} `json:"author"`
				} `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ListIssuesGraphQL returns all issues for owner/repo via GraphQL cursor paging.
func (c *GitHubClient) ListIssuesGraphQL(ctx context.Context, owner, repo string) ([]Issue, error) {
	var out []Issue
	cursor := ""
	for {
		vars := map[string]any{"owner": owner, "repo": repo}
		if cursor != "" {
			vars["cursor"] = cursor
		} else {
			vars["cursor"] = nil
		}
		payload, _ := json.Marshal(map[string]any{"query": issuesGraphQLQuery, "variables": vars})
		b, _, err := c.do(ctx, http.MethodPost, c.graphQL, string(payload))
		if err != nil {
			return nil, err
		}
		var r gqlIssuesResp
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("github: decode graphql issues: %w", err)
		}
		if len(r.Errors) > 0 {
			return nil, fmt.Errorf("github graphql: %s", r.Errors[0].Message)
		}
		for _, n := range r.Data.Repository.Issues.Nodes {
			out = append(out, Issue{
				Number: n.Number, Title: n.Title, Body: n.Body, State: n.State,
				URL: n.URL, Author: n.Author.Login, Repo: owner + "/" + repo,
				CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
			})
		}
		pi := r.Data.Repository.Issues.PageInfo
		if !pi.HasNextPage || pi.EndCursor == "" {
			break
		}
		cursor = pi.EndCursor
	}
	return out, nil
}

// closingKeywordRe matches GitHub closing keywords + #N references.
var closingKeywordRe = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)

// refRe matches any bare #N issue reference.
var refRe = regexp.MustCompile(`#(\d+)`)

// ExtractLinkedIssues returns issue numbers referenced by closing keywords in
// the given body (deduped, order-preserving). Falls back to bare #N refs.
func ExtractLinkedIssues(body string) []int {
	seen := map[int]bool{}
	var out []int
	add := func(matches [][]string) {
		for _, m := range matches {
			n, err := strconv.Atoi(m[1])
			if err != nil || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	add(closingKeywordRe.FindAllStringSubmatch(body, -1))
	if len(out) == 0 {
		add(refRe.FindAllStringSubmatch(body, -1))
	}
	return out
}
