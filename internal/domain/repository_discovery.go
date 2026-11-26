package domain

// RepositorySource identifies one explicitly selected discovery source. Scope is
// a GitHub owner, Bitbucket Cloud workspace, or Server project key.
type RepositorySource struct {
	Provider, Host, APIURL, GitURL, Connection, GrantID, TokenEnv, Scope string
}

// RemoteRepository contains nonsecret provider identity and validated clone URLs.
// ID is stable within Provider, Host, and APIURL; FullName is owner/project/name.
// An empty DefaultBranch explicitly means unknown; providers need not expose it
// in repository-list responses. Callers must not infer a branch name from it.
type RemoteRepository struct {
	ProjectKey                                            string `json:"project_key,omitempty"`
	Provider, Host, APIURL, ID, Namespace, Name, FullName string
	Description, DefaultBranch, HTTPSURL, SSHURL          string
	Private, Archived                                     bool
}

// ProviderWorkspace is a Bitbucket Cloud workspace, separate from a local workspace.
type ProviderWorkspace struct{ Slug, Name string }
