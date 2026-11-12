package bitbucket

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"bearforge/internal/domain"
)

func lookupEndpoint(origin *url.URL, q domain.PullRequestQuery) *url.URL {
	endpoint := *origin
	prefix := strings.TrimRight(endpoint.Path, "/")
	if q.Provider == "bitbucket-server" {
		project, repository, _ := strings.Cut(q.Repository, "/")
		endpoint.Path = prefix + "/projects/" + project + "/repos/" + repository + "/pull-requests"
		state := "ALL"
		if q.State == "open" {
			state = "OPEN"
		}
		values := url.Values{"direction": {"INCOMING"}, "limit": {"100"}, "start": {"0"}, "order": {"NEWEST"}, "state": {state}}
		if q.Base != "" {
			values.Set("at", "refs/heads/"+q.Base)
		}
		endpoint.RawQuery = values.Encode()
	} else {
		endpoint.Path = prefix + "/repositories/" + q.Repository + "/pullrequests"
		filter := "source.branch.name = " + strconv.Quote(q.Branch)
		if q.Base != "" {
			filter += " AND destination.branch.name = " + strconv.Quote(q.Base)
		}
		states := []string{"OPEN"}
		if q.State == "closed" {
			states = []string{"MERGED", "DECLINED", "SUPERSEDED"}
		}
		if q.State == "all" {
			states = []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}
		}
		endpoint.RawQuery = url.Values{"pagelen": {"100"}, "sort": {"-updated_on"}, "state": states, "q": {filter}}.Encode()
	}
	endpoint.RawPath = ""
	return &endpoint
}

func (c *Client) readPages(ctx context.Context, origin, endpoint *url.URL, q domain.PullRequestQuery, token string, host *hostState) ([]domain.PullRequest, error) {
	prs := []domain.PullRequest{}
	seen, ids := make(map[string]bool), make(map[int]bool)
	path := endpoint.Path
	for range 100 {
		if seen[endpoint.String()] {
			return nil, domain.E("provider_failed", "Bitbucket pagination repeated a page")
		}
		seen[endpoint.String()] = true
		body, current, err := c.get(ctx, origin, endpoint, path, token, host)
		if err != nil {
			return nil, err
		}
		var entries []domain.PullRequest
		var next *url.URL
		if q.Provider == "bitbucket-server" {
			entries, next, err = serverPage(body, current, q)
		} else {
			entries, next, err = cloudPage(body, current, q)
		}
		if err != nil {
			return nil, err
		}
		for _, pr := range entries {
			if !ids[pr.Number] {
				ids[pr.Number] = true
				prs = append(prs, pr)
			}
		}
		if next == nil {
			return prs, nil
		}
		if !safeEndpoint(origin, next, path) {
			return nil, domain.E("provider_failed", "Bitbucket returned unsafe pagination")
		}
		endpoint = next
	}
	return nil, domain.E("provider_failed", "Bitbucket pull-request lookup exceeded 100 pages")
}

func matches(q domain.PullRequestQuery, source, branch, target, base string) bool {
	return strings.EqualFold(source, q.SourceRepository) && strings.EqualFold(target, q.Repository) && branch == q.Branch && (q.Base == "" || base == q.Base)
}

func normalizeState(state string) string {
	switch state {
	case "OPEN":
		return "open"
	case "MERGED":
		return "merged"
	case "DECLINED", "SUPERSEDED":
		return "closed"
	default:
		return ""
	}
}

func includeState(query, state string) bool {
	return query == "all" || (query == "open" && state == "open") || (query == "closed" && state != "open")
}

func validMetadata(pr domain.PullRequest) bool {
	u, err := url.Parse(pr.URL)
	return pr.Number > 0 && pr.Title != "" && pr.State != "" && !pr.UpdatedAt.IsZero() && validBranch(pr.TargetBranch) && err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && !strings.ContainsFunc(pr.URL, unicode.IsControl)
}

func invalidResponse() error {
	return domain.E("provider_failed", "Bitbucket returned an invalid or incomplete pull-request response")
}
