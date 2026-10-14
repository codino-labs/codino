// Package host is the Host core: it takes session events and clock ticks,
// keeps the Session Tracker, and drives the Device through the Device Link.
// It does no I/O of its own beyond what the injected Discoverer opens, so tests
// drive it with a fake clock and fake Devices.
package host

import (
	"log/slog"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/protocol"
)

// Clock is the injected time source.
type Clock interface {
	Now() time.Time
}

// Host is safe for concurrent use: the daemon applies events from many
// connections while a ticker calls Tick.
type Host struct {
	clock   Clock
	devices devicelink.Discoverer

	// tickMu keeps Ticks from overlapping, so only one claim runs at a time.
	tickMu sync.Mutex

	mu       sync.Mutex
	sessions *tracker
	device   *devicelink.Device
	// lastHeartbeat is when the Device last heard from the Host.
	lastHeartbeat time.Time
	// deviceProblem is why the last claim failed, at lastFailedClaim.
	deviceProblem   string
	lastFailedClaim time.Time
	closed          bool
}

// New returns a Host that finds Devices through devices.
func New(clock Clock, devices devicelink.Discoverer) *Host {
	return &Host{clock: clock, devices: devices, sessions: newTracker()}
}

// Apply records one session event and forwards the change to the Device.
func (h *Host) Apply(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, removed := h.sessions.apply(e, h.clock.Now())
	switch {
	case s == nil:
	case removed:
		h.send(protocol.NewSessionRemove(s.id))
	default:
		h.send(upsert(s))
	}
	return nil
}

// Tick does the time-driven work: claim a Device when none is claimed, and
// send a heartbeat every HeartbeatInterval. The daemon calls it often (more
// than once per HeartbeatInterval); tests call it after moving the fake clock.
func (h *Host) Tick() {
	h.tickMu.Lock()
	defer h.tickMu.Unlock()

	h.mu.Lock()
	h.dropGoneDevice()
	if h.device != nil {
		if now := h.clock.Now(); now.Sub(h.lastHeartbeat) >= HeartbeatInterval {
			h.send(protocol.NewHeartbeat(now.UnixMilli()))
			h.lastHeartbeat = now
		}
	}
	look := h.device == nil && !h.closed &&
		(h.lastFailedClaim.IsZero() || h.clock.Now().Sub(h.lastFailedClaim) >= ClaimRetryInterval)
	h.mu.Unlock()
	if look {
		h.claim()
	}
}

// ClaimRetryInterval is how long the Host waits after a failed claim before
// looking for a Device again, so a broken candidate isn't reopened in a loop.
const ClaimRetryInterval = 2 * time.Second

// claim looks for a Device and, once one answers the handshake, sends it every
// known session. The handshake can take up to devicelink.HandshakeTimeout, so
// it runs without h.mu and events keep flowing meanwhile.
func (h *Host) claim() {
	now := h.clock.Now()
	dev, err := devicelink.Claim(h.devices.Candidates(), now)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		if problem := err.Error(); problem != h.deviceProblem {
			slog.Info("no Device claimed", "problem", problem)
			h.deviceProblem = problem
		}
		h.lastFailedClaim = now
		return
	}
	if h.closed {
		dev.Close()
		return
	}
	slog.Info("Device claimed", "device", dev.Name, "firmware", dev.HelloReply.Firmware)
	h.device = dev
	h.deviceProblem = ""
	h.lastFailedClaim = time.Time{}
	h.lastHeartbeat = now // hello counts as one
	for _, s := range h.sessions.ordered() {
		h.send(upsert(s))
	}
}

// HeartbeatInterval is how often the Host tells a claimed Device it is alive.
const HeartbeatInterval = time.Second

// send writes to the claimed Device, if any. A failed write drops the Device;
// the next Tick claims one again. Callers hold h.mu.
func (h *Host) send(msg any) {
	if h.device == nil {
		return
	}
	if err := h.device.Send(msg); err != nil {
		h.dropGoneDevice()
	}
}

// dropGoneDevice forgets a Device that was unplugged. Callers hold h.mu.
func (h *Host) dropGoneDevice() {
	if h.device == nil {
		return
	}
	select {
	case <-h.device.Gone():
		h.device.Close()
		h.device = nil
	default:
	}
}

func upsert(s *session) protocol.SessionUpsert {
	return protocol.NewSessionUpsert(s.id, string(s.state), s.projectColor, s.stateEntered.UnixMilli(), s.tile)
}

// Close releases the Device; the Host claims no other afterwards.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.device != nil {
		h.device.Close()
		h.device = nil
	}
}
