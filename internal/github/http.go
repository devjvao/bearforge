package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bearforge/internal/domain"
)

const maxResponseBytes = 8 * 1024 * 1024

var (
	errUnsafeRedirect      = errors.New("unsafe GitHub redirect")
	errRateLimitedRedirect = errors.New("rate-limited GitHub redirect")
)

type apiRepository struct {
	FullName string `json:"full_name"`
}
type apiRef struct {
	Ref  string         `json:"ref"`
	Repo *apiRepository `json:"repo"`
}
type apiPullRequest struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	URL       string     `json:"html_url"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	MergedAt  *time.Time `json:"merged_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Head      apiRef     `json:"head"`
	Base      apiRef     `json:"base"`
}

func (c *Client) readPages(ctx context.Context, origin, endpoint *url.URL, q domain.PullRequestQuery, token string, host *hostState) ([]domain.PullRequest, error) {
	prs := []domain.PullRequest{}
	seen := make(map[string]bool)
	for page := 0; page < 100; page++ {
		if seen[endpoint.String()] {
			return nil, domain.E("provider_failed", "GitHub pagination repeated a page")
		}
		seen[endpoint.String()] = true
		body, header, responseURL, err := c.get(ctx, origin, endpoint, token, host)
		if err != nil {
			return nil, err
		}
		var entries []apiPullRequest
		if err := json.Unmarshal(body, &entries); err != nil || entries == nil {
			return nil, domain.E("provider_failed", "GitHub returned an invalid pull-request response")
		}
		for _, pr := range entries {
			if pr.Head.Repo == nil || pr.Base.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, q.SourceRepository) || !strings.EqualFold(pr.Base.Repo.FullName, q.Repository) || pr.Head.Ref != q.Branch || (q.Base != "" && pr.Base.Ref != q.Base) {
				continue
			}
			if pr.Number <= 0 || (pr.State != "open" && pr.State != "closed") {
				return nil, domain.E("provider_failed", "GitHub returned invalid pull-request metadata")
			}
			if q.State != "all" && pr.State != q.State {
				continue
			}
			state := pr.State
			if pr.MergedAt != nil {
				state = "merged"
			}
			prs = append(prs, domain.PullRequest{
				Number: pr.Number, Title: pr.Title, URL: pr.URL, State: state, Draft: pr.Draft,
				SourceRepository: pr.Head.Repo.FullName, TargetRepository: pr.Base.Repo.FullName,
				SourceBranch: pr.Head.Ref, TargetBranch: pr.Base.Ref, UpdatedAt: pr.UpdatedAt,
			})
		}
		next, err := nextPage(header, responseURL, origin)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return prs, nil
		}
		endpoint = next
		if c.now().Before(host.cooldown) {
			return nil, domain.E("rate_limited", "GitHub API is cooling down; retry later")
		}
	}
	return nil, domain.E("provider_failed", "GitHub pull-request lookup exceeded 100 pages")
}

func sameOrigin(origin, target *url.URL) bool {
	if target.Scheme != "https" || target.User != nil || target.Fragment != "" {
		return false
	}
	return apiHost(origin) == apiHost(target)
}

// apiHost gives HTTPS origin checks and request state one authority identity.
func apiHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func nextPage(header http.Header, current, origin *url.URL) (*url.URL, error) {
	var next *url.URL
	for _, line := range header.Values("Link") {
		parts, err := linkValues(line)
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			end := strings.Index(part, ">")
			if !strings.HasPrefix(part, "<") || end < 0 {
				return nil, domain.E("provider_failed", "GitHub returned invalid pagination")
			}
			isNext := false
			for _, param := range strings.Split(part[end+1:], ";") {
				key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
				if ok && strings.EqualFold(key, "rel") {
					for _, rel := range strings.Fields(strings.Trim(value, `"`)) {
						if rel == "next" {
							isNext = true
						}
					}
				}
			}
			if !isNext {
				continue
			}
			u, err := current.Parse(part[1:end])
			if err != nil || !sameOrigin(origin, u) || next != nil {
				return nil, domain.E("provider_failed", "GitHub returned unsafe pagination")
			}
			next = u
		}
	}
	return next, nil
}

// Link targets may contain commas; only commas outside angle brackets and
// quoted parameters separate link values.
func linkValues(line string) ([]string, error) {
	var values []string
	start := 0
	inURL, quoted, escaped := false, false, false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if quoted {
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			continue
		}
		if inURL {
			if r == '>' {
				inURL = false
			}
			continue
		}
		switch r {
		case '<':
			inURL = true
		case '"':
			quoted = true
		case ',':
			values = append(values, line[start:i])
			start = i + 1
		}
	}
	if inURL || quoted {
		return nil, domain.E("provider_failed", "GitHub returned invalid pagination")
	}
	return append(values, line[start:]), nil
}

func (c *Client) get(ctx context.Context, origin, endpoint *url.URL, token string, host *hostState) ([]byte, http.Header, *url.URL, error) {
	client := *c.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.Response != nil {
			c.observeRateLimit(req.Response, host)
		}
		if len(via) >= 10 || req.Method != http.MethodGet || !sameOrigin(origin, req.URL) {
			return errUnsafeRedirect
		}
		if c.now().Before(host.cooldown) {
			return errRateLimitedRedirect
		}
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if c.now().Before(host.cooldown) {
			return nil, nil, nil, domain.E("rate_limited", "GitHub API is cooling down; retry later")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, nil, nil, domain.E("invalid", "invalid GitHub request coordinates")
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "bearforge")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
			if errors.Is(err, errUnsafeRedirect) {
				return nil, nil, nil, domain.E("provider_failed", "GitHub returned an unsafe redirect")
			}
			if errors.Is(err, errRateLimitedRedirect) {
				return nil, nil, nil, domain.E("rate_limited", "GitHub API is cooling down; retry later")
			}
			if attempt < 2 && transientNetwork(err) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, nil, err
				}
				continue
			}
			return nil, nil, nil, domain.E("offline", "cannot reach GitHub API; check network connectivity")
		}
		header := response.Header
		status := response.StatusCode
		limited := c.observeRateLimit(response, host)
		if status != http.StatusOK {
			response.Body.Close()
			if limited {
				return nil, nil, nil, domain.E("rate_limited", "GitHub API rate limit reached; retry later")
			}
			if attempt < 2 && (status == 502 || status == 503 || status == 504) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, nil, err
				}
				continue
			}
			return nil, nil, nil, statusError(status)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		response.Body.Close()
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		if err != nil {
			if !limited && attempt < 2 && transientNetwork(err) {
				if err := retryWait(ctx, attempt); err != nil {
					return nil, nil, nil, err
				}
				continue
			}
			return nil, nil, nil, domain.E("offline", "GitHub response was interrupted; check network connectivity")
		}
		if len(body) > maxResponseBytes {
			return nil, nil, nil, domain.E("provider_failed", "GitHub response exceeded 8 MiB")
		}
		return body, header, response.Request.URL, nil
	}
	return nil, nil, nil, domain.E("provider_failed", "GitHub API request failed")
}

// Redirect responses are inspected before net/http follows them, using the
// same cooldown policy as responses returned by Do.
func (c *Client) observeRateLimit(response *http.Response, host *hostState) bool {
	header := response.Header
	limited := response.StatusCode == 429 || header.Get("Retry-After") != "" || header.Get("X-RateLimit-Remaining") == "0"
	if limited {
		host.cooldown = cooldownUntil(header, c.now())
	}
	return limited
}

func statusError(status int) error {
	switch status {
	case 401:
		return domain.E("unauthenticated", "GitHub authentication failed; check token environment variables")
	case 403:
		return domain.E("unauthorized", "GitHub access denied; check repository and token permissions")
	case 422:
		return domain.E("invalid", "GitHub rejected the pull-request query")
	default:
		return domain.E("provider_failed", "GitHub API returned HTTP %d; check repository access or retry later", status)
	}
}

func transientNetwork(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &network) && (network.Timeout() || network.Temporary()))
}

func retryWait(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func cooldownUntil(header http.Header, now time.Time) time.Time {
	var until time.Time
	if raw := header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil && seconds >= 0 {
			until = now.Add(time.Duration(seconds) * time.Second)
		} else if date, err := http.ParseTime(raw); err == nil {
			until = date
		}
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset := time.Unix(seconds, 0)
			if reset.After(until) {
				until = reset
			}
		}
	}
	if !until.After(now) {
		until = now.Add(time.Minute)
	}
	return until
}
