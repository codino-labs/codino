package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

var noDevices = devicelink.NoDevices

// serve starts a daemon on ln in the background, stopped at test cleanup.
func serve(t *testing.T, ln *daemon.Listener) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := host.New(fixedClock{time.UnixMilli(1760432400000)}, noDevices)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, ln, h) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		h.Close()
		ln.Unlock()
	})
}

func listen(t *testing.T, paths daemon.Paths) *daemon.Listener {
	t.Helper()
	ln, err := daemon.Listen(paths)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return ln
}

func TestTheSocketIsOwnerOnly(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	ln := listen(t, paths)
	defer ln.Unlock()

	for path, want := range map[string]os.FileMode{paths.Dir: 0o700, paths.Socket: 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %v, want %v", path, got, want)
		}
	}
}

func TestARuntimeDirectoryOthersCanReadIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rt")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if ln, err := daemon.Listen(daemon.PathsIn(dir)); err == nil {
		ln.Unlock()
		t.Fatal("Listen accepted a world-readable runtime directory")
	}
}

func TestOnlyOneDaemonListens(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	ln := listen(t, paths)
	defer ln.Unlock()

	if second, err := daemon.Listen(paths); !errors.Is(err, daemon.ErrAlreadyRunning) {
		if second != nil {
			second.Unlock()
		}
		t.Fatalf("second Listen: err = %v, want ErrAlreadyRunning", err)
	}
}

func TestDeliveredEventsShowInStatus(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	serve(t, listen(t, paths))
	client := daemon.Client{Paths: paths}
	ctx := context.Background()

	err := client.Deliver(ctx,
		host.Event{SessionID: "a", State: host.Working, Project: "/src/app"},
		host.Event{SessionID: "b", State: host.AwaitingInput},
	)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	st, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if st.AggregateState != host.AwaitingInput || len(st.Sessions) != 2 || st.Sessions[0].Project != "/src/app" {
		t.Errorf("Status = %+v, want sessions a (/src/app) and b, Awaiting Input", st)
	}
}

func TestDeliverRejectsAnInvalidEventWithoutCallingTheDaemon(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	spawned := false
	client := daemon.Client{Paths: paths, Spawn: func() error { spawned = true; return nil }}

	if err := client.Deliver(context.Background(), host.Event{SessionID: "a", State: "busy"}); err == nil {
		t.Fatal("Deliver accepted an unknown Session State")
	}
	if spawned {
		t.Error("Deliver spawned a daemon for an invalid event")
	}
}

func TestDeliverSpawnsTheDaemonWhenNoneRuns(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	spawns := 0
	client := daemon.Client{Paths: paths, Spawn: func() error {
		spawns++
		serve(t, listen(t, paths))
		return nil
	}}
	ctx := context.Background()

	for range 2 {
		if err := client.Deliver(ctx, host.Event{SessionID: "a", State: host.Working}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}

	if spawns != 1 {
		t.Errorf("spawned %d daemons, want 1", spawns)
	}
}

func TestStatusNeverSpawnsADaemon(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))

	_, err := daemon.Client{Paths: paths, Spawn: func() error {
		t.Error("Status spawned a daemon")
		return nil
	}}.Status(context.Background())

	if !errors.Is(err, daemon.ErrNotRunning) {
		t.Errorf("Status: err = %v, want ErrNotRunning", err)
	}
}

func TestTheLockOutlivesServingUntilUnlock(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	ln := listen(t, paths)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := daemon.Serve(ctx, ln, host.New(fixedClock{}, noDevices)); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if _, err := daemon.Listen(paths); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("Listen after Serve returned: err = %v, want ErrAlreadyRunning while the Device may still be released", err)
	}
	ln.Unlock()
	next := listen(t, paths)
	next.Unlock()
}

func TestRunReturnsOnceTheHostRetires(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	ln := listen(t, paths)
	defer ln.Unlock()
	h := host.New(fixedClock{time.UnixMilli(1760432400000)}, noDevices, host.WithExitGrace(0))
	defer h.Close()

	if err := daemon.Run(context.Background(), ln, h); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := (daemon.Client{Paths: paths}).Status(context.Background()); !errors.Is(err, daemon.ErrNotRunning) {
		t.Errorf("Status after Run returned: err = %v, want ErrNotRunning", err)
	}
}

func TestDeliverToARetiringDaemonSpawnsANewOne(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	old := listen(t, paths)
	retiring := host.New(fixedClock{time.UnixMilli(1760432400000)}, noDevices, host.WithExitGrace(0))
	retiring.Tick()
	ctx, stopOld := context.WithCancel(context.Background())
	oldDone := make(chan error, 1)
	go func() { oldDone <- daemon.Serve(ctx, old, retiring) }()
	spawns := 0
	client := daemon.Client{Paths: paths, Spawn: func() error {
		spawns++
		// The old daemon finishes exiting; the new one takes over.
		stopOld()
		<-oldDone
		retiring.Close()
		old.Unlock()
		serve(t, listen(t, paths))
		return nil
	}}

	if err := client.Deliver(context.Background(), host.Event{SessionID: "a", State: host.Working}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if spawns != 1 || len(st.Sessions) != 1 {
		t.Errorf("spawned %d daemons, new daemon has %d sessions; want 1 and 1", spawns, len(st.Sessions))
	}
}

func TestListenWithinWaitsForAnExitingDaemonToReleaseTheLock(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	exiting := listen(t, paths)
	got := make(chan error, 1)
	go func() {
		ln, err := daemon.ListenWithin(paths, time.Minute)
		if err == nil {
			ln.Unlock()
		}
		got <- err
	}()

	exiting.Unlock()

	if err := <-got; err != nil {
		t.Errorf("ListenWithin: %v", err)
	}
}

func TestListenWithinGivesUpOnARunningDaemon(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	running := listen(t, paths)
	defer running.Unlock()

	if _, err := daemon.ListenWithin(paths, 0); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Errorf("ListenWithin: err = %v, want ErrAlreadyRunning", err)
	}
}
