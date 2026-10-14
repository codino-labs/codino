// Package host is the Host core: it takes session events and clock ticks,
// keeps the Session Tracker, and drives the Device through the Device Link.
// It does no I/O of its own beyond what the injected Discoverer opens, so tests
// drive it with a fake clock and fake Devices.
package host

import (
	"errors"
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

	// mu guards the fields below. Take it with h.mu.Lock and release it with
	// h.unlock, which closes the Devices dropped meanwhile.
	mu       sync.Mutex
	sessions *tracker
	device   *devicelink.Device
	// dropped are Devices to close once mu is released: closing a hung
	// Device can block for a second, which must not stall events.
	dropped []*devicelink.Device
	// lastHeartbeat is when the Device last heard from the Host.
	lastHeartbeat time.Time
	// deviceProblem is why the last claim failed, at lastFailedClaim.
	deviceProblem   string
	lastFailedClaim time.Time
	closed          bool

	exitGrace time.Duration
	// emptySince is when the last session ended (or the Host started without
	// any); zero while there are sessions.
	emptySince time.Time
	retired    chan struct{}

	transcripts TranscriptProbe
	// lastTranscriptCheck is when Tick last probed the transcripts.
	lastTranscriptCheck time.Time
}

// TranscriptProbe reads an Agent's transcript for what no hook reports.
type TranscriptProbe interface {
	// Interrupted reports whether the latest turn in the transcript at path
	// ends with the human interrupting it (Esc, Ctrl-C, a denied permission
	// or a rejected plan), and when.
	Interrupted(path string) (at time.Time, ok bool)
}

// WithTranscriptProbe lets the Host return an interrupted session to Idle:
// Claude Code fires no hook when the human interrupts a turn.
func WithTranscriptProbe(p TranscriptProbe) Option {
	return func(h *Host) { h.transcripts = p }
}

// TranscriptCheckInterval is how often the Host probes the transcripts of
// Working and Awaiting Input sessions.
const TranscriptCheckInterval = time.Second

// DefaultExitGrace is how long the Host Daemon stays up with no sessions.
const DefaultExitGrace = 5 * time.Minute

// Option configures a Host.
type Option func(*Host)

// WithExitGrace sets how long the Host waits with no sessions before it
// retires (default DefaultExitGrace).
func WithExitGrace(d time.Duration) Option {
	return func(h *Host) { h.exitGrace = d }
}

// New returns a Host that finds Devices through devices.
func New(clock Clock, devices devicelink.Discoverer, opts ...Option) *Host {
	h := &Host{
		clock:      clock,
		devices:    devices,
		sessions:   newTracker(),
		exitGrace:  DefaultExitGrace,
		emptySince: clock.Now(),
		retired:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Retired is closed once the Host has had no sessions for the exit grace: the
// Host Daemon should exit, and the next event spawns a new one.
func (h *Host) Retired() <-chan struct{} { return h.retired }

// hasRetired reports whether Retired is closed, without waiting.
func (h *Host) hasRetired() bool {
	select {
	case <-h.retired:
		return true
	default:
		return false
	}
}

// ErrRetired means the Host has retired and takes no more events: the
// client should deliver them to a new Host Daemon.
var ErrRetired = errors.New("the Host Daemon is exiting")

// Apply records one session event and forwards the change to the Device.
func (h *Host) Apply(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.unlock()
	if h.hasRetired() {
		return ErrRetired
	}
	now := h.clock.Now()
	s, removed := h.sessions.apply(e, now)
	switch empty := len(h.sessions.sessions) == 0; {
	case !empty:
		h.emptySince = time.Time{}
	case h.emptySince.IsZero():
		h.emptySince = now
	}
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
	if !h.hasRetired() && !h.emptySince.IsZero() && h.clock.Now().Sub(h.emptySince) >= h.exitGrace {
		close(h.retired)
	}
	h.dropGoneDevice()
	if h.device != nil {
		if now := h.clock.Now(); now.Sub(h.lastHeartbeat) >= HeartbeatInterval {
			h.send(protocol.NewHeartbeat(now.UnixMilli()))
			h.lastHeartbeat = now
		}
	}
	look := h.device == nil && !h.closed &&
		(h.lastFailedClaim.IsZero() || h.clock.Now().Sub(h.lastFailedClaim) >= ClaimRetryInterval)
	probe := h.transcriptsToProbe()
	h.unlock()
	if look {
		h.claim()
	}
	h.probeTranscripts(probe)
}

// transcriptsToProbe returns the transcript of every session a hook-less
// interrupt could leave stuck, once per TranscriptCheckInterval. Callers hold
// h.mu.
func (h *Host) transcriptsToProbe() map[string]string {
	now := h.clock.Now()
	if h.transcripts == nil || now.Sub(h.lastTranscriptCheck) < TranscriptCheckInterval {
		return nil
	}
	h.lastTranscriptCheck = now
	return h.sessions.transcriptsToProbe()
}

// probeTranscripts reads the transcripts without h.mu (it is file I/O) and
// returns interrupted sessions to Idle.
func (h *Host) probeTranscripts(probe map[string]string) {
	interrupted := map[string]time.Time{}
	for id, path := range probe {
		if at, ok := h.transcripts.Interrupted(path); ok {
			interrupted[id] = at
		}
	}
	if len(interrupted) == 0 {
		return
	}
	h.mu.Lock()
	defer h.unlock()
	for id, at := range interrupted {
		if s := h.sessions.interrupt(id, at, h.clock.Now()); s != nil {
			h.send(upsert(s))
		}
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
	defer h.unlock()
	if err != nil {
		if problem := err.Error(); problem != h.deviceProblem {
			slog.Info("no Device claimed", "problem", problem)
			h.deviceProblem = problem
		}
		h.lastFailedClaim = now
		return
	}
	if h.closed {
		h.dropped = append(h.dropped, dev)
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
		slog.Info("Device dropped", "err", err)
		h.drop()
	}
}

// dropGoneDevice forgets a Device that was unplugged. Callers hold h.mu.
func (h *Host) dropGoneDevice() {
	if h.device == nil {
		return
	}
	select {
	case <-h.device.Gone():
		h.drop()
	default:
	}
}

// drop forgets the claimed Device and schedules its Close for h.unlock.
// Callers hold h.mu.
func (h *Host) drop() {
	h.dropped = append(h.dropped, h.device)
	h.device = nil
}

// unlock releases h.mu, then closes the Devices dropped while it was held.
func (h *Host) unlock() {
	dropped := h.dropped
	h.dropped = nil
	h.mu.Unlock()
	for _, d := range dropped {
		d.Close()
	}
}

func upsert(s *session) protocol.SessionUpsert {
	return protocol.NewSessionUpsert(s.id, string(s.state), s.projectColor, s.stateEntered.UnixMilli(), s.tile)
}

// Close releases the Device; the Host claims no other afterwards.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.unlock()
	h.closed = true
	if h.device != nil {
		h.drop()
	}
}
