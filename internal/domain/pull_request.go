package domain

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// PullRequestQuery identifies the target repository and the actual head identity.
type PullRequestQuery struct {
	Provider         string `json:"provider,omitempty"`
	Host             string `json:"host"`
	APIURL           string `json:"api_url"`
	Repository       string `json:"repository"`
	SourceRepository string `json:"source_repository"`
	Branch           string `json:"branch"`
	Base             string `json:"base"`
	State            string `json:"state"`
	TokenEnv         string `json:"token_env"`
	Connection       string `json:"connection,omitempty"`
	GrantID          string `json:"grant_id,omitempty"`
}

// LookupGroup identifies queries that share a provider client's per-host gate.
func (q PullRequestQuery) LookupGroup() string {
	client := q.Provider
	switch client {
	case "":
		client = "github" // Legacy queries dispatch to the GitHub client.
	case "bitbucket-server":
		client = "bitbucket" // Cloud and Server share one client.
	}
	u, err := url.Parse(q.APIURL)
	if err != nil || u.Hostname() == "" {
		// Leave validation to the provider and serialize unaddressed queries.
		return client
	}
	return client + "/" + HTTPSAuthority(u)
}

// HTTPSAuthority returns the lowercase hostname and effective port used by
// provider request gates and same-origin checks.
func HTTPSAuthority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// PullRequest is provider-independent metadata for a verified matching PR.
type PullRequest struct {
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	State            string    `json:"state"`
	Draft            bool      `json:"draft"`
	SourceRepository string    `json:"source_repository"`
	TargetRepository string    `json:"target_repository"`
	SourceBranch     string    `json:"source_branch"`
	TargetBranch     string    `json:"target_branch"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// PullRequestObservation records a complete successful lookup and its freshness.
type PullRequestObservation struct {
	Query        PullRequestQuery `json:"query"`
	PullRequests []PullRequest    `json:"pull_requests"`
	ObservedAt   time.Time        `json:"observed_at"`
	Cached       bool             `json:"cached"`
}
