package relay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func TestAbruptRestartCustody(t *testing.T) {
	if path := os.Getenv("PEERTURN_CRASH_DB"); path != "" {
		s, err := store.Open(path, store.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err = s.CreateMailbox(t.Context(), store.Mailbox{ID: "box", ReadKey: "read"}, ""); err != nil {
			t.Fatal(err)
		}
		if err = s.PutPermit(t.Context(), "box", "read", store.Permit{ID: "write", WriterKey: "writer", Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		_, err = s.PutMessage(t.Context(), store.Message{MailboxID: "box", PermitID: "write", WriterKey: "writer", PermitEpoch: 1, ID: "m1", Packet: bytes.Repeat([]byte{0x95}, 128), ExpiresAt: now.Add(time.Hour)}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fmt.Fprintln(os.Stdout, "committed"); err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "relay.sqlite")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The executable comes from the OS, with no shell, PATH lookup or request input.
	cmd := &exec.Cmd{Path: executable, Args: []string{executable, "-test.run=^TestAbruptRestartCustody$"}}
	childContext, stopChild := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopChild()
	cmd.Env = append(os.Environ(), "PEERTURN_CRASH_DB="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := stdin.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			t.Error(closeErr)
		}
	}()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan struct{})
	defer close(childDone)
	go func() {
		select {
		case <-childContext.Done():
			if killErr := cmd.Process.Kill(); killErr != nil {
				return
			}
		case <-childDone:
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "committed\n" {
		killErr := cmd.Process.Kill()
		waitErr := cmd.Wait()
		t.Fatalf("child did not commit: line=%q read=%v kill=%v wait=%v", line, err, killErr, waitErr)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("expected abrupt process death")
	}
	s, err := store.Open(path, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	rows, err := s.Fetch(t.Context(), "box", "read", 0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Seq != 1 || len(rows[0].Packet) != 128 {
		t.Fatalf("lost committed ciphertext: %+v", rows)
	}
	if err = s.Ack(t.Context(), "box", "read", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.PutMessage(t.Context(), rows[0], time.Now())
	if err != nil || receipt.Seq != 1 || !receipt.Duplicate {
		t.Fatalf("ACK-loss retry created new custody: %+v %v", receipt, err)
	}
}
