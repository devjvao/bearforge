package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

type registry struct {
	Version     int                   `json:"version"`
	Connections map[string]Connection `json:"connections"`
}

func (m *Manager) readRegistry() (registry, error) {
	r := registry{Version: 1, Connections: map[string]Connection{}}
	f, e := os.Open(m.path())
	if errors.Is(e, os.ErrNotExist) {
		return r, nil
	}
	if e != nil {
		return r, errors.New("cannot read authentication registry")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 2<<20))
	if e = d.Decode(&r); e != nil {
		return r, errors.New("invalid authentication registry")
	}
	if r.Version != 1 || r.Connections == nil {
		return r, errors.New("unsupported authentication registry version")
	}
	return r, nil
}
func (m *Manager) lock(ctx context.Context) (func(), error) {
	if m.path() == "" {
		return nil, errors.New("user configuration directory is unavailable")
	}
	if e := os.MkdirAll(filepath.Dir(m.path()), 0700); e != nil {
		return nil, e
	}
	l := flock.New(m.path() + ".lock")
	ok, e := l.TryLockContext(ctx, 25*time.Millisecond)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, context.Canceled
	}
	return func() { _ = l.Unlock() }, nil
}
func (m *Manager) writeRegistry(r registry) error {
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(m.path()), ".connections-*")
	if e != nil {
		return e
	}
	p := f.Name()
	defer os.Remove(p)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(p, m.path())
}
func (m *Manager) Connections(ctx context.Context) ([]Connection, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r, e := m.readRegistry()
	if e != nil {
		return nil, e
	}
	out := make([]Connection, 0, len(r.Connections))
	for _, c := range r.Connections {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *Manager) Connection(ctx context.Context, name string) (Connection, error) {
	if e := ctx.Err(); e != nil {
		return Connection{}, e
	}
	r, e := m.readRegistry()
	if e != nil {
		return Connection{}, e
	}
	c, ok := r.Connections[name]
	if !ok {
		return c, ErrDisconnected
	}
	return c, nil
}
func randomID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic("cryptographic random source unavailable")
	}
	return hex.EncodeToString(b)
}
func (m *Manager) save(ctx context.Context, c Connection, access, refresh, clientSecret string) (Connection, error) {
	unlock, e := m.lock(ctx)
	if e != nil {
		return Connection{}, e
	}
	defer unlock()
	r, e := m.readRegistry()
	if e != nil {
		return Connection{}, e
	}
	old := r.Connections[c.Name]
	if e = m.saveGeneration(ctx, &c, access, refresh, clientSecret); e != nil {
		return Connection{}, e
	}
	r.Connections[c.Name] = c
	if e = m.writeRegistry(r); e != nil {
		m.deleteSecrets(context.WithoutCancel(ctx), c)
		return Connection{}, e
	}
	m.deleteSecrets(context.WithoutCancel(ctx), old)
	return c, nil
}
func (m *Manager) saveGeneration(ctx context.Context, c *Connection, access, refresh, clientSecret string) error {
	generation := randomID()
	c.SecretRef = "bearforge/" + c.GrantID + "/" + generation + "/access"
	c.RefreshRef = ""
	c.ClientSecretRef = ""
	values := map[string]string{c.SecretRef: access}
	if refresh != "" {
		c.RefreshRef = "bearforge/" + c.GrantID + "/" + generation + "/refresh"
		values[c.RefreshRef] = refresh
	}
	if clientSecret != "" {
		c.ClientSecretRef = "bearforge/" + c.GrantID + "/" + generation + "/client"
		values[c.ClientSecretRef] = clientSecret
	}
	for key, value := range values {
		if e := m.secrets.Set(ctx, key, value); e != nil {
			m.deleteSecrets(context.WithoutCancel(ctx), *c)
			return fmt.Errorf("save credential: %w", ErrSecureStore)
		}
	}
	return nil
}
func (m *Manager) deleteSecrets(ctx context.Context, c Connection) {
	for _, key := range []string{c.SecretRef, c.RefreshRef, c.ClientSecretRef} {
		if key != "" {
			_ = m.secrets.Delete(ctx, key)
		}
	}
}
