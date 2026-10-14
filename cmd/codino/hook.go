package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/codino-labs/codino/internal/claudecode"
	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/liveness"
)

// maxHookInput bounds what `codino hook` reads: tool results can be large,
// but the Adapter needs only a few fields.
const maxHookInput = 16 << 20

// hook is `codino hook`, run by every Claude Code hook in the plugin: it
// reads the hook JSON on stdin and forwards it to the Host Daemon, spawning
// it when needed. It never fails the hook: a Codino failure must not block
// or fail an Agent Session, so errors only go to stderr.
func hook(paths daemon.Paths, stdin io.Reader, stderr io.Writer, receivedAt time.Time) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "%s hook: %v\n", name, r)
		}
	}()
	if err := forwardHook(paths, stdin, receivedAt); err != nil {
		fmt.Fprintf(stderr, "%s hook: %v\n", name, err)
	}
}

func forwardHook(paths daemon.Paths, stdin io.Reader, receivedAt time.Time) error {
	input, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	// The start time is read here, while the Agent is certainly alive (it
	// runs this hook), not later in the Host Daemon, when its pid may
	// already name another process.
	pid, guessed := claudePID()
	var started uint64
	if pid != 0 {
		started = liveness.Started(pid)
	}
	events, err := claudecode.Events(claudecode.Hook{Input: input, ReceivedAt: receivedAt, PID: pid, PIDStarted: started, PIDGuessed: guessed})
	if err != nil || len(events) == 0 {
		return err
	}
	client := daemon.Client{Paths: paths}
	// A session ending needs no Host Daemon to be started for it.
	for _, e := range events {
		if !e.End {
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("find own binary: %w", err)
			}
			client.Spawn = daemon.SpawnCommand(paths, self, "daemon")
			break
		}
	}
	err = client.Deliver(context.Background(), events...)
	if client.Spawn == nil && errors.Is(err, daemon.ErrNotRunning) {
		return nil // nothing to end
	}
	return err
}

// claudePID is the Claude Code process that runs the hook: CLAUDE_PID, set
// by Claude Code though undocumented, or else, guessed, the hook's
// grandparent (hook → sh → claude), where the OS shows it (Linux, macOS; not
// Windows, where Git Bash sits in between). A guess may name another process
// (a wrapper, a different shell chain), so it is only shown: the session is
// reaped only by CLAUDE_PID, and otherwise returned to Idle when stale.
func claudePID() (pid int, guessed bool) {
	if pid, err := strconv.Atoi(os.Getenv("CLAUDE_PID")); err == nil && pid > 0 {
		return pid, false
	}
	pid = liveness.Parent(os.Getppid())
	return pid, pid != 0
}
