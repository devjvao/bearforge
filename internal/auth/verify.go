package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type verificationError struct{ state string }

func (e *verificationError) Error() string { return "provider authentication: " + e.state }
func (m *Manager) verify(ctx context.Context, c Connection, v *credential) (Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := c.APIURL + "/user"
	if c.Provider == "bitbucket-server" {
		endpoint = c.APIURL + "/repos?permission=REPO_READ&limit=1"
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return c, ErrScope
	}
	if e = v.Authorize(req); e != nil {
		return c, e
	}
	req.Header.Set("Accept", "application/json")
	resp, e := m.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return c, ctx.Err()
		}
		return c, &verificationError{StateOffline}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401:
		return c, &verificationError{StateRejected}
	case 403:
		return c, &verificationError{StateForbidden}
	case 429:
		return c, &verificationError{StateRateLimited}
	default:
		return c, &verificationError{StateOffline}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if e != nil || len(b) > 1<<20 {
		return c, errors.New("invalid provider identity response")
	}
	var identity struct {
		Login    string          `json:"login"`
		ID       json.RawMessage `json:"id"`
		UUID     string          `json:"uuid"`
		Username string          `json:"username"`
	}
	if e = json.Unmarshal(b, &identity); e != nil {
		return c, errors.New("invalid provider identity response")
	}
	c.IdentityVerified = false
	switch c.Provider {
	case "github":
		if identity.Login == "" || len(identity.ID) == 0 {
			return c, errors.New("provider did not attest an account identity")
		}
		c.Account = identity.Login
		c.Username = identity.Login
		c.IdentityVerified = true
	case "bitbucket-cloud":
		if identity.UUID == "" {
			return c, errors.New("provider did not attest an account identity")
		}
		c.Account = identity.UUID
		c.IdentityVerified = true
	case "bitbucket-server": // Repository reads can be anonymous; never attest a supplied username.
		c.Account = ""
		c.State = StateUnverified
		c.ObservedAt = m.now()
		return c, nil
	}
	c.State = StateVerified
	c.ObservedAt = m.now()
	return c, nil
}
