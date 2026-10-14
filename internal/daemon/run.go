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

// showTickInterval replaces tickInterval while an animation is shown, so its
// frames go out on time: picture.MinDelay is the shortest delay.
const showTickInterval = 10 * time.Millisecond

// Run is the Host Daemon: it ticks h and serves clients on ln until ctx is
// done or h retires.
func Run(ctx context.Context, ln net.Listener, h *host.Host, opts ...Option) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-h.Retired():
			cancel()
		case <-ctx.Done():
		}
	}()
	ticking := make(chan struct{})
	go func() {
		defer close(ticking)
		for {
			h.Tick()
			wait := tickInterval
			if h.ShowsAnimation() {
				wait = showTickInterval
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
	err := Serve(ctx, ln, h, opts...)
	cancel()
	<-ticking
	return err
}
