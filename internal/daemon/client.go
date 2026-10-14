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

// ErrNotRunning means no Host Daemon answered and none was spawned.
var ErrNotRunning = errors.New("the Host Daemon is not running")

// errUnavailable marks a failed call that another Host Daemon may answer:
// none listens, or the one that answered is exiting.
var errUnavailable = errors.New("no Host Daemon available")

// call sends req and returns the reply. When no Host Daemon is available and
// spawn is set, it spawns one (once) and retries until ctx is done.
func (c Client) call(ctx context.Context, req Request, spawn func() error) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	spawned := false
	for {
		resp, err := c.roundTrip(ctx, req)
		if !errors.Is(err, errUnavailable) {
			return resp, err
		}
		if spawn == nil {
			return Response{}, fmt.Errorf("%w: %v", ErrNotRunning, err)
		}
		if !spawned {
			if err := spawn(); err != nil {
				return Response{}, fmt.Errorf("spawn Host Daemon: %w", err)
			}
			spawned = true
		}
		select {
		case <-ctx.Done():
			return Response{}, fmt.Errorf("Host Daemon did not start: %w", err)
		case <-retry.C:
		}
	}
}

// roundTrip makes one request on a new connection.
func (c Client) roundTrip(ctx context.Context, req Request) (Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Paths.Socket)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer conn.Close()
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
