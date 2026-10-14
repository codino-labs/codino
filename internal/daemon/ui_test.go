package daemon_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/host"
)

type fakeUI struct {
	url string
	err error
}

func (u fakeUI) Open() (string, error) { return u.url, u.err }

// serveUI starts a daemon with ui on a fresh socket.
func serveUI(t *testing.T, ui daemon.UI) daemon.Paths {
	t.Helper()
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	ln := listen(t, paths)
	ctx, cancel := context.WithCancel(context.Background())
	h := host.New(fixedClock{time.UnixMilli(1760432400000)}, noDevices)
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, ln, h, daemon.WithUI(ui)) }()
	t.Cleanup(func() {
		cancel()
		<-done
		h.Close()
		ln.Unlock()
	})
	return paths
}

func TestOpenUIReturnsTheURLOfTheDaemonsWebUI(t *testing.T) {
	paths := serveUI(t, fakeUI{url: "http://127.0.0.1:4242/open?ticket=t"})

	got, err := daemon.Client{Paths: paths}.OpenUI(context.Background())

	if err != nil || got != "http://127.0.0.1:4242/open?ticket=t" {
		t.Fatalf("OpenUI = %q, %v", got, err)
	}
}

func TestOpenUIOnARetiringDaemonSpawnsANewOne(t *testing.T) {
	paths := serveUI(t, fakeUI{err: host.ErrRetired})
	spawned := false
	client := daemon.Client{Paths: paths, Spawn: func() error { spawned = true; return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := client.OpenUI(ctx)

	if !spawned || err == nil {
		t.Fatalf("spawned = %v, err = %v: want a new daemon spawned", spawned, err)
	}
}

func TestADaemonWithoutAWebUISaysSo(t *testing.T) {
	paths := daemon.PathsIn(filepath.Join(t.TempDir(), "rt"))
	serve(t, listen(t, paths))

	_, err := daemon.Client{Paths: paths}.OpenUI(context.Background())

	if err == nil || errors.Is(err, daemon.ErrNotRunning) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}
