package github

import (
	"context"
	"crypto/sha256"
	"net/http"

	"github.com/devjvao/bearforge/internal/domain"
)

// requestCredential preserves the legacy Bearer contract while allowing saved
// grants to select their provider-specific authentication scheme.
type requestCredential struct {
	bearer   string
	resolved domain.Credential
}

func (c *Client) credentialsFor(ctx context.Context, req domain.CredentialRequest) (requestCredential, error) {
	if c.resolver != nil {
		resolved, err := c.resolver.Resolve(ctx, req)
		return requestCredential{resolved: resolved}, err
	}
	if req.Connection != "" || req.GrantID != "" {
		return requestCredential{}, domain.E("unauthenticated", "saved connection is unavailable; use bearforge auth login or select a configured credential source")
	}
	origin, err := apiURL(req.APIURL)
	if err != nil {
		return requestCredential{}, err
	}
	token, err := credential(domain.PullRequestQuery{
		Provider: req.Provider, Host: req.Host, TokenEnv: req.TokenEnv,
	}, origin)
	return requestCredential{bearer: token}, err
}

func (c requestCredential) authorize(req *http.Request) error {
	if c.resolved != nil {
		return c.resolved.Authorize(req)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	return nil
}

func (c requestCredential) cacheIdentity() [32]byte {
	if c.resolved != nil {
		return sha256.Sum256([]byte(c.resolved.CacheKey()))
	}
	return sha256.Sum256([]byte(c.bearer))
}
