package codex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type responderCapture struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	size     int
	overflow bool
	cancel   context.CancelFunc
}

func (c *responderCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) > c.limit-c.size {
		c.overflow = true
		c.cancel()
		return 0, &ResponderError{Reason: "budget_exceeded"}
	}
	c.size += len(p)
	return c.buf.Write(p)
}

func runResponderProcess(ctx context.Context, command ResponderCommand, limit int) ([]byte, error) {
	if !filepath.IsAbs(command.Executable) || limit <= 0 {
		return nil, &ResponderError{Reason: "responder_unavailable"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := &responderCapture{limit: limit, cancel: cancel}
	// The absolute executable and argv remain distinct. Cancellation supervises
	// the whole process group, including descendants which retain output pipes.
	cmd := &exec.Cmd{Path: command.Executable, Args: append([]string{command.Executable}, command.Args...)}
	cmd.Dir = command.Directory
	cmd.Env = command.Environment
	cmd.Stdin = bytes.NewReader(command.Stdin)
	cmd.Stdout = capture
	// Diagnostics share the byte cap but never enter the model-answer decoder.
	cmd.Stderr = &responderDiscard{capture: capture}
	cmd.WaitDelay = 500 * time.Millisecond
	if err := isolateResponderProcess(cmd); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, &ResponderError{Reason: "cancelled"}
	}
	if err := cmd.Start(); err != nil {
		return nil, &ResponderError{Reason: "responder_failed"}
	}
	processID := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		if stopErr := stopResponderProcess(processID); stopErr != nil {
			// A sandbox can reject a signal after a short-lived child has
			// exited. Reap it before classifying the already-detected overflow.
			select {
			case err = <-done:
			default:
				if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
					return nil, &ResponderError{Reason: "responder_failed"}
				}
				err = <-done
			}
		} else {
			err = <-done
		}
	}
	stopErr := stopResponderProcess(processID)
	if capture.overflow {
		return nil, &ResponderError{Reason: "budget_exceeded"}
	}
	if ctx.Err() != nil {
		return nil, &ResponderError{Reason: "cancelled"}
	}
	if err != nil || stopErr != nil {
		return nil, &ResponderError{Reason: "responder_failed"}
	}
	return append([]byte(nil), capture.buf.Bytes()...), nil
}

type responderDiscard struct {
	capture *responderCapture
}

func (d *responderDiscard) Write(p []byte) (int, error) {
	d.capture.mu.Lock()
	defer d.capture.mu.Unlock()
	if len(p) > d.capture.limit-d.capture.size {
		d.capture.overflow = true
		d.capture.cancel()
		return 0, io.ErrShortWrite
	}
	d.capture.size += len(p)
	return len(p), nil
}
