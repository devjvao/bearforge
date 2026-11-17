package github

import (
	"context"
	"encoding/json"
	"github.com/devjvao/bearforge/internal/discovery/safety"
	"github.com/devjvao/bearforge/internal/domain"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type discoveredRepository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	HTTPSURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
}

// ListRepositories returns all accessible repositories in the selected grant,
// optionally filtered by owner. Anonymous access requires a public owner scope.
// A failed later page discards the entire result.
func (c *Client) ListRepositories(ctx context.Context, source domain.RepositorySource) ([]domain.RemoteRepository, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := safety.Source(source)
	if err != nil {
		return nil, err
	}
	if s.Provider != "github" {
		return nil, domain.E("unsupported_provider", "repository source is not GitHub")
	}
	if s.Scope != "" && !ownerPattern.MatchString(s.Scope) {
		return nil, domain.E("invalid", "invalid GitHub owner scope")
	}
	origin, _ := safety.APIURL(s.APIURL)
	credential, err := c.credentialsFor(ctx, domain.CredentialRequest{Provider: s.Provider, Host: s.Host, APIURL: s.APIURL, GitURL: s.GitURL, Connection: s.Connection, GrantID: s.GrantID, TokenEnv: s.TokenEnv, AllowRefresh: true})
	if err != nil {
		return nil, err
	}
	anonymous := credential.resolved == nil && credential.bearer == ""
	if anonymous && s.Scope == "" {
		return nil, domain.E("unauthenticated", "anonymous GitHub discovery requires an owner or organization scope; select a connection to list accessible repositories")
	}
	host := c.host(apiHost(origin))
	select {
	case host.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-host.gate }()
	endpoint := *origin
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/user/repos"
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{"affiliation": {"owner,collaborator,organization_member"}, "per_page": {"100"}, "page": {"1"}}.Encode()
	if anonymous {
		path, kind, err := c.publicRepositoryOwner(ctx, origin, s.Scope, host)
		if err != nil {
			return nil, err
		}
		endpoint.Path = path
		endpoint.RawQuery = url.Values{"type": {kind}, "per_page": {"100"}, "page": {"1"}}.Encode()
	}
	first := endpoint
	// get only needs these two immutable fields; request state stays on the
	// original client's host gate so listing and PR lookup share one cooldown.
	httpClient, guardError := safety.HTTPClient(c.httpClient, &first, "page")
	guarded := &Client{httpClient: httpClient, now: c.now}
	repositories := []domain.RemoteRepository{}
	seenPages := map[string]bool{}
	seenIDs := map[int64]bool{}
	for range 100 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seenPages[endpoint.String()] {
			return nil, domain.E("provider_failed", "GitHub repository pagination repeated a page")
		}
		seenPages[endpoint.String()] = true
		body, header, responseURL, err := guarded.get(ctx, origin, &endpoint, first.Path, credential, host)
		if err != nil {
			return nil, guardError(err)
		}
		var entries []discoveredRepository
		if json.Unmarshal(body, &entries) != nil || entries == nil {
			return nil, domain.E("provider_failed", "GitHub returned invalid repository metadata")
		}
		for _, entry := range entries {
			if entry.ID <= 0 || !validRepository(entry.FullName) {
				return nil, domain.E("provider_failed", "GitHub returned invalid repository identity")
			}
			if anonymous && entry.Private {
				return nil, domain.E("provider_failed", "GitHub returned private metadata for an anonymous public source")
			}
			owner, name, _ := strings.Cut(entry.FullName, "/")
			if s.Scope != "" && !strings.EqualFold(owner, s.Scope) {
				continue
			}
			https, err := safety.Clone(entry.HTTPSURL, "https", s)
			if err != nil {
				return nil, err
			}
			ssh, err := safety.Clone(entry.SSHURL, "ssh", s)
			if err != nil {
				return nil, err
			}
			if https == "" && ssh == "" {
				return nil, domain.E("provider_failed", "GitHub returned no usable repository clone URL")
			}
			for _, clone := range []string{https, ssh} {
				if clone != "" {
					_, identity, err := parseRemote(clone)
					if err != nil || !strings.EqualFold(identity, entry.FullName) {
						return nil, domain.E("provider_failed", "GitHub clone URL does not match repository identity")
					}
				}
			}
			if seenIDs[entry.ID] {
				continue
			}
			seenIDs[entry.ID] = true
			repositories = append(repositories, domain.RemoteRepository{Provider: s.Provider, Host: s.Host, APIURL: s.APIURL, ID: strconv.FormatInt(entry.ID, 10), Namespace: owner, Name: name, FullName: entry.FullName, Description: entry.Description, DefaultBranch: entry.DefaultBranch, Private: entry.Private, Archived: entry.Archived, HTTPSURL: https, SSHURL: ssh})
		}
		next, err := nextPage(header, responseURL, origin)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return repositories, nil
		}
		next, err = safety.Page(responseURL, next, &first, "page")
		if err != nil {
			return nil, err
		}
		endpoint = *next
	}
	return nil, domain.E("provider_failed", "GitHub repository discovery exceeded 100 pages")
}

// publicRepositoryOwner distinguishes user and organization endpoints without
// following a provider-supplied URL or permitting redirects to another scope.
func (c *Client) publicRepositoryOwner(ctx context.Context, origin *url.URL, owner string, host *hostState) (string, string, error) {
	endpoint := *origin
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/users/" + owner
	endpoint.RawPath, endpoint.RawQuery = "", ""
	httpClient, guardError := safety.HTTPClient(c.httpClient, &endpoint)
	guarded := &Client{httpClient: httpClient, now: c.now}
	body, _, _, err := guarded.get(ctx, origin, &endpoint, endpoint.Path, requestCredential{}, host)
	if err != nil {
		return "", "", guardError(err)
	}
	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if json.Unmarshal(body, &account) != nil || !strings.EqualFold(account.Login, owner) {
		return "", "", domain.E("provider_failed", "GitHub returned invalid public owner identity")
	}
	prefix, kind := "/users/", "owner"
	switch account.Type {
	case "User":
	case "Organization":
		prefix, kind = "/orgs/", "public"
	default:
		return "", "", domain.E("provider_failed", "GitHub returned unsupported public owner type")
	}
	return strings.TrimRight(origin.Path, "/") + prefix + owner + "/repos", kind, nil
}
