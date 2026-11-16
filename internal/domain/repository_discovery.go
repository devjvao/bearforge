package domain

// RepositorySource identifies one explicitly selected discovery source. Scope is
// a GitHub owner, Bitbucket Cloud workspace, or Server project key.
type RepositorySource struct {
	Provider, Host, APIURL, GitURL, Connection, GrantID, TokenEnv, Scope string
}

// RemoteRepository contains nonsecret provider identity and validated clone URLs.
// ID is stable within Provider, Host, and APIURL; FullName is owner/project/name.
type RemoteRepository struct {
	Provider, Host, APIURL, ID, Namespace, Name, FullName string
	Description, DefaultBranch, HTTPSURL, SSHURL          string
	Private, Archived                                     bool
}

// ProviderWorkspace is a Bitbucket Cloud workspace, separate from a local workspace.
type ProviderWorkspace struct{ Slug, Name string }
