package auth

import (
	"context"
	"errors"
	"fmt"
)

func (m *Manager) Status(ctx context.Context, r StatusRequest) ([]StatusResult, error) {
	cs, e := m.Connections(ctx)
	if e != nil {
		return nil, e
	}
	out := []StatusResult{}
	for _, c := range cs {
		if r.Connection != "" && c.Name != r.Connection {
			continue
		}
		s := StatusResult{Connection: c, State: StateUnverified, Detail: "Previously observed metadata; credentials have not been checked"}
		if !r.Local {
			v, err := m.resolveSaved(ctx, c, true)
			if err == nil {
				verified, errVerify := m.verify(ctx, c, v.(*credential))
				err = errVerify
				if err == nil && c.IdentityVerified && verified.Account != c.Account {
					err = &verificationError{StateRejected}
				}
				if err == nil {
					s.Connection = verified
					s.State = verified.State
					s.IdentityVerified = verified.IdentityVerified
					s.Detail = "API check only; Git read and push permission have not been checked"
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				s.State = StateMissing
				s.Detail = "Credential check could not complete"
				var ve *verificationError
				switch {
				case errors.As(err, &ve):
					s.State = ve.state
				case errors.Is(err, ErrExpired):
					s.State = StateExpired
				case errors.Is(err, ErrOAuthRemoved):
					s.State, s.Detail = StateExpired, ErrOAuthRemoved.Error()
				case errors.Is(err, context.DeadlineExceeded):
					s.State = StateOffline
				}
			}
		}
		out = append(out, s)
	}
	if r.Connection != "" && len(out) == 0 {
		return nil, ErrDisconnected
	}
	return out, nil
}
func (m *Manager) Logout(ctx context.Context, name string) (LogoutResult, error) {
	if name == "" {
		return LogoutResult{}, errors.New("select a connection to log out")
	}
	unlock, e := m.lock(ctx)
	if e != nil {
		return LogoutResult{}, e
	}
	defer unlock()
	r, e := m.readRegistry()
	if e != nil {
		return LogoutResult{}, e
	}
	c, ok := r.Connections[name]
	if !ok {
		return LogoutResult{}, ErrDisconnected
	}
	delete(r.Connections, name)
	if e = m.writeRegistry(r); e != nil {
		return LogoutResult{}, e
	}
	var failed bool
	for _, key := range []string{c.SecretRef, c.RefreshRef, c.ClientSecretRef} {
		if key != "" {
			if e = m.secrets.Delete(ctx, key); e != nil && !errors.Is(e, ErrSecretMissing) {
				failed = true
			}
		}
	}
	result := LogoutResult{Connection: name, ManagementURL: managementURL(c), LocalOnly: true}
	if failed {
		return result, fmt.Errorf("connection disconnected but secure-store cleanup failed: %w", ErrSecureStore)
	}
	return result, nil
}
