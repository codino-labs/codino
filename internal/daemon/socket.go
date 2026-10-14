package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/picture"
	"github.com/codino-labs/codino/internal/protocol"
)

// ErrAlreadyRunning means another Host Daemon owns the socket.
var ErrAlreadyRunning = errors.New("a Host Daemon is already running")

// requestTimeout bounds one client connection, so a stuck client can't hold
// a server goroutine.
const requestTimeout = 2 * time.Second

// Listener is the daemon's end of the local socket. Close stops listening and
// removes the socket; the daemon lock stays held until Unlock, so the daemon
// can release its Device before another daemon may start.
type Listener struct {
	net.Listener
	lock *os.File
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
	return &Listener{Listener: ln, lock: lock}, nil
}

// ListenWithin is Listen for a daemon that may be starting while the previous
// one is still exiting: it waits up to wait for the daemon lock.
func ListenWithin(paths Paths, wait time.Duration) (*Listener, error) {
	deadline := time.Now().Add(wait)
	for {
		ln, err := Listen(paths)
		if !errors.Is(err, ErrAlreadyRunning) || !time.Now().Before(deadline) {
			return ln, err
		}
		time.Sleep(lockRetryInterval)
	}
}

const lockRetryInterval = 50 * time.Millisecond

// Unlock closes the listener if still open and releases the daemon lock.
// Call it last.
func (l *Listener) Unlock() {
	l.Listener.Close()
	l.lock.Close()
}

// UI is the web UI a Host Daemon serves (webui.Launcher).
type UI interface {
	// Open starts the web UI unless it runs and returns the URL that opens
	// its page once. It fails with host.ErrRetired once the Host retired.
	Open() (string, error)
}

// Option configures Serve and Run.
type Option func(*options)

type options struct {
	ui UI
}

// WithUI serves OpUI with ui. Without it the daemon refuses OpUI.
func WithUI(ui UI) Option {
	return func(o *options) { o.ui = ui }
}

// Serve answers requests on ln with h until ctx is done or ln fails.
func Serve(ctx context.Context, ln net.Listener, h *host.Host, opts ...Option) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
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
		wg.Go(func() { serveConn(ctx, conn, h, o) })
	}
}

// maxRequest bounds one request line: a base64 picture and some room.
const maxRequest = MaxPicture*4/3 + 4096

func serveConn(ctx context.Context, conn net.Conn, h *host.Host, o options) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))
	var req Request
	line, err := bufio.NewReader(io.LimitReader(conn, maxRequest)).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	if err == nil && req.Op == OpShow {
		serveShow(ctx, conn, req, h)
		return
	}
	if err == nil && (req.Op == OpInstall || req.Op == OpUninstall) {
		// The Device writes its flash before it replies.
		conn.SetDeadline(time.Now().Add(InstallTimeout))
		reply(conn, serveInstall(ctx, req, h))
		return
	}
	var resp Response
	switch {
	case err != nil:
		resp.Error = fmt.Sprintf("bad request: %v", err)
	case req.Op == OpUI:
		resp = serveUI(o.ui)
	default:
		resp = handle(req, h)
	}
	reply(conn, resp)
}

// closedBy returns a channel closed once the peer closes (or half-closes)
// conn. What it reads meanwhile is discarded.
func closedBy(conn net.Conn) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		io.Copy(io.Discard, conn)
		close(closed)
	}()
	return closed
}

func reply(conn net.Conn, resp Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		slog.Debug("reply to client", "err", err)
	}
}

// serveShow shows the request's picture until the client half-closes the
// connection (or goes away), a newer show replaces it, or the daemon stops.
// Closing the connection tells the client the show ended.
func serveShow(ctx context.Context, conn net.Conn, req Request, h *host.Host) {
	p, err := picture.Decode(req.Picture)
	if err != nil {
		reply(conn, Response{Error: err.Error()})
		return
	}
	showing, err := h.Show(p)
	if errors.Is(err, host.ErrRetired) {
		reply(conn, Response{Retiring: true})
		return
	}
	if err != nil {
		reply(conn, Response{Error: err.Error()})
		return
	}
	defer showing.Stop()
	st := h.Status()
	reply(conn, Response{Status: &st})
	conn.SetDeadline(time.Time{})
	select {
	case <-closedBy(conn): // the client half-closed or went away
	case <-showing.Ended():
	case <-ctx.Done():
	}
}

// installReplyTimeout is how long the daemon waits for the Device's reply
// to an install, within the client's InstallTimeout.
const installReplyTimeout = 3 * time.Second

func serveInstall(ctx context.Context, req Request, h *host.Host) Response {
	ctx, cancel := context.WithTimeout(ctx, installReplyTimeout)
	defer cancel()
	var reply protocol.InstallReply
	var err error
	if req.Op == OpInstall {
		var install protocol.Install
		if install, err = protocol.NewInstall(req.Install); err != nil {
			return Response{Error: err.Error()}
		}
		reply, err = h.Install(ctx, install)
	} else {
		reply, err = h.Uninstall(ctx, req.Kind, req.Name)
	}
	if errors.Is(err, host.ErrNoDevice) {
		return Response{NoDevice: true}
	}
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{InstallReply: &reply}
}

func serveUI(ui UI) Response {
	if ui == nil {
		return Response{Error: "this Host Daemon has no web UI"}
	}
	url, err := ui.Open()
	if errors.Is(err, host.ErrRetired) {
		return Response{Retiring: true}
	}
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{URL: url}
}

func handle(req Request, h *host.Host) Response {
	switch req.Op {
	case OpEvents:
		for _, e := range req.Events {
			err := h.Apply(e)
			if errors.Is(err, host.ErrRetired) {
				return Response{Retiring: true}
			}
			if err != nil {
				return Response{Error: err.Error()}
			}
		}
		return Response{}
	case OpStatus:
		st := h.Status()
		return Response{Status: &st}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}
