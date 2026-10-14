package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/protocol"
)

// Timeout bounds every client call, spawning included: a client never blocks
// its caller (an Agent's hook) for long.
const Timeout = 2 * time.Second

// Client talks to the Host Daemon over the local socket.
type Client struct {
	Paths Paths
	// Spawn starts a Host Daemon in the background. Nil means: never spawn.
	Spawn func() error
}

// Deliver sends session events to the Host Daemon, spawning it when it isn't
// running. It is the one entry point for every Agent's events: `codino event`
// and the Claude Code Adapter both end here.
func (c Client) Deliver(ctx context.Context, events ...host.Event) error {
	for _, e := range events {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	_, err := c.call(ctx, Request{Op: OpEvents, Events: events}, c.Spawn)
	return err
}

// Status asks a running Host Daemon for its sessions and Device. It never
// spawns one.
func (c Client) Status(ctx context.Context) (host.Status, error) {
	resp, err := c.call(ctx, Request{Op: OpStatus}, nil)
	if err != nil {
		return host.Status{}, err
	}
	if resp.Status == nil {
		return host.Status{}, errors.New("status: empty reply")
	}
	return *resp.Status, nil
}

// Wake is Status from a Host Daemon it spawns when none is running, for a
// command that needs the Device even when no Agent Session is.
func (c Client) Wake(ctx context.Context) (host.Status, error) {
	resp, err := c.call(ctx, Request{Op: OpStatus}, c.Spawn)
	if err != nil {
		return host.Status{}, err
	}
	if resp.Status == nil {
		return host.Status{}, errors.New("status: empty reply")
	}
	return *resp.Status, nil
}

// ErrNoDevice means the Host Daemon has no Device claimed to install on.
var ErrNoDevice = errors.New("no Device connected")

// InstallTimeout bounds an install or uninstall: the Device writes its
// flash before it replies.
const InstallTimeout = 5 * time.Second

// Install installs a Theme or Sprite Pack file on the Device, spawning the
// Host Daemon when it isn't running, and returns the Device's reply. A file
// that can't be a Theme or Sprite Pack fails before any daemon is called.
func (c Client) Install(ctx context.Context, file []byte) (protocol.InstallReply, error) {
	if _, err := protocol.NewInstall(file); err != nil {
		return protocol.InstallReply{}, err
	}
	return c.install(ctx, Request{Op: OpInstall, Install: file})
}

// Uninstall removes an installed Theme or Sprite Pack from the Device.
func (c Client) Uninstall(ctx context.Context, kind protocol.Kind, name string) (protocol.InstallReply, error) {
	return c.install(ctx, Request{Op: OpUninstall, Kind: kind, Name: name})
}

func (c Client) install(ctx context.Context, req Request) (protocol.InstallReply, error) {
	ctx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()
	resp, err := c.call(ctx, req, c.Spawn)
	if err != nil {
		return protocol.InstallReply{}, err
	}
	if resp.NoDevice {
		return protocol.InstallReply{}, ErrNoDevice
	}
	if resp.InstallReply == nil {
		return protocol.InstallReply{}, errors.New("install: empty reply")
	}
	return *resp.InstallReply, nil
}

// ErrNotRunning means no Host Daemon answered and none was spawned.
var ErrNotRunning = errors.New("the Host Daemon is not running")

// errUnavailable marks a failed call that another Host Daemon may answer:
// none listens, or the one that answered is exiting.
var errUnavailable = errors.New("no Host Daemon available")

// Show shows an encoded picture (PNG, JPEG or GIF) on the Device in Raw
// Mode, spawning the Host Daemon when it isn't running. The show lasts until
// Stop, or until the daemon ends it (Ended). The reply's Status tells whether
// a Device is claimed.
func (c Client) Show(ctx context.Context, picture []byte) (*Show, error) {
	if len(picture) > MaxPicture {
		return nil, fmt.Errorf("picture of %d bytes: the most is %d", len(picture), MaxPicture)
	}
	conn, resp, err := c.open(ctx, Request{Op: OpShow, Picture: picture}, c.Spawn)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	s := &Show{conn: conn}
	if resp.Status != nil {
		s.Status = *resp.Status
	}
	s.ended = closedBy(conn)
	return s, nil
}

// Show is a picture the Host Daemon shows for this client.
type Show struct {
	// Status is the daemon's status when the show started.
	Status host.Status
	conn   net.Conn
	ended  <-chan struct{}
}

// Ended is closed once the daemon ended the show: a newer show replaced it,
// or the daemon exited.
func (s *Show) Ended() <-chan struct{} { return s.ended }

// Stop ends the show and waits, up to Timeout, until the daemon has returned
// the Device to its own rendering.
func (s *Show) Stop() {
	defer s.conn.Close()
	if c, ok := s.conn.(interface{ CloseWrite() error }); ok && c.CloseWrite() == nil {
		select {
		case <-s.ended:
		case <-time.After(Timeout):
		}
	}
}

// call sends req and returns the reply. When no Host Daemon is available and
// spawn is set, it spawns one (once) and retries until ctx is done.
func (c Client) call(ctx context.Context, req Request, spawn func() error) (Response, error) {
	conn, resp, err := c.open(ctx, req, spawn)
	if err != nil {
		return Response{}, err
	}
	conn.Close()
	return resp, nil
}

// open is call that leaves the connection open for the caller to close. A
// ctx without a deadline gets Timeout.
func (c Client) open(ctx context.Context, req Request, spawn func() error) (net.Conn, Response, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, Timeout)
		defer cancel()
	}
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	spawned := false
	for {
		conn, resp, err := c.roundTrip(ctx, req)
		if !errors.Is(err, errUnavailable) {
			return conn, resp, err
		}
		if spawn == nil {
			return nil, Response{}, fmt.Errorf("%w: %w", ErrNotRunning, err)
		}
		if !spawned {
			if err := spawn(); err != nil {
				return nil, Response{}, fmt.Errorf("spawn Host Daemon: %w", err)
			}
			spawned = true
		}
		select {
		case <-ctx.Done():
			return nil, Response{}, fmt.Errorf("Host Daemon did not start: %w", err)
		case <-retry.C:
		}
	}
}

// roundTrip makes one request on a new connection, which it returns open
// when the request succeeded. The reply is the daemon's last write unless
// the request keeps the connection open (OpShow), so nothing is lost to the
// buffered reader.
func (c Client) roundTrip(ctx context.Context, req Request) (net.Conn, Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Paths.Socket)
	if err != nil {
		return nil, Response{}, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	resp, err := exchange(ctx, conn, req)
	if err != nil {
		conn.Close()
		return nil, Response{}, err
	}
	return conn, resp, nil
}

func exchange(ctx context.Context, conn net.Conn, req Request) (Response, error) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("send to Host Daemon: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Response{}, fmt.Errorf("reply from Host Daemon: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("reply from Host Daemon: %w", err)
	}
	if resp.Retiring {
		return Response{}, fmt.Errorf("%w: the Host Daemon is exiting", errUnavailable)
	}
	if resp.Error != "" {
		return Response{}, fmt.Errorf("Host Daemon: %s", resp.Error)
	}
	return resp, nil
}

// SpawnCommand returns a Spawn that runs `executable args...` detached, with
// stdin closed and its output appended to the daemon log. The child inherits
// the environment, so settings such as the Virtual Device to target follow it.
func SpawnCommand(paths Paths, executable string, args ...string) func() error {
	return func() error {
		if err := paths.ensureDir(); err != nil {
			return err
		}
		log, err := os.OpenFile(paths.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("daemon log: %w", err)
		}
		defer log.Close()
		cmd := exec.Command(executable, args...)
		cmd.Stdout = log
		cmd.Stderr = log
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", executable, err)
		}
		return cmd.Process.Release()
	}
}
