package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// ErrAlreadyRunning means another Host Daemon owns the socket.
var ErrAlreadyRunning = errors.New("a Host Daemon is already running")

// requestTimeout bounds one client connection, so a stuck client can't hold
// a server goroutine.
const requestTimeout = 2 * time.Second

// Listener is the daemon's end of the local socket. It holds the daemon lock
// until closed.
type Listener struct {
	net.Listener
	paths Paths
	lock  *os.File
}

// Listen takes the daemon lock and opens the Unix socket with owner-only
// permissions. It fails with ErrAlreadyRunning when a daemon already runs.
func Listen(paths Paths) (*Listener, error) {
	if err := paths.ensureDir(); err != nil {
		return nil, err
	}
	lock, err := lockFile(paths.Lock)
	if err != nil {
		return nil, err
	}
	// Holding the lock, any socket file left over is stale.
	if err := os.Remove(paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		lock.Close()
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", paths.Socket)
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("listen: %w", err)
	}
	// The directory is already owner-only; the socket is too, in case it moves.
	if err := os.Chmod(paths.Socket, 0o600); err != nil {
		ln.Close()
		lock.Close()
		return nil, fmt.Errorf("socket permissions: %w", err)
	}
	return &Listener{Listener: ln, paths: paths, lock: lock}, nil
}

// Close stops listening, removes the socket and releases the lock.
func (l *Listener) Close() error {
	err := l.Listener.Close() // removes the socket file too
	l.lock.Close()
	return err
}

// Serve answers requests on ln with h until ctx is done or ln fails.
func Serve(ctx context.Context, ln net.Listener, h *host.Host) error {
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		wg.Go(func() { serveConn(conn, h) })
	}
}

func serveConn(conn net.Conn, h *host.Host) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))
	var req Request
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	var resp Response
	if err != nil {
		resp.Error = fmt.Sprintf("bad request: %v", err)
	} else {
		resp = handle(req, h)
	}
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		slog.Debug("reply to client", "err", err)
	}
}

func handle(req Request, h *host.Host) Response {
	switch req.Op {
	case "events":
		for _, e := range req.Events {
			if err := h.Apply(e); err != nil {
				return Response{Error: err.Error()}
			}
		}
		return Response{}
	case "status":
		st := h.Status()
		return Response{Status: &st}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}
