package daemon

import (
	"context"
	"net"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// tickInterval is how often the daemon lets the Host do its time-driven work
// (claiming a Device, heartbeats).
const tickInterval = 100 * time.Millisecond

// Run is the Host Daemon: it ticks h and serves clients on ln until ctx is
// done.
func Run(ctx context.Context, ln net.Listener, h *host.Host) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ticking := make(chan struct{})
	go func() {
		defer close(ticking)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			h.Tick()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	err := Serve(ctx, ln, h)
	cancel()
	<-ticking
	return err
}
