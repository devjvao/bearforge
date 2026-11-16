package bitbucket

import (
	"bearforge/internal/discovery/safety"
	"bearforge/internal/domain"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type repositoryClone struct {
	Name string `json:"name"`
	Href string `json:"href"`
}
type repositoryEntry struct {
	ID            int64  `json:"id"`
	UUID          string `json:"uuid"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	IsPrivate     bool   `json:"is_private"`
	Public        bool   `json:"public"`
	Archived      bool   `json:"archived"`
	DefaultBranch string `json:"defaultBranch"`
	MainBranch    struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
	Project struct {
		Key string `json:"key"`
	} `json:"project"`
	Links struct {
		Clone []repositoryClone `json:"clone"`
	} `json:"links"`
}

// ListRepositories lists one Cloud workspace or all readable Server repositories.
// Cloud callers without a workspace must choose one via ListWorkspaces first.
func (c *Client) ListRepositories(ctx context.Context, source domain.RepositorySource) ([]domain.RemoteRepository, error) {
	s, err := safety.Source(source)
	if err != nil {
		return nil, err
	}
	if s.Provider != "bitbucket" && s.Provider != "bitbucket-server" {
		return nil, domain.E("unsupported_provider", "repository source is not Bitbucket")
	}
	if s.Provider == "bitbucket" && s.Scope == "" {
		return nil, domain.E("invalid", "select a Bitbucket workspace before listing repositories")
	}
	results := []domain.RemoteRepository{}
	seen := map[string]bool{}
	err = c.repositoryPages(ctx, s, false, func(raw json.RawMessage) error {
		var entry repositoryEntry
		if json.Unmarshal(raw, &entry) != nil {
			return domain.E("provider_failed", "Bitbucket returned invalid repository metadata")
		}
		full := entry.FullName
		id := entry.UUID
		private := entry.IsPrivate
		if s.Provider == "bitbucket-server" {
			full = entry.Project.Key + "/" + entry.Slug
			id = strconv.FormatInt(entry.ID, 10)
			private = !entry.Public
			entry.MainBranch.Name = entry.DefaultBranch
			if entry.ID <= 0 {
				return domain.E("provider_failed", "Bitbucket returned invalid repository identity")
			}
		}
		if !validRepository(full, s.Provider == "bitbucket-server") || id == "" {
			return domain.E("provider_failed", "Bitbucket returned invalid repository identity")
		}
		namespace, name, _ := strings.Cut(full, "/")
		if s.Scope != "" && !strings.EqualFold(namespace, s.Scope) {
			return domain.E("provider_failed", "Bitbucket returned a repository outside the selected scope")
		}
		var https, ssh string
		for _, clone := range entry.Links.Clone {
			switch clone.Name {
			case "https", "http":
				value, err := safety.Clone(clone.Href, "https", s)
				if err != nil {
					return err
				}
				if https != "" && https != value {
					return domain.E("provider_failed", "Bitbucket returned ambiguous clone URLs")
				}
				https = value
			case "ssh":
				value, err := safety.Clone(clone.Href, "ssh", s)
				if err != nil {
					return err
				}
				if ssh != "" && ssh != value {
					return domain.E("provider_failed", "Bitbucket returned ambiguous clone URLs")
				}
				ssh = value
			}
		}
		if https == "" && ssh == "" {
			return domain.E("provider_failed", "Bitbucket returned no usable repository clone URL")
		}
		for _, clone := range []string{https, ssh} {
			if clone != "" {
				identity, err := parseRemote(clone, s.Provider == "bitbucket-server")
				if err != nil || !strings.EqualFold(identity.repository, full) {
					return domain.E("provider_failed", "Bitbucket clone URL does not match repository identity")
				}
			}
		}
		if seen[id] {
			return nil
		}
		seen[id] = true
		results = append(results, domain.RemoteRepository{Provider: s.Provider, Host: s.Host, APIURL: s.APIURL, ID: id, Namespace: namespace, Name: name, FullName: full, Description: entry.Description, DefaultBranch: entry.MainBranch.Name, Private: private, Archived: entry.Archived, HTTPSURL: https, SSHURL: ssh})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ListWorkspaces lists accessible Bitbucket Cloud workspaces. Server projects
// are selected through the repository source's scope rather than this API.
func (c *Client) ListWorkspaces(ctx context.Context, source domain.RepositorySource) ([]domain.ProviderWorkspace, error) {
	s, err := safety.Source(source)
	if err != nil {
		return nil, err
	}
	if s.Provider != "bitbucket" {
		return nil, domain.E("unsupported_provider", "workspace discovery requires Bitbucket Cloud")
	}
	results := []domain.ProviderWorkspace{}
	seen := map[string]bool{}
	err = c.repositoryPages(ctx, s, true, func(raw json.RawMessage) error {
		var entry struct {
			Workspace struct{ Slug, Name string } `json:"workspace"`
		}
		if json.Unmarshal(raw, &entry) != nil || !safety.ValidComponent(entry.Workspace.Slug) {
			return domain.E("provider_failed", "Bitbucket returned invalid workspace metadata")
		}
		if !seen[entry.Workspace.Slug] {
			seen[entry.Workspace.Slug] = true
			results = append(results, domain.ProviderWorkspace{Slug: entry.Workspace.Slug, Name: entry.Workspace.Name})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) repositoryPages(ctx context.Context, s domain.RepositorySource, workspaces bool, consume func(json.RawMessage) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	origin, _ := safety.APIURL(s.APIURL)
	credential, err := c.credentialsFor(ctx, domain.CredentialRequest{Provider: s.Provider, Host: s.Host, APIURL: s.APIURL, GitURL: s.GitURL, Connection: s.Connection, GrantID: s.GrantID, TokenEnv: s.TokenEnv, AllowRefresh: true})
	if err != nil {
		return err
	}
	host := c.host(apiHost(origin))
	select {
	case host.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-host.gate }()
	first := *origin
	query := url.Values{}
	pagination := "page"
	if s.Provider == "bitbucket-server" {
		first.Path = strings.TrimRight(first.Path, "/") + "/repos"
		query.Set("limit", "100")
		query.Set("start", "0")
		pagination = "start"
		if s.Scope != "" {
			query.Set("projectkey", s.Scope)
		}
	} else {
		query.Set("pagelen", "100")
		query.Set("page", "1")
		if workspaces {
			first.Path = strings.TrimRight(first.Path, "/") + "/user/workspaces"
		} else {
			first.Path = strings.TrimRight(first.Path, "/") + "/repositories/" + s.Scope
		}
	}
	first.RawPath = ""
	first.RawQuery = query.Encode()
	endpoint := first
	httpClient, guardError := safety.HTTPClient(c.httpClient, &first, pagination)
	guarded := &Client{httpClient: httpClient, now: c.now}
	seen := map[string]bool{}
	for range 100 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[endpoint.String()] {
			return domain.E("provider_failed", "Bitbucket repository pagination repeated a page")
		}
		seen[endpoint.String()] = true
		body, responseURL, err := guarded.get(ctx, origin, &endpoint, first.Path, credential, host)
		if err != nil {
			return guardError(err)
		}
		var page struct {
			Values        []json.RawMessage `json:"values"`
			Next          string            `json:"next"`
			IsLastPage    *bool             `json:"isLastPage"`
			NextPageStart *int              `json:"nextPageStart"`
		}
		if json.Unmarshal(body, &page) != nil || page.Values == nil {
			return domain.E("provider_failed", "Bitbucket returned invalid discovery response")
		}
		for _, raw := range page.Values {
			if err := consume(raw); err != nil {
				return err
			}
		}
		var next *url.URL
		if s.Provider == "bitbucket-server" {
			if page.IsLastPage == nil {
				return domain.E("provider_failed", "Bitbucket returned incomplete repository pagination")
			}
			if *page.IsLastPage {
				return nil
			}
			if page.NextPageStart == nil || *page.NextPageStart < 0 {
				return domain.E("provider_failed", "Bitbucket returned invalid repository pagination")
			}
			n := *responseURL
			values := n.Query()
			values.Set("start", strconv.Itoa(*page.NextPageStart))
			n.RawQuery = values.Encode()
			next = &n
		} else {
			if page.Next == "" {
				return nil
			}
			next, err = responseURL.Parse(page.Next)
			if err != nil {
				return domain.E("provider_failed", "Bitbucket returned invalid repository pagination")
			}
		}
		next, err = safety.Page(responseURL, next, &first, pagination)
		if err != nil {
			return err
		}
		endpoint = *next
	}
	return domain.E("provider_failed", "Bitbucket repository discovery exceeded 100 pages")
}
