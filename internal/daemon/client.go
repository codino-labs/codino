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
	_, err := c.call(ctx, Request{Op: "events", Events: events}, c.Spawn)
	return err
}

// Status asks a running Host Daemon for its sessions and Device. It never
// spawns one.
func (c Client) Status(ctx context.Context) (host.Status, error) {
	resp, err := c.call(ctx, Request{Op: "status"}, nil)
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

func (c Client) call(ctx context.Context, req Request, spawn func() error) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	conn, err := c.dial(ctx, spawn)
	if err != nil {
		return Response{}, err
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
	if resp.Error != "" {
		return Response{}, fmt.Errorf("Host Daemon: %s", resp.Error)
	}
	return resp, nil
}

// dial connects to the socket. When nothing listens and spawn is set, it
// spawns a daemon and retries until ctx is done.
func (c Client) dial(ctx context.Context, spawn func() error) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Paths.Socket)
	if err == nil {
		return conn, nil
	}
	if spawn == nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	if err := spawn(); err != nil {
		return nil, fmt.Errorf("spawn Host Daemon: %w", err)
	}
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Host Daemon did not start: %w", err)
		case <-retry.C:
		}
		if conn, err = d.DialContext(ctx, "unix", c.Paths.Socket); err == nil {
			return conn, nil
		}
	}
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
			return err
		}
		return cmd.Process.Release()
	}
}
