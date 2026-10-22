package localipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
)

// Handler processes an authenticated and decoded local request.
type Handler func(context.Context, Request) (any, error)

// Server owns the daemon socket and the independent lifetime lock.
type Server struct {
	listener    *net.UnixListener
	lock        *os.File
	directory   *os.File
	path        string
	info        os.FileInfo
	stateMu     sync.Mutex
	serving     bool
	stopped     bool
	cancel      context.CancelFunc
	done        chan struct{}
	stopOnce    sync.Once
	listenerErr error
	once        sync.Once
	closeErr    error
	leases      chan struct{}
}

// Listen acquires the single-instance lock before removing a stale socket.
func Listen(runtimeDir string) (*Server, error) {
	directory, err := privateDirectory(runtimeDir, true)
	if err != nil {
		return nil, err
	}
	lock, err := lockDirectory(directory, "daemon.lock")
	if err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	server := &Server{lock: lock, directory: directory, path: filepath.Join(runtimeDir, SocketName), done: make(chan struct{}), leases: make(chan struct{}, 128)}
	if _, err = os.Lstat(server.path); err == nil {
		if _, err = privateSocket(server.path); err != nil {
			return nil, errors.Join(err, server.Close())
		}
		if err = os.Remove(server.path); err != nil {
			return nil, errors.Join(NewError("unavailable", "unavailable"), server.Close())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(NewError("unavailable", "unavailable"), server.Close())
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.path, Net: "unix"})
	if err != nil {
		return nil, errors.Join(NewError("unavailable", "unavailable"), server.Close())
	}
	listener.SetUnlinkOnClose(false)
	server.listener = listener
	if err = os.Chmod(server.path, 0o600); err != nil {
		return nil, errors.Join(NewError("unavailable", "unavailable"), server.Close())
	}
	server.info, err = privateSocket(server.path)
	if err != nil {
		return nil, errors.Join(err, server.Close())
	}
	return server, nil
}

// Close releases the socket before releasing its independent daemon lock.
func (s *Server) Close() error {
	s.stateMu.Lock()
	s.stopped = true
	cancel, serving := s.cancel, s.serving
	s.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.stopListener()
	if serving {
		<-s.done
	}
	return s.release()
}

func (s *Server) stopListener() {
	s.stopOnce.Do(func() {
		if s.listener != nil {
			s.listenerErr = s.listener.Close()
		}
	})
}

func (s *Server) release() error {
	s.once.Do(func() {
		s.closeErr = s.listenerErr
		if s.info != nil {
			info, err := os.Lstat(s.path)
			if err == nil && os.SameFile(s.info, info) {
				s.closeErr = errors.Join(s.closeErr, os.Remove(s.path))
			}
		}
		s.closeErr = errors.Join(s.closeErr, s.lock.Close(), s.directory.Close())
	})
	return s.closeErr
}

// Serve drains clients on shutdown. Close retains ownership of the lifetime lock.
func (s *Server) Serve(ctx context.Context, handler Handler) error {
	serveCtx, cancel := context.WithCancel(ctx)
	s.stateMu.Lock()
	if s.serving || s.stopped {
		s.stateMu.Unlock()
		cancel()
		return NewError("unavailable", "stopped")
	}
	s.serving, s.cancel = true, cancel
	s.stateMu.Unlock()
	stop := context.AfterFunc(serveCtx, s.stopListener)
	defer stop()
	var clients sync.WaitGroup
	defer func() {
		cancel()
		s.stopListener()
		clients.Wait()
		s.stateMu.Lock()
		s.serving, s.stopped = false, true
		close(s.done)
		s.stateMu.Unlock()
	}()
	capacity := make(chan struct{}, 64)
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			cancel()
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return NewError("unavailable", "unavailable")
		}
		select {
		case capacity <- struct{}{}:
			release := sync.OnceFunc(func() { <-capacity })
			connectionCtx := context.WithValue(serveCtx, requestSlotKey{}, release)
			clients.Go(func() { defer release(); s.handle(connectionCtx, conn, handler) })
		default:
			closeConnection(conn)
		}
	}
}

// Run is the foreground convenience form of Listen followed by Serve.
func Run(ctx context.Context, runtimeDir string, handler Handler) (err error) {
	server, err := Listen(runtimeDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, server.Close()) }()
	return server.Serve(ctx, handler)
}

func (s *Server) handle(parent context.Context, conn *net.UnixConn, handler Handler) {
	defer closeConnection(conn)
	if err := verifyUID(conn, os.Getuid()); err != nil {
		return
	}
	lifetime, disconnected := context.WithCancel(parent)
	defer disconnected()
	ctx, cancel := context.WithTimeout(lifetime, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { closeConnection(conn) })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}
	data, err := readFrame(conn)
	var request Request
	if err == nil {
		err = codec.Decode(data, MaxFrame, &request)
	}
	if err == nil {
		err = request.Validate()
	}
	if err == nil && request.Lease {
		select {
		case s.leases <- struct{}{}:
			defer func() { <-s.leases }()
			ctx = context.WithValue(ctx, connectionLifetimeKey{}, lifetime)
		default:
			err = NewError("backpressure", "pending")
		}
	}
	var value any
	if err == nil {
		// The one-request connection lets a disconnect cancel a long-running call.
		go func() {
			defer disconnected()
			var extra [1]byte
			if _, readErr := conn.Read(extra[:]); readErr != nil {
				return
			}
		}()
		value, err = handler(ctx, request)
	}
	reply := response{}
	if err != nil {
		reply.Error = PublicError(err)
	} else {
		reply.Result, err = json.Marshal(value)
		if err != nil {
			reply.Error = PublicError(err)
		}
	}
	encoded, err := json.Marshal(reply)
	if err == nil && len(encoded) > MaxFrame {
		encoded, err = json.Marshal(response{Error: NewError("too_large", "rejected")})
	}
	if err == nil {
		if err = writeFrame(conn, encoded); err != nil {
			return
		}
	}
	if err == nil && reply.Error == nil && request.Lease {
		stop()
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return
		}
		// Idle leases have their own bound and cannot consume the request slots
		// needed by owner status, stop or other short-lived control calls.
		if release, ok := parent.Value(requestSlotKey{}).(func()); ok {
			release()
		}
		<-lifetime.Done()
	}
}

func closeConnection(conn *net.UnixConn) {
	if err := conn.Close(); err != nil {
		return
	}
}
