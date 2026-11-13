package github

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"bearforge/internal/domain"
)

var (
	ownerPattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)
	repoPattern     = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	envPattern      = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	sshAliasPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

// Resolve derives a PR target and source from metadata and the actual Git remote.
func (c *Client) Resolve(repo domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	p := repo.Provider
	if kind := p["kind"]; kind != "" && kind != "github" {
		return domain.PullRequestQuery{}, domain.E("unsupported_provider", "pull-request provider is not GitHub")
	}
	remoteHost, remoteRepo, remoteErr := parseRemote(remote)
	if e, ok := remoteErr.(*domain.Error); ok && e.Code == "invalid" {
		return domain.PullRequestQuery{}, remoteErr
	}
	q := domain.PullRequestQuery{Host: p["host"], APIURL: p["api_url"], Repository: p["repository"], SourceRepository: p["source_repository"], TokenEnv: p["token_env"], Branch: branch, State: "open"}
	if q.Host == "" {
		q.Host = remoteHost
	}
	q.Host = strings.ToLower(q.Host)
	if !validHost(q.Host) && q.Host != "" {
		return q, domain.E("invalid", "invalid provider host")
	}
	if q.Host != "github.com" && p["kind"] != "github" {
		return q, domain.E("unsupported_provider", "custom hosts require explicit GitHub provider configuration")
	}
	if remoteErr != nil && q.SourceRepository == "" {
		return q, remoteErr
	}
	if q.SourceRepository == "" {
		if !strings.EqualFold(remoteHost, q.Host) {
			return q, domain.E("invalid", "remote source host differs from provider host; configure source_repository")
		}
		q.SourceRepository = remoteRepo
	}
	if q.Repository == "" {
		q.Repository = remoteRepo
	}
	if q.APIURL == "" {
		q.APIURL = "https://" + q.Host + "/api/v3"
		if q.Host == "github.com" {
			q.APIURL = "https://api.github.com"
		}
	}
	q.APIURL = strings.TrimRight(q.APIURL, "/")
	return q, validateQuery(q)
}

func parseRemote(raw string) (string, string, error) {
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return "", "", domain.E("invalid", "invalid GitHub remote URL")
	}
	// Local Git remotes have no provider identity to infer.
	if !strings.Contains(raw, "://") && (strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, `\`) || (len(raw) >= 3 && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\'))) {
		return "", "", domain.E("unsupported_provider", "remote is not a supported GitHub remote")
	}
	var host, path string
	ssh := true // SCP-style remotes may use an OpenSSH alias instead of DNS.
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err == nil && u.Scheme == "file" {
			if u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.ContainsFunc(u.Path, unicode.IsControl) || strings.Contains(u.Path, `\`) {
				return "", "", domain.E("invalid", "invalid GitHub remote URL")
			}
			return "", "", domain.E("unsupported_provider", "local file remote requires explicit GitHub provider coordinates")
		}
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "ssh") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
			return "", "", domain.E("invalid", "invalid GitHub remote URL")
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return "", "", domain.E("invalid", "invalid GitHub remote URL")
			}
		}
		if u.User != nil {
			if _, password := u.User.Password(); password || u.Scheme != "ssh" || strings.ContainsFunc(u.User.Username(), unicode.IsControl) {
				return "", "", domain.E("invalid", "credentials in remote URLs are not supported")
			}
		}
		ssh = u.Scheme == "ssh"
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	} else {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 {
			return "", "", domain.E("unsupported_provider", "remote is not a supported GitHub remote")
		}
		host = parts[0]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		path = parts[1]
	}
	path = strings.TrimSuffix(path, ".git")
	if !validRepository(path) {
		return "", "", domain.E("invalid", "invalid GitHub remote coordinates")
	}
	if !validHost(host) {
		if ssh && len(host) <= 253 && sshAliasPattern.MatchString(host) {
			return "", "", domain.E("unsupported_provider", "SSH alias requires explicit GitHub provider coordinates")
		}
		return "", "", domain.E("invalid", "invalid GitHub remote coordinates")
	}
	return strings.ToLower(host), path, nil
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !ownerPattern.MatchString(label) || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	return len(parts) == 2 && len(parts[0]) <= 100 && ownerPattern.MatchString(parts[0]) && len(parts[1]) <= 100 && repoPattern.MatchString(parts[1]) && parts[1] != "." && parts[1] != ".."
}

func validBranch(branch string) bool {
	if domain.ValidateBranch(branch) != nil || branch == "@" || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, ".") || strings.ContainsAny(branch, " ~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return !strings.ContainsFunc(branch, unicode.IsControl)
}

func apiURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00\\") {
		return nil, domain.E("invalid", "API URL must be an HTTPS URL without credentials, query or fragment")
	}
	if !validHost(u.Hostname()) {
		return nil, domain.E("invalid", "invalid API hostname")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, domain.E("invalid", "invalid API port")
		}
	}
	if strings.ContainsFunc(u.Path, unicode.IsControl) || strings.Contains(u.Path, "\\") {
		return nil, domain.E("invalid", "invalid API path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, domain.E("invalid", "invalid API path")
		}
	}
	return u, nil
}

func validateQuery(q domain.PullRequestQuery) error {
	if !validHost(q.Host) || !validRepository(q.Repository) || !validRepository(q.SourceRepository) || !validBranch(q.Branch) || (q.Base != "" && !validBranch(q.Base)) || (q.TokenEnv != "" && !envPattern.MatchString(q.TokenEnv)) {
		return domain.E("invalid", "invalid GitHub pull-request coordinates")
	}
	if q.State != "open" && q.State != "closed" && q.State != "all" {
		return domain.E("invalid", "pull-request state must be open, closed or all")
	}
	_, err := apiURL(q.APIURL)
	return err
}
