package localipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
)

// Client connects only to the private Unix endpoint in RuntimeDir.
type Client struct{ RuntimeDir, NativeThread, BindingEpoch, ProfileID string }

// Call authenticates the daemon uid and exchanges one bounded request.
func (c Client) Call(ctx context.Context, request Request) (json.RawMessage, error) {
	result, conn, err := c.exchange(ctx, request)
	if conn != nil {
		defer closeConnection(conn)
	}
	return result, err
}

func (c Client) exchange(ctx context.Context, request Request) (_ json.RawMessage, connection *net.UnixConn, result error) {
	if c.ProfileID != "" {
		if request.ProfileID != "" && request.ProfileID != c.ProfileID {
			return nil, nil, NewError("conflict", "rejected")
		}
		request.ProfileID = c.ProfileID
	}
	if request.Method == "hello" && request.BindingEpoch == "" {
		request.BindingEpoch = c.BindingEpoch
	}
	if request.NativeThread == "" {
		request.NativeThread = c.NativeThread
	}
	defer func() {
		if result != nil && connection != nil {
			closeConnection(connection)
			connection = nil
		}
	}()
	if err := request.Validate(); err != nil {
		return nil, nil, err
	}
	dir, err := privateDirectory(c.RuntimeDir, false)
	if err != nil {
		return nil, nil, err
	}
	if err = dir.Close(); err != nil {
		return nil, nil, NewError("unavailable", "unavailable")
	}
	path := filepath.Join(c.RuntimeDir, SocketName)
	if _, err = privateSocket(path); err != nil {
		return nil, nil, NewError("unavailable", "unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, nil, PublicError(err)
	}
	conn, ok := raw.(*net.UnixConn)
	if !ok {
		return nil, nil, errors.Join(NewError("unsupported", "unavailable"), raw.Close())
	}
	connection = conn
	if err = verifyUID(conn, os.Getuid()); err != nil {
		return nil, connection, err
	}
	stop := context.AfterFunc(ctx, func() { closeConnection(conn) })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, connection, PublicError(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, connection, PublicError(err)
	}
	if err = writeFrame(conn, encoded); err != nil {
		return nil, connection, PublicError(err)
	}
	data, err := readFrame(conn)
	if err != nil {
		if ctx.Err() != nil {
			return nil, connection, PublicError(ctx.Err())
		}
		return nil, connection, PublicError(err)
	}
	var reply response
	if err := codec.Decode(data, MaxFrame, &reply); err != nil {
		return nil, connection, err
	}
	if reply.Error != nil {
		return nil, connection, reply.Error
	}
	if len(reply.Result) == 0 {
		return nil, connection, NewError("invalid_encoding", "rejected")
	}
	return reply.Result, connection, nil
}
