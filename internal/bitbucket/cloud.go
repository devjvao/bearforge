package bitbucket

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
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

type cloudPagination struct {
	Next   string `json:"next"`
	Page   *int64 `json:"page"`
	Length *int64 `json:"pagelen"`
	Size   *int64 `json:"size"`
}

type cloudPageProgress struct {
	rawRows        int64
	advertisedSize int64
}

// Count rows before filtering or deduplication. An omitted size leaves the
// last advertised total in effect, including across shorter intermediate pages.
func (progress *cloudPageProgress) accept(page cloudPagination, rows int) bool {
	if (page.Page != nil && *page.Page <= 0) || (page.Length != nil && *page.Length <= 0) || (page.Size != nil && *page.Size < 0) {
		return false
	}
	if page.Size != nil {
		progress.advertisedSize = *page.Size
	}
	progress.rawRows += int64(rows)
	if page.Next == "" && progress.advertisedSize > int64(rows) {
		if progress.rawRows < progress.advertisedSize {
			return false
		}
		if page.Page == nil || page.Length == nil {
			return true // Iterator responses can omit positional controls.
		}
		if *page.Page-1 > progress.advertisedSize / *page.Length {
			return false
		}
		// The division guard bounds multiplication by size, avoiding overflow.
		previous := (*page.Page - 1) * *page.Length
		return progress.advertisedSize-previous <= int64(rows)
	}
	return true
}

func cloudPage(body []byte, current *url.URL, q domain.PullRequestQuery, progress *cloudPageProgress) ([]domain.PullRequest, *url.URL, error) {
	var page struct {
		cloudPagination
		Values []cloudPullRequest `json:"values"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Values == nil || !progress.accept(page.cloudPagination, len(page.Values)) {
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
