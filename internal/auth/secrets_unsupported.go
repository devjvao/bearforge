//go:build (!windows && !linux && !darwin) || (darwin && !cgo)

package auth

import "context"

type systemSecretStore struct{}

func NewSystemSecretStore() SecretStore                               { return systemSecretStore{} }
func (systemSecretStore) Get(context.Context, string) (string, error) { return "", ErrSecureStore }
func (systemSecretStore) Set(context.Context, string, string) error   { return ErrSecureStore }
func (systemSecretStore) Delete(context.Context, string) error        { return ErrSecureStore }
