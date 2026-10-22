package keystore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const destination = "org.freedesktop.secrets"
const servicePath dbus.ObjectPath = "/org/freedesktop/secrets"
const serviceInterface = "org.freedesktop.Secret.Service"

type secretServiceBackend struct {
	connect func(context.Context) (serviceConnection, busCall, error)
}
type serviceConnection interface{ Close() error }
type serviceSecret struct {
	Session           dbus.ObjectPath
	Parameters, Value []byte
	ContentType       string
}
type busCall func(context.Context, dbus.ObjectPath, string, ...any) *dbus.Call

func connectService(ctx context.Context) (serviceConnection, busCall, error) {
	address, err := sessionBusAddress()
	if err != nil {
		return nil, nil, err
	}
	// ConnectSessionBus may launch dbus-launch. An explicit Unix address keeps
	// the backend entirely in Go and never launches or autostarts a session bus.
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	call := func(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		return conn.Object(destination, path).CallWithContext(ctx, method, 0, args...)
	}
	return conn, call, nil
}

func (b secretServiceBackend) connection(ctx context.Context) (serviceConnection, busCall, error) {
	if b.connect != nil {
		return b.connect(ctx)
	}
	return connectService(ctx)
}

func (b secretServiceBackend) Load(ctx context.Context, id string) (_ []byte, result error) {
	if !validID(id) {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, call, err := b.connection(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() {
		if err = conn.Close(); err != nil {
			result = ErrUnavailable
		}
	}()
	return loadSecret(ctx, call, id)
}

func loadSecret(ctx context.Context, call busCall, id string) ([]byte, error) {
	unlocked, locked, err := searchSecrets(ctx, call, id)
	if err != nil || len(locked) != 0 || len(unlocked) != 1 {
		return nil, ErrUnavailable
	}
	if !persistentItem(unlocked[0]) {
		return nil, ErrUnavailable
	}
	session, err := openSession(ctx, call)
	if err != nil {
		return nil, err
	}
	defer closeSession(ctx, call, session)
	var secret serviceSecret
	if err = call(ctx, unlocked[0], "org.freedesktop.Secret.Item.GetSecret", session).Store(&secret); err != nil {
		return nil, ErrUnavailable
	}
	// GNOME Keyring 42 normalizes the content type to text/plain even for an
	// octet-stream item. The key is raw bytes; its size and session are decisive.
	if secret.Session != session || len(secret.Parameters) != 0 || len(secret.Value) != 32 || (secret.ContentType != "application/octet-stream" && secret.ContentType != "text/plain") {
		clear(secret.Value)
		return nil, ErrUnavailable
	}
	return secret.Value, nil
}

func (b secretServiceBackend) Store(ctx context.Context, id string, key []byte) (result error) {
	if !validID(id) || len(key) != 32 {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, call, err := b.connection(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		if err = conn.Close(); err != nil {
			result = ErrUnavailable
		}
	}()
	return storeSecret(ctx, call, id, key)
}

func storeSecret(ctx context.Context, call busCall, id string, key []byte) error {
	unlocked, locked, err := searchSecrets(ctx, call, id)
	if err != nil || len(unlocked) != 0 || len(locked) != 0 {
		return ErrUnavailable
	}
	var collection dbus.ObjectPath
	if err = call(ctx, servicePath, serviceInterface+".ReadAlias", "default").Store(&collection); err != nil || !persistentCollection(collection) {
		return ErrUnavailable
	}
	session, err := openSession(ctx, call)
	if err != nil {
		return err
	}
	defer closeSession(ctx, call, session)
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("PeerTurn wrapping key"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"application": service, "key_id": id}),
	}
	secret := serviceSecret{Session: session, Parameters: []byte{}, Value: key, ContentType: "application/octet-stream"}
	var item, prompt dbus.ObjectPath
	if err = call(ctx, collection, "org.freedesktop.Secret.Collection.CreateItem", props, secret, false).Store(&item, &prompt); err != nil || prompt != "/" || !persistentItem(item) || !strings.HasPrefix(string(item), string(collection)+"/") {
		return ErrUnavailable
	}
	return nil
}

func persistentCollection(path dbus.ObjectPath) bool {
	name, ok := strings.CutPrefix(string(path), string(servicePath)+"/collection/")
	// The session collection is volatile even when selected by the default
	// alias. Require its canonical sibling path rather than accepting aliases.
	return ok && path.IsValid() && name != "" && name != "session" && !strings.Contains(name, "/")
}

func persistentItem(path dbus.ObjectPath) bool {
	separator := strings.LastIndex(string(path), "/")
	return separator > 0 && path.IsValid() && persistentCollection(dbus.ObjectPath(string(path)[:separator]))
}

func searchSecrets(ctx context.Context, call busCall, id string) (unlocked, locked []dbus.ObjectPath, err error) {
	err = call(ctx, servicePath, serviceInterface+".SearchItems", map[string]string{"application": service, "key_id": id}).Store(&unlocked, &locked)
	if err != nil {
		err = ErrUnavailable
	}
	return
}

func openSession(ctx context.Context, call busCall) (dbus.ObjectPath, error) {
	var output dbus.Variant
	var path dbus.ObjectPath
	if err := call(ctx, servicePath, serviceInterface+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &path); err != nil || path == "/" || !path.IsValid() {
		return "", ErrUnavailable
	}
	return path, nil
}

func closeSession(ctx context.Context, call busCall, path dbus.ObjectPath) {
	// Connection close is also a session fence; no application work follows.
	_ = call(ctx, path, "org.freedesktop.Secret.Session.Close").Err
}

func sessionBusAddress() (string, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if !filepath.IsAbs(runtimeDir) {
			return "", ErrUnavailable
		}
		address = "unix:path=" + dbus.EscapeBusAddressValue(filepath.Join(runtimeDir, "bus"))
	}
	if !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") || len(address) > 4096 {
		return "", ErrUnavailable
	}
	return address, nil
}
