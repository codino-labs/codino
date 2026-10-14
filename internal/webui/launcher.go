package webui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// Launcher runs the web UI for the Host Daemon: Open starts it on a free
// port of 127.0.0.1 (or reuses the one running) and holds the Host Daemon
// open; once the page has gone (Server.Expired) it stops, ends the preview
// and lets the Host Daemon retire.
type Launcher struct {
	Host        *host.Host
	Preferences Preferences
	// PreferencesFile is where the Preferences are, for the page to say.
	PreferencesFile string
	Clock           host.Clock

	mu      sync.Mutex
	running *running
	closed  bool
}

type running struct {
	server *Server
	http   *http.Server
	hold   *host.Hold
	done   chan struct{}
}

// sweepInterval is how often a running UI checks that its page is there.
const sweepInterval = time.Second

// Open returns a URL that opens the page, starting the web UI unless it
// runs. It fails with host.ErrRetired once the Host has retired or the
// Launcher closed.
func (l *Launcher) Open() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return "", host.ErrRetired // the Host Daemon is exiting
	}
	if l.running == nil {
		r, err := l.start()
		if err != nil {
			return "", err
		}
		l.running = r
	}
	return l.running.server.OpenURL(), nil
}

// start serves a new web UI. Callers hold l.mu.
func (l *Launcher) start() (*running, error) {
	hold, err := l.Host.Hold()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		hold.Release()
		return nil, fmt.Errorf("web UI: %w", err)
	}
	server := NewServer(Options{Host: l.Host, Preferences: l.Preferences, Clock: l.Clock, Addr: ln.Addr().String(),
		PreferencesFile: l.PreferencesFile})
	r := &running{server: server, hold: hold, done: make(chan struct{}),
		http: &http.Server{Handler: server, ReadHeaderTimeout: 5 * time.Second}}
	go func() {
		if err := r.http.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("web UI stopped", "err", err)
		}
	}()
	go l.sweepUntil(r.done)
	slog.Info("web UI open", "addr", ln.Addr().String())
	return r, nil
}

func (l *Launcher) sweepUntil(done <-chan struct{}) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.Sweep()
		case <-done:
			return
		}
	}
}

// Sweep stops the web UI once its page has gone. It holds l.mu meanwhile, so
// an Open waits for the old UI to end its preview before starting anew.
func (l *Launcher) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.running
	if r == nil || !r.server.Expired() {
		return
	}
	l.running = nil
	slog.Info("web UI closed: the page has gone")
	l.stop(r)
}

// stop shuts r down, ends the preview and releases the Host Daemon. Callers
// hold l.mu.
func (l *Launcher) stop(r *running) {
	close(r.done)
	r.server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.http.Shutdown(ctx); err != nil {
		r.http.Close()
	}
	l.Host.EndPreview()
	r.hold.Release()
}

// Close stops the web UI, if it runs, and opens no other.
func (l *Launcher) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if r := l.running; r != nil {
		l.running = nil
		l.stop(r)
	}
}
