// Package safety validates nonsecret discovery coordinates and page boundaries.
package safety

import (
	"bearforge/internal/domain"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
)

var component = regexp.MustCompile(`^[a-zA-Z0-9_~][a-zA-Z0-9_.~-]*$`)
var environment = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ValidComponent(value string) bool {
	return len(value) <= 255 && component.MatchString(value) && value != "." && value != ".."
}

func Source(s domain.RepositorySource) (domain.RepositorySource, error) {
	s.Provider = strings.ToLower(s.Provider)
	s.Host = strings.ToLower(s.Host)
	if s.Host == "" {
		switch s.Provider {
		case "github":
			s.Host = "github.com"
		case "bitbucket":
			s.Host = "bitbucket.org"
		}
	}
	if s.Provider != "github" && s.Provider != "bitbucket" && s.Provider != "bitbucket-server" {
		return s, domain.E("unsupported_provider", "unsupported repository discovery provider")
	}
	if !validHost(s.Host) || (s.Scope != "" && !ValidComponent(s.Scope)) || (s.TokenEnv != "" && !environment.MatchString(s.TokenEnv)) {
		return s, domain.E("invalid", "invalid repository discovery source")
	}
	if s.APIURL == "" {
		switch s.Provider {
		case "github":
			s.APIURL = "https://" + s.Host + "/api/v3"
			if s.Host == "github.com" {
				s.APIURL = "https://api.github.com"
			}
		case "bitbucket":
			if s.Host != "bitbucket.org" {
				return s, domain.E("invalid", "Bitbucket Cloud requires an explicit API URL for custom hosts")
			}
			s.APIURL = "https://api.bitbucket.org/2.0"
		case "bitbucket-server":
			s.APIURL = "https://" + s.Host + "/rest/api/latest"
		}
	}
	s.APIURL = strings.TrimRight(s.APIURL, "/")
	if _, err := APIURL(s.APIURL); err != nil {
		return s, err
	}
	if s.GitURL != "" {
		if _, err := APIURL(s.GitURL); err != nil {
			return s, domain.E("invalid", "invalid approved Git URL")
		}
	}
	return s, nil
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func safePath(u *url.URL) bool {
	if strings.ContainsFunc(u.Path, unicode.IsControl) || strings.ContainsAny(u.Path, `\`) {
		return false
	}
	raw := strings.ToLower(u.EscapedPath())
	if strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(raw, "%25") {
		return false
	}
	for _, p := range strings.Split(u.Path, "/") {
		if p == "." || p == ".." {
			return false
		}
	}
	return true
}

func APIURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !validHost(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !safePath(u) || strings.ContainsFunc(raw, unicode.IsControl) || !validPort(u.Port()) {
		return nil, domain.E("invalid", "API URL must be HTTPS without credentials, query, fragment or unsafe path")
	}
	return u, nil
}
func validPort(raw string) bool {
	if raw == "" {
		return true
	}
	n, e := strconv.Atoi(raw)
	return e == nil && n > 0 && n <= 65535
}

// Clone preserves approved API-provided SSH usernames, ports and context paths.
func Clone(raw, transport string, s domain.RepositorySource) (string, error) {
	if raw == "" {
		return "", nil
	}
	invalid := domain.E("provider_failed", "provider returned an unsafe clone URL")
	if strings.ContainsFunc(raw, unicode.IsSpace) || strings.ContainsFunc(raw, unicode.IsControl) || strings.ContainsAny(raw, `\?#`) {
		return "", invalid
	}
	candidate := raw
	if transport == "ssh" && !strings.Contains(raw, "://") {
		authority, path, ok := strings.Cut(raw, ":")
		if !ok || !strings.Contains(authority, "@") || strings.HasPrefix(path, "/") {
			return "", invalid
		}
		candidate = "ssh://" + authority + "/" + path
	}
	u, err := url.Parse(candidate)
	if err != nil || u.Scheme != transport || !validHost(u.Hostname()) || !validPort(u.Port()) || !safePath(u) || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", invalid
	}
	if u.User != nil {
		if _, password := u.User.Password(); password || transport != "ssh" || u.User.Username() == "" || strings.ContainsAny(u.User.Username(), "/@:") {
			return "", invalid
		}
	}
	allowed := strings.EqualFold(u.Hostname(), s.Host)
	if s.GitURL != "" {
		git, _ := url.Parse(s.GitURL)
		allowed = strings.EqualFold(u.Hostname(), git.Hostname())
		if transport == "https" {
			allowed = allowed && authority(u) == authority(git) && strings.HasPrefix(u.Path, strings.TrimRight(git.Path, "/")+"/")
		}
	}
	if !allowed {
		return "", invalid
	}
	return raw, nil
}
func authority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// Page fixes origin, path and all query scope; only pagination keys may vary.
// Some providers omit fixed query options in relative next links, so restore them.
func Page(current, target, first *url.URL, variables ...string) (*url.URL, error) {
	invalid := domain.E("provider_failed", "provider returned unsafe repository pagination")
	if target.Scheme != "https" || authority(target) != authority(first) || target.User != nil || target.Fragment != "" || target.Path != first.Path || !safePath(target) || strings.ContainsFunc(target.String(), unicode.IsControl) {
		return nil, invalid
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return nil, invalid
	}
	fixed := first.Query()
	variable := map[string]bool{}
	for _, k := range variables {
		variable[k] = true
	}
	for key, values := range query {
		if variable[key] {
			if len(values) != 1 || values[0] == "" {
				return nil, invalid
			}
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 0 {
				return nil, invalid
			}
			continue
		}
		if len(values) != 1 || len(fixed[key]) != 1 || values[0] != fixed.Get(key) {
			return nil, invalid
		}
	}
	for key, values := range fixed {
		if !variable[key] {
			query[key] = values
		}
	}
	copyURL := *target
	copyURL.RawQuery = query.Encode()
	copyURL.RawPath = ""
	return &copyURL, nil
}

type guardTransport struct {
	base      http.RoundTripper
	first     *url.URL
	variables []string
	rejected  atomic.Bool
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	page, err := Page(req.URL, req.URL, g.first, g.variables...)
	if err != nil {
		g.rejected.Store(true)
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.URL = page
	return g.base.RoundTrip(clone)
}

// HTTPClient adds query checks before every redirect reaches the network; the
// existing provider get path still owns redirects, retries, sizes and cooldowns.
func HTTPClient(base *http.Client, first *url.URL, variables ...string) (*http.Client, func(error) error) {
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	guard := &guardTransport{base: transport, first: first, variables: variables}
	client.Transport = guard
	return &client, func(err error) error {
		if guard.rejected.Load() {
			return domain.E("provider_failed", "provider returned unsafe repository pagination or redirect")
		}
		return err
	}
}
