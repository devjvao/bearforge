package bitbucket

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bearforge/internal/domain"
)

type serverRef struct {
	ID         string `json:"id"`
	Repository struct {
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"repository"`
}

func (r serverRef) repository() string {
	if r.Repository.Project.Key == "" || r.Repository.Slug == "" {
		return ""
	}
	return r.Repository.Project.Key + "/" + r.Repository.Slug
}

func (r serverRef) branch() string {
	if !strings.HasPrefix(r.ID, "refs/heads/") {
		return ""
	}
	return strings.TrimPrefix(r.ID, "refs/heads/")
}

type serverPullRequest struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Draft   bool      `json:"draft"`
	Updated *int64    `json:"updatedDate"`
	From    serverRef `json:"fromRef"`
	To      serverRef `json:"toRef"`
	Links   struct {
		Self []struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
}

func serverPage(body []byte, current *url.URL, q domain.PullRequestQuery) ([]domain.PullRequest, *url.URL, error) {
	var page struct {
		Values []serverPullRequest `json:"values"`
		Last   *bool               `json:"isLastPage"`
		Next   *int64              `json:"nextPageStart"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Values == nil || page.Last == nil {
		return nil, nil, invalidResponse()
	}
	prs := []domain.PullRequest{}
	for _, entry := range page.Values {
		if !matches(q, entry.From.repository(), entry.From.branch(), entry.To.repository(), entry.To.branch()) {
			continue
		}
		if entry.Updated == nil || *entry.Updated <= 0 {
			return nil, nil, invalidResponse()
		}
		var link string
		for _, candidate := range entry.Links.Self {
			if candidate.Href != "" {
				link = candidate.Href
				break
			}
		}
		pr := domain.PullRequest{
			Number: entry.ID, Title: entry.Title, URL: link, State: normalizeState(entry.State), Draft: entry.Draft,
			SourceRepository: entry.From.repository(), SourceBranch: entry.From.branch(),
			TargetRepository: entry.To.repository(), TargetBranch: entry.To.branch(), UpdatedAt: time.UnixMilli(*entry.Updated).UTC(),
		}
		if !validMetadata(pr) {
			return nil, nil, invalidResponse()
		}
		if includeState(q.State, pr.State) {
			prs = append(prs, pr)
		}
	}
	if *page.Last {
		return prs, nil, nil
	}
	start, err := strconv.ParseInt(current.Query().Get("start"), 10, 64)
	if err != nil || page.Next == nil || *page.Next <= start {
		return nil, nil, invalidResponse()
	}
	next := *current
	values := next.Query()
	values.Set("start", strconv.FormatInt(*page.Next, 10))
	next.RawQuery = values.Encode()
	return prs, &next, nil
}
