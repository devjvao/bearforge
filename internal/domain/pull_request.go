package domain

import "time"

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
