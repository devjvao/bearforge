package bitbucket

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"bearforge/internal/domain"
)

var (
	hostLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	componentPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)
	envPattern       = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	sshAliasPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

type remoteIdentity struct {
	host, repository, api string
}

// Resolve derives target and source identities from metadata and the actual remote.
func (c *Client) Resolve(repo domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	p := repo.Provider
	kind := p["kind"]
	if kind != "" && kind != "bitbucket" && kind != "bitbucket-server" {
		return domain.PullRequestQuery{}, domain.E("unsupported_provider", "pull-request provider is not Bitbucket")
	}
	identity, remoteErr := parseRemote(remote, kind == "bitbucket-server")
	if problem, ok := remoteErr.(*domain.Error); ok && problem.Code == "invalid" {
		return domain.PullRequestQuery{}, remoteErr
	}
	q := domain.PullRequestQuery{
		Provider: kind, Host: strings.ToLower(p["host"]), APIURL: p["api_url"],
		Repository: p["repository"], SourceRepository: p["source_repository"],
		TokenEnv: p["token_env"], Branch: branch, State: "open",
	}
	if q.Host == "" {
		q.Host = identity.host
	}
	if kind == "" {
		if q.Host != "bitbucket.org" {
			return q, domain.E("unsupported_provider", "company hosts require explicit Bitbucket provider configuration")
		}
		q.Provider = "bitbucket"
	}
	if q.SourceRepository == "" {
		if remoteErr != nil {
			return q, remoteErr
		}
		if !strings.EqualFold(identity.host, q.Host) {
			return q, domain.E("invalid", "remote source host differs from provider host; configure source_repository")
		}
		q.SourceRepository = identity.repository
	}
	if q.Repository == "" {
		if !strings.EqualFold(identity.host, q.Host) {
			return q, domain.E("invalid", "configure the target repository for this provider host")
		}
		q.Repository = identity.repository
	}
	if q.APIURL == "" {
		q.APIURL = "https://" + q.Host + "/2.0"
		if q.Host == "bitbucket.org" && q.Provider == "bitbucket" {
			q.APIURL = "https://api.bitbucket.org/2.0"
		}
		if q.Provider == "bitbucket-server" {
			q.APIURL = "https://" + q.Host + "/rest/api/1.0"
			if strings.EqualFold(identity.host, q.Host) && identity.api != "" {
				q.APIURL = identity.api
			}
		}
	}
	q.APIURL = strings.TrimRight(q.APIURL, "/")
	return q, validateQuery(q)
}

func parseRemote(raw string, server bool) (remoteIdentity, error) {
	invalid := domain.E("invalid", "invalid Bitbucket Git remote coordinates")
	unsupported := domain.E("unsupported_provider", "remote is not a supported Bitbucket Git remote; configure provider coordinates")
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return remoteIdentity{}, invalid
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, `\`) || (len(raw) >= 3 && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\')) {
		return remoteIdentity{}, unsupported
	}
	var identity remoteIdentity
	var path string
	ssh := true // SCP-style remotes may name an OpenSSH alias rather than DNS.
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Host == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !validRemoteHost(u.Hostname(), u.Scheme == "ssh") || !validPort(u.Port()) || !safePath(u) {
			return identity, invalid
		}
		ssh = u.Scheme == "ssh"
		if u.User != nil {
			if _, password := u.User.Password(); password || strings.ContainsFunc(u.User.Username(), unicode.IsControl) {
				return identity, invalid
			}
		}
		identity.host, path = strings.ToLower(u.Hostname()), strings.TrimPrefix(u.Path, "/")
		if server && u.Scheme == "https" {
			index := strings.LastIndex(u.Path, "/scm/")
			if index < 0 {
				return identity, unsupported
			}
			path = u.Path[index+5:]
			identity.api = "https://" + u.Host + u.Path[:index] + "/rest/api/1.0"
		}
	} else {
		authority, coordinates, ok := strings.Cut(raw, ":")
		if !ok {
			return identity, unsupported
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		identity.host, path = strings.ToLower(authority), coordinates
	}
	identity.repository = strings.TrimSuffix(path, ".git")
	if !validRemoteHost(identity.host, ssh) || !validRepository(identity.repository, server) {
		return remoteIdentity{}, invalid
	}
	return identity, nil
}

func validRemoteHost(host string, ssh bool) bool {
	return validHost(host) || (ssh && len(host) <= 253 && sshAliasPattern.MatchString(host))
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validRepository(repository string, server bool) bool {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return false
	}
	project := parts[0]
	if server {
		project = strings.TrimPrefix(project, "~")
	}
	return len(project) <= 255 && len(parts[1]) <= 255 && componentPattern.MatchString(project) && componentPattern.MatchString(parts[1])
}

func validBranch(branch string) bool {
	if domain.ValidateBranch(branch) != nil || branch == "@" || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, ".") || strings.ContainsAny(branch, " ~^:?*[\\") || strings.ContainsFunc(branch, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func safePath(u *url.URL) bool {
	if strings.ContainsFunc(u.Path, unicode.IsControl) || strings.Contains(u.Path, `\`) {
		return false
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func apiURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !validHost(u.Hostname()) || !validPort(u.Port()) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !safePath(u) || strings.ContainsFunc(raw, unicode.IsControl) {
		return nil, domain.E("invalid", "Bitbucket API URL must be HTTPS without credentials, query, fragment or unsafe path")
	}
	return u, nil
}

func validateQuery(q domain.PullRequestQuery) error {
	if (q.Provider != "bitbucket" && q.Provider != "bitbucket-server") || !validHost(q.Host) || !validRepository(q.Repository, q.Provider == "bitbucket-server") || !validRepository(q.SourceRepository, q.Provider == "bitbucket-server") || !validBranch(q.Branch) || (q.Base != "" && !validBranch(q.Base)) || (q.TokenEnv != "" && !envPattern.MatchString(q.TokenEnv)) {
		return domain.E("invalid", "invalid Bitbucket pull-request coordinates")
	}
	if q.State != "open" && q.State != "closed" && q.State != "all" {
		return domain.E("invalid", "pull-request state must be open, closed or all")
	}
	_, err := apiURL(q.APIURL)
	return err
}
