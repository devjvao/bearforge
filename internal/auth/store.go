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

const registrySizeLimit = 2 << 20

// registryPublicationError means either registry generation may survive a crash.
// Callers must retain the secrets referenced by both generations.
type registryPublicationError struct{ cause error }

func (e *registryPublicationError) Error() string {
	return "authentication registry publication durability is uncertain: " + e.cause.Error()
}
func (e *registryPublicationError) Unwrap() error { return e.cause }

func replaceAndSyncFile(source, target string, replace func(string, string) error, syncDirectory func(string) error) (bool, error) {
	if err := replace(source, target); err != nil {
		return false, err
	}
	return true, syncDirectory(filepath.Dir(target))
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
	b, e := io.ReadAll(io.LimitReader(f, registrySizeLimit+1))
	if e != nil {
		return r, errors.New("cannot read authentication registry")
	}
	if len(b) > registrySizeLimit {
		return r, errors.New("authentication registry exceeds size limit")
	}
	if e = json.Unmarshal(b, &r); e != nil {
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
	if len(b) > registrySizeLimit {
		return errors.New("authentication registry exceeds size limit")
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
	publish := m.publishRegistry
	if publish == nil {
		publish = replaceFile
	}
	published, e := publish(p, m.path())
	if e != nil && published {
		return &registryPublicationError{cause: e}
	}
	return e
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
func (m *Manager) save(ctx context.Context, c Connection, access string) (Connection, error) {
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
	if e = m.saveGeneration(ctx, &c, access); e != nil {
		return Connection{}, e
	}
	r.Connections[c.Name] = c
	if e = m.writeRegistry(r); e != nil {
		var uncertain *registryPublicationError
		if !errors.As(e, &uncertain) {
			m.deleteSecrets(context.WithoutCancel(ctx), c)
		}
		return Connection{}, e
	}
	m.deleteSecrets(context.WithoutCancel(ctx), old)
	return c, nil
}
func (m *Manager) saveGeneration(ctx context.Context, c *Connection, access string) error {
	generation := randomID()
	c.SecretRef = "bearforge/" + c.GrantID + "/" + generation + "/access"
	c.RefreshRef = ""
	c.ClientSecretRef = ""
	c.ClientID, c.TokenURL = "", ""
	if e := m.secrets.Set(ctx, c.SecretRef, access); e != nil {
		m.deleteSecrets(context.WithoutCancel(ctx), *c)
		return fmt.Errorf("save credential: %w", ErrSecureStore)
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
