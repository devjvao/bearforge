package domain

import (
	"net/url"
	"regexp"
	"strings"
)

var portableID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
var remoteName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func ValidateID(id string) error {
	if len(id) > 64 || !portableID.MatchString(id) || strings.HasSuffix(id, ".") {
		return E("invalid", "invalid portable name %q", id)
	}
	base := strings.Split(id, ".")[0]
	reserved := base == "con" || base == "prn" || base == "aux" || base == "nul" || base == "repos"
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '0' && base[3] <= '9' {
		reserved = true
	}
	if reserved {
		return E("invalid", "reserved name %q", id)
	}
	return nil
}

func ValidateRemoteName(name string) error {
	if !remoteName.MatchString(name) {
		return E("invalid", "invalid remote name %q", name)
	}
	return nil
}

func ValidateBranch(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") || strings.Contains(name, "@{") || strings.ContainsAny(name, "\x00\r\n") {
		return E("invalid", "invalid literal branch name %q", name)
	}
	return nil
}

func ValidateURL(raw string) error {
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "\x00\r\n") {
		return E("invalid", "invalid repository URL")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return E("invalid", "invalid repository URL")
		}
		if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "file" {
			return E("invalid", "unsupported repository URL scheme")
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return E("invalid", "URL queries and fragments are not supported")
		}
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword || (u.Scheme != "ssh") {
				return E("invalid", "use a credential helper instead of credentials in URLs")
			}
		}
	} else if strings.Contains(raw, "::") {
		return E("invalid", "custom remote helpers are not supported")
	}
	return nil
}
