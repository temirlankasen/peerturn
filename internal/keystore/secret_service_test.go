package keystore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestSecretServiceRejectsLockedAndAmbiguousItems(t *testing.T) {
	for _, counts := range [][2]int{{0, 1}, {1, 1}, {2, 0}, {0, 0}} {
		call := func(_ context.Context, _ dbus.ObjectPath, method string, _ ...any) *dbus.Call {
			if !strings.HasSuffix(method, "SearchItems") {
				t.Fatal("locked/ambiguous lookup continued")
			}
			return &dbus.Call{Body: []any{make([]dbus.ObjectPath, counts[0]), make([]dbus.ObjectPath, counts[1])}}
		}
		if _, err := loadSecret(t.Context(), call, strings.Repeat("a", 32)); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unsafe lookup accepted")
		}
	}
}

func TestSecretServicePlainSessionTransfersOnlyExpectedKey(t *testing.T) {
	key := bytes.Repeat([]byte{17}, 32)
	session := dbus.ObjectPath("/session/one")
	closed := false
	call := func(_ context.Context, _ dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case serviceInterface + ".SearchItems":
			return &dbus.Call{Body: []any{[]dbus.ObjectPath{"/org/freedesktop/secrets/collection/login/one"}, []dbus.ObjectPath{}}}
		case serviceInterface + ".OpenSession":
			if args[0] != "plain" {
				t.Fatal("unreviewed transport crypto")
			}
			return &dbus.Call{Body: []any{dbus.MakeVariant(""), session}}
		case "org.freedesktop.Secret.Item.GetSecret":
			return &dbus.Call{Body: []any{serviceSecret{Session: session, Value: key, ContentType: "text/plain"}}}
		case "org.freedesktop.Secret.Session.Close":
			closed = true
			return &dbus.Call{}
		default:
			t.Fatalf("unexpected call %s", method)
			return nil
		}
	}
	got, err := loadSecret(t.Context(), call, strings.Repeat("a", 32))
	if err != nil || !bytes.Equal(got, key) || !closed {
		t.Fatalf("incomplete key retrieval: %v", err)
	}
}

func TestSecretServiceStoreNeverReplacesOrPrompts(t *testing.T) {
	for _, prompt := range []dbus.ObjectPath{"/", "/prompt/one"} {
		call := func(_ context.Context, _ dbus.ObjectPath, method string, args ...any) *dbus.Call {
			switch method {
			case serviceInterface + ".SearchItems":
				return &dbus.Call{Body: []any{[]dbus.ObjectPath{}, []dbus.ObjectPath{}}}
			case serviceInterface + ".ReadAlias":
				return &dbus.Call{Body: []any{dbus.ObjectPath("/org/freedesktop/secrets/collection/login")}}
			case serviceInterface + ".OpenSession":
				return &dbus.Call{Body: []any{dbus.MakeVariant(""), dbus.ObjectPath("/session/one")}}
			case "org.freedesktop.Secret.Collection.CreateItem":
				if args[2] != false {
					t.Fatal("existing key replacement allowed")
				}
				return &dbus.Call{Body: []any{dbus.ObjectPath("/org/freedesktop/secrets/collection/login/one"), prompt}}
			case "org.freedesktop.Secret.Session.Close":
				return &dbus.Call{}
			default:
				t.Fatalf("unexpected prompt/call %s", method)
				return nil
			}
		}
		err := storeSecret(t.Context(), call, strings.Repeat("a", 32), make([]byte, 32))
		if (err == nil) != (prompt == "/") {
			t.Fatalf("prompt policy: %v", err)
		}
	}
}

func TestSecretServiceNeverAutolaunchesOrUsesRemoteBus(t *testing.T) {
	for _, address := range []string{"", "autolaunch:", "tcp:host=example.invalid,port=1234", "unix:path=/tmp/bus;tcp:host=example.invalid"} {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		t.Setenv("XDG_RUNTIME_DIR", "")
		if _, err := sessionBusAddress(); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unsupported bus accepted")
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/peerturn-runtime")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if address, err := sessionBusAddress(); err != nil || address != "unix:path=/tmp/peerturn-runtime/bus" {
		t.Fatal("runtime bus not selected")
	}
}

func TestSecretServiceRefusesSessionCollectionBeforeKeyTransfer(t *testing.T) {
	for _, collection := range []dbus.ObjectPath{
		"/org/freedesktop/secrets/collection/session",
		"/org/freedesktop/secrets/aliases/session",
	} {
		t.Run(string(collection), func(t *testing.T) {
			transferred := false
			call := func(_ context.Context, _ dbus.ObjectPath, method string, _ ...any) *dbus.Call {
				switch method {
				case serviceInterface + ".SearchItems":
					return &dbus.Call{Body: []any{[]dbus.ObjectPath{}, []dbus.ObjectPath{}}}
				case serviceInterface + ".ReadAlias":
					return &dbus.Call{Body: []any{collection}}
				case serviceInterface + ".OpenSession":
					return &dbus.Call{Body: []any{dbus.MakeVariant(""), dbus.ObjectPath("/session/one")}}
				case "org.freedesktop.Secret.Collection.CreateItem":
					transferred = true
					return &dbus.Call{Body: []any{dbus.ObjectPath("/item/one"), dbus.ObjectPath("/")}}
				case "org.freedesktop.Secret.Session.Close":
					return &dbus.Call{}
				default:
					t.Fatalf("unexpected call %s", method)
					return nil
				}
			}
			err := storeSecret(t.Context(), call, strings.Repeat("a", 32), make([]byte, 32))
			if !errors.Is(err, ErrUnavailable) || transferred {
				t.Fatalf("nonpersistent key storage accepted: transferred=%v err=%v", transferred, err)
			}
		})
	}
}

func TestSecretServiceRefusesLoadingSessionCollection(t *testing.T) {
	for _, item := range []dbus.ObjectPath{
		"/org/freedesktop/secrets/collection/session/one",
		"/org/freedesktop/secrets/aliases/session/one",
		"/org/freedesktop/secrets/aliases/default/one",
	} {
		call := func(_ context.Context, _ dbus.ObjectPath, method string, _ ...any) *dbus.Call {
			if method != serviceInterface+".SearchItems" {
				t.Fatal("volatile item reached key retrieval")
			}
			return &dbus.Call{Body: []any{[]dbus.ObjectPath{item}, []dbus.ObjectPath{}}}
		}
		if _, err := loadSecret(t.Context(), call, strings.Repeat("a", 32)); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("volatile item accepted: %s", item)
		}
	}
}
