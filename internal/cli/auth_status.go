package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
	"github.com/devjvao/bearforge/internal/pullrequests"
)

type authenticationStatusData struct {
	Connections  []auth.StatusResult              `json:"connections"`
	Repositories []repositoryAuthenticationStatus `json:"repositories"`
	Local        bool                             `json:"-"`
}

type repositoryAuthenticationStatus struct {
	Repository string    `json:"repository"`
	Remote     string    `json:"remote"`
	Connection string    `json:"connection,omitempty"`
	API        string    `json:"api"`
	GitRead    string    `json:"git_read"`
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Source     string    `json:"-"`
}

func (o *options) authenticationStatus(ctx context.Context, connection, repository string, local bool) error {
	manager, err := o.authentication()
	if err != nil {
		return err
	}
	store, err := o.store()
	if err != nil {
		return err
	}
	catalog, _, err := store.LoadCatalog()
	if err != nil {
		return err
	}
	if repository != "" {
		repo, ok := catalog.Repositories[repository]
		if !ok {
			return domain.E("not_found", "repository %s is not registered", repository)
		}
		if connection != "" && repo.Provider["connection"] != connection {
			return domain.E("invalid", "repository %s is not associated with connection %s; check its configured connection or review a repo rebind", repository, connection)
		}
		if connection == "" {
			connection = repo.Provider["connection"]
		}
	}
	data := authenticationStatusData{Connections: []auth.StatusResult{}, Repositories: []repositoryAuthenticationStatus{}, Local: local}
	if repository == "" || connection != "" {
		data.Connections, err = manager.Status(ctx, auth.StatusRequest{Connection: connection, Local: local})
	}
	result := app.NewResult("auth.status")
	var failures []error
	if err != nil {
		failures = append(failures, err)
	}
	for _, status := range data.Connections {
		if !local && status.State != auth.StateVerified && status.State != auth.StateUnverified {
			failures = append(failures, domain.E("unauthenticated", "connection %s: %s", status.Connection.Name, status.State))
		}
	}
	ids := make([]string, 0, len(catalog.Repositories))
	for id := range catalog.Repositories {
		if repository == "" || id == repository {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	service := app.New(store, nil, nil)
	for _, id := range ids {
		repo := catalog.Repositories[id]
		if connection != "" && repo.Provider["connection"] != connection {
			continue
		}
		status := repositoryAuthenticationStatus{Repository: id, Remote: "origin", Connection: repo.Provider["connection"], API: "configured-unverified", GitRead: "configured-unverified"}
		if name := repo.Provider["token_env"]; name != "" {
			status.Source = "env:" + name
		}
		remote := repo.Remotes["origin"]
		if filepath.IsAbs(remote) || strings.HasPrefix(remote, "file://") || (!strings.Contains(remote, ":") && !strings.Contains(remote, "://")) {
			status.API, status.GitRead = "not-applicable", "local-remote"
			data.Repositories = append(data.Repositories, status)
			continue
		}
		if local {
			data.Repositories = append(data.Repositories, status)
			continue
		}
		query, resolveErr := pullrequests.NewProviderWithResolver(manager).Resolve(repo, remote, "main")
		request := domain.CredentialRequest{Provider: query.Provider, Host: query.Host, APIURL: query.APIURL, Repository: query.SourceRepository, Connection: query.Connection, GrantID: query.GrantID, TokenEnv: query.TokenEnv, AllowRefresh: true}
		var credential domain.Credential
		if resolveErr == nil {
			credential, resolveErr = manager.Resolve(ctx, request)
		}
		if resolveErr != nil {
			status.API = "unavailable"
			status.Detail = "API credential or provider configuration requires attention"
			failures = append(failures, resolveErr)
		} else {
			status.API, resolveErr = repositoryAPIAccess(ctx, query, credential)
			if resolveErr != nil {
				failures = append(failures, resolveErr)
			}
		}
		git, gitErr := gitcli.New()
		var repoPath string
		if gitErr == nil {
			repoPath, gitErr = service.RepositoryPath(repo)
		}
		if gitErr == nil {
			gitErr = git.VerifyBare(ctx, repoPath)
		}
		if gitErr == nil {
			var actualOrigin string
			actualOrigin, gitErr = git.FetchURL(ctx, repoPath, "origin")
			if gitErr == nil && actualOrigin != remote {
				gitErr = domain.E("conflict", "repository %s origin differs from its registered URL", id)
			}
		}
		if gitErr == nil && repo.Provider["token_env"] == "" && strings.HasPrefix(remote, "https://") && status.Connection != "" {
			git, gitErr = o.connectionGit(git, repo.Provider["connection"], repo.Provider["grant_id"], remote)
		}
		if gitErr == nil {
			readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, gitErr = git.Run(readCtx, repoPath, "ls-remote", "--refs", "--", remote)
			cancel()
		}
		status.GitRead = "readable"
		if gitErr != nil {
			status.GitRead = "unavailable"
			status.Detail = "Git read check failed; inspect the selected connection, Git helper, or SSH setup"
			failures = append(failures, domain.E("unauthenticated", "repository %s Git read access could not be verified", id))
		}
		status.ObservedAt = time.Now().UTC()
		data.Repositories = append(data.Repositories, status)
	}
	result.Data = data
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	return o.finishAuthentication(result, errors.Join(failures...))
}

func repositoryAPIAccess(ctx context.Context, q domain.PullRequestQuery, credential domain.Credential) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(q.APIURL, "/")
	switch q.Provider {
	case "github":
		endpoint += "/repos/" + q.SourceRepository
	case "bitbucket":
		endpoint += "/repositories/" + q.SourceRepository
	case "bitbucket-server":
		project, repo, _ := strings.Cut(q.SourceRepository, "/")
		endpoint += "/projects/" + url.PathEscape(project) + "/repos/" + url.PathEscape(repo)
	default:
		return "unsupported", domain.E("unsupported_provider", "unsupported authentication provider")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "unavailable", domain.E("invalid", "invalid API coordinates")
	}
	if credential != nil {
		if err := credential.Authorize(req); err != nil {
			return "unavailable", err
		}
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return "offline", domain.E("offline", "repository API could not be reached")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusOK {
		return "readable", nil
	}
	state := "unavailable"
	switch response.StatusCode {
	case 401:
		state = "rejected"
	case 403:
		state = "insufficient-permission"
	case 429:
		state = "rate-limited"
	}
	return state, domain.E("unauthenticated", "repository API access: %s", state)
}
