package bitbucket

import (
	"encoding/json"
	"net/url"
	"time"

	"bearforge/internal/domain"
)

type cloudRef struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

type cloudPullRequest struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Updated     time.Time `json:"updated_on"`
	Source      cloudRef  `json:"source"`
	Destination cloudRef  `json:"destination"`
	Links       struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func cloudPage(body []byte, current *url.URL, q domain.PullRequestQuery) ([]domain.PullRequest, *url.URL, error) {
	var page struct {
		Values []cloudPullRequest `json:"values"`
		Next   string             `json:"next"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Values == nil {
		return nil, nil, invalidResponse()
	}
	prs := []domain.PullRequest{}
	for _, entry := range page.Values {
		if !matches(q, entry.Source.Repository.FullName, entry.Source.Branch.Name, entry.Destination.Repository.FullName, entry.Destination.Branch.Name) {
			continue
		}
		pr := domain.PullRequest{
			Number: entry.ID, Title: entry.Title, URL: entry.Links.HTML.Href, State: normalizeState(entry.State), Draft: entry.Draft,
			SourceRepository: entry.Source.Repository.FullName, SourceBranch: entry.Source.Branch.Name,
			TargetRepository: entry.Destination.Repository.FullName, TargetBranch: entry.Destination.Branch.Name, UpdatedAt: entry.Updated,
		}
		if !validMetadata(pr) {
			return nil, nil, invalidResponse()
		}
		if includeState(q.State, pr.State) {
			prs = append(prs, pr)
		}
	}
	if page.Next == "" {
		return prs, nil, nil
	}
	next, err := current.Parse(page.Next)
	if err != nil {
		return nil, nil, invalidResponse()
	}
	return prs, next, nil
}
