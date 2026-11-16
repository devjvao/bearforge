package auth

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

const secretService = "org.freedesktop.secrets"
const serviceInterface = "org.freedesktop.Secret.Service"
const itemInterface = "org.freedesktop.Secret.Item"
const collectionInterface = "org.freedesktop.Secret.Collection"

type systemSecretStore struct{}
type dbusSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func NewSystemSecretStore() SecretStore { return systemSecretStore{} }
func secretBus(ctx context.Context) (*dbus.Conn, context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, e := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if e != nil {
		cancel()
		return nil, ctx, func() {}, ErrSecureStore
	}
	if e = c.Auth(nil); e == nil {
		e = c.Hello()
	}
	if e != nil {
		c.Close()
		cancel()
		return nil, ctx, func() {}, ErrSecureStore
	}
	return c, ctx, func() { c.Close(); cancel() }, nil
}
func searchSecrets(ctx context.Context, c *dbus.Conn, key string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	e := c.Object(secretService, "/org/freedesktop/secrets").CallWithContext(ctx, serviceInterface+".SearchItems", 0, map[string]string{"application": "bearforge", "reference": key}).Store(&unlocked, &locked)
	if e != nil || len(locked) > 0 {
		return nil, ErrSecureStore
	}
	return unlocked, nil
}
func secretSession(ctx context.Context, c *dbus.Conn) (dbus.ObjectPath, error) {
	var output dbus.Variant
	var session dbus.ObjectPath
	e := c.Object(secretService, "/org/freedesktop/secrets").CallWithContext(ctx, serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&output, &session)
	if e != nil {
		return "", ErrSecureStore
	}
	return session, nil
}
func (systemSecretStore) Get(ctx context.Context, key string) (string, error) {
	c, ctx, close, e := secretBus(ctx)
	if e != nil {
		return "", e
	}
	defer close()
	items, e := searchSecrets(ctx, c, key)
	if e != nil {
		return "", e
	}
	if len(items) == 0 {
		return "", ErrSecretMissing
	}
	if len(items) != 1 {
		return "", ErrSecureStore
	}
	session, e := secretSession(ctx, c)
	if e != nil {
		return "", e
	}
	var secret dbusSecret
	e = c.Object(secretService, items[0]).CallWithContext(ctx, itemInterface+".GetSecret", 0, session).Store(&secret)
	if e != nil {
		return "", ErrSecureStore
	}
	return string(secret.Value), nil
}
func (systemSecretStore) Set(ctx context.Context, key, value string) error {
	c, ctx, close, e := secretBus(ctx)
	if e != nil {
		return e
	}
	defer close()
	var collection dbus.ObjectPath
	e = c.Object(secretService, "/org/freedesktop/secrets").CallWithContext(ctx, serviceInterface+".ReadAlias", 0, "default").Store(&collection)
	if e != nil || collection == "/" {
		return ErrSecureStore
	}
	object := c.Object(secretService, collection)
	var locked dbus.Variant
	e = object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, collectionInterface, "Locked").Store(&locked)
	if e != nil || locked.Value() != false {
		return ErrSecureStore
	}
	session, e := secretSession(ctx, c)
	if e != nil {
		return e
	}
	var item, prompt dbus.ObjectPath
	properties := map[string]dbus.Variant{itemInterface + ".Label": dbus.MakeVariant("Bearforge provider credential"), itemInterface + ".Attributes": dbus.MakeVariant(map[string]string{"application": "bearforge", "reference": key})}
	e = object.CallWithContext(ctx, collectionInterface+".CreateItem", 0, properties, dbusSecret{session, []byte{}, []byte(value), "text/plain; charset=utf8"}, true).Store(&item, &prompt)
	if e != nil || prompt != "/" {
		return ErrSecureStore
	}
	return nil
}
func (systemSecretStore) Delete(ctx context.Context, key string) error {
	c, ctx, close, e := secretBus(ctx)
	if e != nil {
		return e
	}
	defer close()
	items, e := searchSecrets(ctx, c, key)
	if e != nil {
		return e
	}
	for _, item := range items {
		var prompt dbus.ObjectPath
		if e = c.Object(secretService, item).CallWithContext(ctx, itemInterface+".Delete", 0).Store(&prompt); e != nil || prompt != "/" {
			return ErrSecureStore
		}
	}
	return nil
}
