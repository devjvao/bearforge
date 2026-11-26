package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

func (m *Manager) Login(ctx context.Context, r LoginRequest, h LoginHooks) (Connection, error) {
	if r.Method != "" && r.Method != MethodToken {
		return Connection{}, ErrOAuthRemoved
	}
	r, e := NormalizeLoginRequest(r)
	if e != nil {
		return Connection{}, e
	}
	if r.Provider == "bitbucket-server" && !tokenPageUsername(r.Username) {
		return Connection{}, errors.New("Bitbucket Server token setup requires a valid Git username")
	}
	c := Connection{Name: r.Name, Provider: r.Provider, Host: r.Host, APIURL: r.APIURL, GitURL: r.GitURL, Username: r.Username, GrantID: randomID(), State: StateUnverified, Method: MethodToken}
	if h.Confirm != nil {
		if e = h.Confirm(ctx, c); e != nil {
			return Connection{}, e
		}
	}
	if h.Notify != nil {
		h.Notify(tokenSetupGuide(c))
	}
	if m.browser != nil && !r.NoBrowser {
		_ = m.browser.Open(ctx, tokenSetupURL(c))
	}
	if h.ReadSecret == nil {
		return Connection{}, errors.New("hidden token input is required")
	}
	access, e := h.ReadSecret(ctx, "Token")
	if e != nil {
		return Connection{}, e
	}
	if strings.TrimSpace(access) == "" || strings.ContainsAny(access, "\r\n\x00") {
		return Connection{}, errors.New("token is empty or malformed")
	}
	switch c.Provider {
	case "github":
		c.TokenKind = "github-token"
	case "bitbucket-cloud":
		if c.Username == "" {
			return Connection{}, errors.New("Bitbucket Cloud API token requires the account email")
		}
		c.TokenKind = "bitbucket-api-token"
	default:
		c.TokenKind = "bitbucket-user-token"
	}
	c, e = m.verify(ctx, c, credentialFor(c, access))
	if e != nil {
		return Connection{}, e
	}
	return m.save(ctx, c, access)
}

func tokenSetupURL(c Connection) string {
	if c.Provider == "github" {
		return c.Host + "/settings/personal-access-tokens/new?name=Bearforge&contents=read&pull_requests=read"
	}
	return managementURL(c)
}

func tokenSetupGuide(c Connection) string {
	lines := []string{"Token setup: " + tokenSetupURL(c)}
	switch c.Provider {
	case "github":
		lines = append(lines,
			"GitHub fine-grained personal access token:",
			"- Choose an expiration and the Resource owner (your account or organization).",
			"- Repository access: Only select repositories; select the intended repositories.",
			"- Repository permissions: Contents: Read-only (clone/fetch).",
			"- Metadata: Read-only (automatic; repository discovery and API checks).",
			"- Pull requests: Read-only (PR lookup).",
			"- Account permissions: none.",
			"Organization approval may be required before private repositories are accessible.")
		if c.Host != "https://github.com" {
			lines = append(lines,
				"If this installation does not support fine-grained tokens: "+managementURL(c),
				"Classic tokens require repo for private repositories; this also grants write access. SSO authorization may be required.")
		}
	case "bitbucket-cloud":
		lines = append(lines,
			"Create an API token with scopes and select Bitbucket. Select all four explicitly:",
			"- User: Read (read:user:bitbucket) - account verification.",
			"- Workspaces: Read (read:workspace:bitbucket) - workspace discovery.",
			"- Repositories: Read (read:repository:bitbucket) - repository discovery and clone/fetch.",
			"- Pull requests: Read (read:pullrequest:bitbucket) - PR lookup.",
			"Use your Atlassian account email as the username. The account must already have access to the intended repositories.")
	default:
		lines = append(lines,
			"Create a user HTTP access token on this Bitbucket Server / Data Center installation:",
			"If redirected by single sign-on, open your profile picture > Manage account > HTTP access tokens.",
			"- Repository permissions: Repository read (discovery, clone/fetch, and PR lookup).",
			"- Project permissions: Project read, if the form includes this selector.",
			"Use your Git username. The account must already have repository access; company policy may require token approval.")
	}
	return strings.Join(lines, "\n")
}

func managementURL(c Connection) string {
	switch c.Provider {
	case "github":
		return c.Host + "/settings/tokens"
	case "bitbucket-cloud":
		return "https://id.atlassian.com/manage-profile/security/api-tokens"
	default:
		if !tokenPageUsername(c.Username) {
			return c.Host
		}
		return c.Host + "/plugins/servlet/access-tokens/users/" + url.PathEscape(c.Username) + "/manage"
	}
}

func tokenPageUsername(username string) bool {
	return username != "" && strings.TrimSpace(username) == username &&
		username != "." && username != ".." && !strings.ContainsAny(username, "/\\") &&
		strings.IndexFunc(username, unicode.IsControl) == -1
}
