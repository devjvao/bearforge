package auth

import (
	"context"
	"errors"

	"github.com/danieljoos/wincred"
)

type systemSecretStore struct{}

func NewSystemSecretStore() SecretStore { return systemSecretStore{} }
func (systemSecretStore) Get(ctx context.Context, key string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	c, e := wincred.GetGenericCredential(key)
	if errors.Is(e, wincred.ErrElementNotFound) {
		return "", ErrSecretMissing
	}
	if e != nil {
		return "", ErrSecureStore
	}
	return string(c.CredentialBlob), ctx.Err()
}
func (systemSecretStore) Set(ctx context.Context, key, value string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if len(value) > 2560 {
		return ErrSecureStore
	}
	c := wincred.NewGenericCredential(key)
	c.CredentialBlob = []byte(value)
	if e := c.Write(); e != nil {
		return ErrSecureStore
	}
	return nil
}
func (systemSecretStore) Delete(ctx context.Context, key string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	c := wincred.NewGenericCredential(key)
	if e := c.Delete(); e != nil && !errors.Is(e, wincred.ErrElementNotFound) {
		return ErrSecureStore
	}
	return nil
}
