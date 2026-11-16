package domain

import (
	"context"
	"net/http"
	"net/url"
)

// CredentialRequest selects an explicit source and the approved API/Git scope.
type CredentialRequest struct {
	Provider, Host, APIURL, GitURL, Repository, Connection, GrantID, TokenEnv string
	AllowRefresh                                                              bool
}

// Credential applies credentials without exposing serializable secret fields.
type Credential interface {
	Authorize(*http.Request) error
	GitCredential(*url.URL) (username, password string, err error)
	CacheKey() string
}

type CredentialResolver interface {
	Resolve(context.Context, CredentialRequest) (Credential, error)
}
