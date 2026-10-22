package keystore

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

type testServiceConnection struct{ close func() error }

func (c testServiceConnection) Close() error { return c.close() }

func TestSecretServiceStoreReloadUsesFreshClosedConnections(t *testing.T) {
	key := bytes.Repeat([]byte{19}, 32)
	const collection dbus.ObjectPath = "/org/freedesktop/secrets/collection/login"
	const item dbus.ObjectPath = collection + "/one"
	connections := 0
	closed := map[int]bool{}
	var stored []byte
	backend := secretServiceBackend{connect: func(context.Context) (serviceConnection, busCall, error) {
		connections++
		connection := connections
		if connection > 1 && !closed[connection-1] {
			t.Fatal("previous connection remains open")
		}
		session := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/session/%d", connection))
		call := func(_ context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
			if closed[connection] {
				t.Fatal("closed connection reused")
			}
			switch method {
			case serviceInterface + ".SearchItems":
				var items []dbus.ObjectPath
				if stored != nil {
					items = []dbus.ObjectPath{item}
				}
				return &dbus.Call{Body: []any{items, []dbus.ObjectPath{}}}
			case serviceInterface + ".ReadAlias":
				if args[0] != "default" {
					t.Fatal("unexpected collection alias")
				}
				return &dbus.Call{Body: []any{collection}}
			case serviceInterface + ".OpenSession":
				return &dbus.Call{Body: []any{dbus.MakeVariant(""), session}}
			case "org.freedesktop.Secret.Collection.CreateItem":
				if path != collection || connection != 1 || args[2] != false {
					t.Fatal("unsafe key insertion")
				}
				secret, ok := args[1].(serviceSecret)
				if !ok {
					t.Fatal("invalid key transfer type")
				}
				stored = bytes.Clone(secret.Value)
				return &dbus.Call{Body: []any{item, dbus.ObjectPath("/")}}
			case "org.freedesktop.Secret.Item.GetSecret":
				if path != item || connection != 2 {
					t.Fatal("key read did not use fresh connection")
				}
				return &dbus.Call{Body: []any{serviceSecret{Session: session, Value: bytes.Clone(stored), ContentType: "application/octet-stream"}}}
			case "org.freedesktop.Secret.Session.Close":
				return &dbus.Call{}
			default:
				t.Fatalf("unexpected call %s", method)
				return nil
			}
		}
		return testServiceConnection{close: func() error { closed[connection] = true; return nil }}, call, nil
	}}
	id := strings.Repeat("a", 32)
	if err := backend.Store(t.Context(), id, key); err != nil {
		t.Fatal(err)
	}
	loaded, err := backend.Load(t.Context(), id)
	if err != nil || !bytes.Equal(loaded, key) || connections != 2 || !closed[2] {
		t.Fatal("key verification did not complete over a fresh closed connection", err)
	}
}
