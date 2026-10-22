package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCLIInboxAck(t *testing.T) {
	for _, mode := range []string{"ok", "failed", "short", "live", "malformed", "ack-failed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TMPDIR", "/tmp")
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			dir, err := daemon.DefaultRuntimeDir()
			if err != nil {
				t.Fatal(err)
			}
			server, err := localipc.Listen(dir)
			if err != nil {
				t.Fatal(err)
			}
			writer := &cliInboxWriter{mode: mode}
			var acks atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, func(_ context.Context, r localipc.Request) (any, error) {
					if r.Method == "inbox.delivered" {
						if !writer.written.Load() || r.Session != "chosen" || r.MessageID != strings.Repeat("a", 32) {
							t.Error("inbox acknowledged before complete output or with wrong authority")
						}
						acks.Add(1)
						if mode == "ack-failed" {
							return nil, localipc.NewError("unavailable", "pending")
						}
						return map[string]bool{"delivered": true}, nil
					}
					if mode == "malformed" {
						return map[string]string{"messages": "invalid"}, nil
					}
					route := "inbox_no_wake"
					if mode == "live" {
						route = "live"
					}
					return map[string]any{"messages": []map[string]string{{"message_id": strings.Repeat("a", 32), "routing": route}}}, nil
				})
			}()
			t.Cleanup(func() {
				cancel()
				if serveErr := <-done; serveErr != nil {
					t.Error(serveErr)
				}
				if closeErr := server.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			var stderr bytes.Buffer
			code := runInput([]string{"inbox", "--session", "chosen"}, strings.NewReader(""), writer, &stderr)
			wantAck := mode == "ok" || mode == "ack-failed"
			if (acks.Load() == 1) != wantAck {
				t.Fatalf("acks=%d mode=%s", acks.Load(), mode)
			}
			wantSuccess := mode == "ok" || mode == "live"
			if (code == 0) != wantSuccess {
				t.Fatalf("code=%d mode=%s stderr=%s", code, mode, stderr.String())
			}
			if mode == "ack-failed" && (!json.Valid(bytes.TrimSpace(writer.Bytes())) || strings.Count(writer.String(), "\n") != 1 || stderr.Len() == 0) {
				t.Fatal("ack failure emitted a second stdout result or lost its diagnostic")
			}
		})
	}
}

type cliInboxWriter struct {
	bytes.Buffer
	mode    string
	written atomic.Bool
}

func (w *cliInboxWriter) Write(data []byte) (int, error) {
	if w.mode == "failed" {
		return 0, io.ErrClosedPipe
	}
	if w.mode == "short" {
		return len(data) - 1, nil
	}
	n, err := w.Buffer.Write(data)
	w.written.Store(err == nil && n == len(data))
	return n, err
}
