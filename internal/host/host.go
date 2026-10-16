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
	"github.com/codino-labs/codino/internal/preferences"
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
	// orientation is the rotation the claimed Device's IMU senses (its
	// hello_reply, then its orientation messages): 0 without one.
	orientation int
	// dropped are Devices to close once mu is released: closing a hung
	// Device can block for a second, which must not stall events.
	dropped []*devicelink.Device
	// lastHeartbeat is when the Device last heard from the Host.
	lastHeartbeat time.Time
	// deviceProblem is why the last claim failed, at lastFailedClaim.
	deviceProblem string
	// deviceUpdateURL is the web flasher's, when deviceProblem is a
	// firmware too old for this Host.
	deviceUpdateURL string
	lastFailedClaim time.Time
	closed          bool

	// prefs are the Preferences in force: the config file's last valid
	// ones. prefsProblem is why the file is ignored, if it is.
	prefsSource  PreferencesSource
	prefs        preferences.Preferences
	prefsProblem string
	// lastPrefsPoll is when Tick last read the config file.
	lastPrefsPoll time.Time

	roots ProjectRootProbe

	exitGrace time.Duration
	// emptySince is when the last session ended (or the Host started without
	// any); zero while there are sessions.
	emptySince time.Time
	retired    chan struct{}

	transcripts TranscriptProbe
	liveness    LivenessProbe
	// lastProbe is when Tick last probed the Agents.
	lastProbe time.Time

	// showing is the picture shown in Raw Mode, if any; shownFrame is the
	// index of its frame the Device has, -1 for none.
	showing    *Showing
	shownFrame int

	// preview is the web UI's preview-state override, if any.
	preview *previewing
	// holds counts the Holds keeping the Host from retiring.
	holds int
	// watchers see what the Device is sent (Watch).
	watchers map[*Watcher]bool
	// copies keeps copies of the installed Themes and Sprite Packs, which
	// themeCopies holds in memory.
	copies      ThemeCopies
	themeCopies []protocol.Install

	// installMu keeps installs one at a time: each waits for its reply.
	installMu sync.Mutex
	// onDevice is what the claimed Device has installed.
	onDevice installedOn
}

// TranscriptProbe reads an Agent's transcript for what no hook reports.
type TranscriptProbe interface {
	// Read reports what the transcript at path shows; the zero Transcript
	// when it can't be read.
	Read(path string) Transcript
}

// Transcript is what a TranscriptProbe read in an Agent's transcript.
type Transcript struct {
	// Modified is when the transcript was last written; zero when unknown.
	Modified time.Time
	// Interrupted is when the human interrupted the latest turn (Esc,
	// Ctrl-C, a denied permission or a rejected plan); zero when the latest
	// turn wasn't interrupted.
	Interrupted time.Time
	// ToolStarted is when the latest tool still in progress started; zero
	// when there is none or it can't tell. A tool starting after a
	// permission prompt means the human approved it, which no hook reports
	// until the tool finishes.
	ToolStarted time.Time
}

// WithTranscriptProbe lets the Host return a stuck session to Idle: Claude
// Code fires no hook when the human interrupts a turn, and a Working session
// whose transcript stays unchanged for StaleAfter falls back to Idle. It also
// returns a session Awaiting Input to Working once the tool the human
// approved starts running.
func WithTranscriptProbe(p TranscriptProbe) Option {
	return func(h *Host) { h.transcripts = p }
}

// LivenessProbe tells whether an Agent has exited, by the liveness handle its
// events carry: a pid and, when known, when that pid's process started.
type LivenessProbe interface {
	// Gone reports whether the Agent with this pid, started at started (an
	// opaque value from the same platform; 0 when unknown), has exited: no
	// such pid, or the pid now names one started at another time (reused).
	// False when it can't tell.
	Gone(pid int, started uint64) bool
}

// WithLivenessProbe lets the Host reap a session whose Agent exited without
// ending it (kill -9 or a crash fires no SessionEnd).
func WithLivenessProbe(p LivenessProbe) Option {
	return func(h *Host) { h.liveness = p }
}

// StaleAfter is how long a Working session with a transcript may go without
// a hook or a transcript write before it falls back to Idle: the backstop for
// whatever else leaves a session stuck in Working. It is longer than the
// Bash tool's 10-minute maximum timeout, during which a turn writes nothing.
const StaleAfter = 15 * time.Minute

// ProbeInterval is how often the Host probes the Agents for what no hook
// reports: the transcripts of Working and Awaiting Input sessions, and
// whether each session's Agent is still alive.
const ProbeInterval = time.Second

// PreferencesSource reads the Preferences: the config file, canonical for
// them. An error means the file is invalid; the Host keeps the Preferences
// it has.
type PreferencesSource interface {
	Load() (preferences.Preferences, error)
}

// WithPreferences makes the Host follow the Preferences of src (default: the
// defaults): it reads them when created and every PreferencesPollInterval,
// and pushes them to the Device on claiming it and when they change.
func WithPreferences(src PreferencesSource) Option {
	return func(h *Host) { h.prefsSource = src }
}

// PreferencesPollInterval is how often the Host reads the config file, so an
// edit reaches the Device about a second later.
const PreferencesPollInterval = time.Second

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
		prefs:      preferences.Defaults(),
		roots:      sameDir{},
		emptySince: clock.Now(),
		retired:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.prefsSource != nil {
		h.reloadPreferences()
	}
	if h.copies != nil {
		h.themeCopies = h.copies.Load()
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
	var root string
	if e.Project != "" {
		root = h.roots.Root(e.Project) // file I/O: outside h.mu
	}
	h.mu.Lock()
	defer h.unlock()
	if h.hasRetired() {
		return ErrRetired
	}
	now := h.clock.Now()
	c := h.sessions.apply(e, root, now)
	h.updateExitGrace(now)
	switch {
	case c.removed:
		h.send(protocol.NewSessionRemove(c.s.id))
	case c.upserted:
		h.send(upsert(c.s))
	}
	if c.acknowledged {
		h.send(acknowledge(c.s))
	}
	for _, s := range c.moved {
		h.send(upsert(s))
	}
	return nil
}

// updateExitGrace starts the exit grace when the last session is gone and
// stops it while there are any, a picture is shown or a Hold is held.
// Callers hold h.mu.
func (h *Host) updateExitGrace(now time.Time) {
	switch empty := len(h.sessions.sessions) == 0 && h.showing == nil && h.holds == 0; {
	case !empty:
		h.emptySince = time.Time{}
	case h.emptySince.IsZero():
		h.emptySince = now
	}
}

// Tick does the time-driven work: claim a Device when none is claimed, decay
// Done sessions to Idle, and send a heartbeat every HeartbeatInterval. The daemon calls it often (more
// than once per HeartbeatInterval); tests call it after moving the fake clock.
func (h *Host) Tick() {
	h.tickMu.Lock()
	defer h.tickMu.Unlock()

	h.mu.Lock()
	if !h.hasRetired() && !h.emptySince.IsZero() && h.clock.Now().Sub(h.emptySince) >= h.exitGrace {
		close(h.retired)
	}
	h.dropGoneDevice()
	for _, s := range h.sessions.decayDone(h.clock.Now(), h.prefs.DoneDecay) {
		h.send(upsert(s))
	}
	if h.listening() {
		if now := h.clock.Now(); now.Sub(h.lastHeartbeat) >= HeartbeatInterval {
			silent := now.Sub(h.lastHeartbeat) >= h.prefs.DisconnectedTimeout
			h.send(protocol.NewHeartbeat(now.UnixMilli()))
			h.lastHeartbeat = now
			h.refreshShow(silent)
			if silent {
				// The Device took the silence for Disconnected, which ended
				// the preview.
				h.sendPreview()
			}
		}
		h.sendShowFrame()
	}
	look := h.device == nil && !h.closed &&
		(h.lastFailedClaim.IsZero() || h.clock.Now().Sub(h.lastFailedClaim) >= ClaimRetryInterval)
	targets := h.toProbe()
	reload := h.prefsSource != nil && h.clock.Now().Sub(h.lastPrefsPoll) >= PreferencesPollInterval
	h.unlock()
	if reload {
		h.reloadPreferences()
	}
	if look {
		h.claim()
	}
	h.probe(targets)
}

// probeTargets is what one probe pass reads, by session id.
type probeTargets struct {
	transcripts map[string]string
	liveness    map[string]livenessHandle
}

// toProbe returns, once per ProbeInterval, the transcript of every session a
// hook-less interrupt could leave stuck and the liveness handle of every
// session that has one. Callers hold h.mu.
func (h *Host) toProbe() probeTargets {
	now := h.clock.Now()
	if now.Sub(h.lastProbe) < ProbeInterval {
		return probeTargets{}
	}
	h.lastProbe = now
	var p probeTargets
	if h.transcripts != nil {
		p.transcripts = h.sessions.transcriptsToProbe()
	}
	if h.liveness != nil {
		p.liveness = h.sessions.livenessToProbe()
	}
	return p
}

// probe runs the probes without h.mu (they are file and OS I/O), then reaps
// sessions whose Agent is gone, returns interrupted and stale ones to Idle,
// and approved ones to Working.
func (h *Host) probe(p probeTargets) {
	read := map[string]Transcript{}
	for id, path := range p.transcripts {
		read[id] = h.transcripts.Read(path)
	}
	gone := map[string]livenessHandle{}
	for id, handle := range p.liveness {
		if h.liveness.Gone(handle.pid, handle.started) {
			gone[id] = handle
		}
	}
	if len(read) == 0 && len(gone) == 0 {
		return
	}
	h.mu.Lock()
	defer h.unlock()
	now := h.clock.Now()
	for id, handle := range gone {
		if s, moved := h.sessions.reap(id, handle, now); s != nil {
			slog.Info("session reaped: its Agent is gone", "session", s.id, "pid", handle.pid)
			h.send(protocol.NewSessionRemove(s.id))
			for _, m := range moved {
				h.send(upsert(m))
			}
		}
	}
	h.updateExitGrace(now)
	for id, tr := range read {
		s := h.sessions.interrupt(id, tr.Interrupted, now)
		if s == nil {
			s = h.sessions.approvedToWorking(id, tr.ToolStarted, now)
		}
		if s == nil {
			if s = h.sessions.staleToIdle(id, tr.Modified, tr.ToolStarted, now); s != nil {
				slog.Info("stale Working session back to Idle", "session", s.id, "stale_after", StaleAfter)
			}
		}
		if s != nil {
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
	if err == nil {
		h.prepareShow(dev)
	}
	h.mu.Lock()
	defer h.unlock()
	if err != nil {
		if problem := err.Error(); problem != h.deviceProblem {
			slog.Info("no Device claimed", "problem", problem)
			h.deviceProblem = problem
		}
		h.deviceUpdateURL = ""
		var mismatch *devicelink.VersionMismatchError
		if errors.As(err, &mismatch) && mismatch.FirmwareOutdated() {
			h.deviceUpdateURL = devicelink.FlasherURL
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
	h.orientation = orientationOf(dev)
	h.onDevice = installedOnDevice(dev)
	h.deviceProblem, h.deviceUpdateURL = "", ""
	h.lastFailedClaim = time.Time{}
	h.lastHeartbeat = now // hello counts as one
	msgs, frame := h.greeting()
	for _, msg := range msgs {
		h.sendDevice(msg)
	}
	h.shownFrame = frame
	for w := range h.watchers {
		h.startWatcher(w, now)
	}
	go h.listen(dev)
}

// greeting is what a Device claimed now hears after hello: the Preferences,
// every session (with its Acknowledge, for one Awaiting Input), the show's
// Raw Mode and its current frame, and the preview. frame is the index of the
// show's frame in it, -1 for none. Callers hold h.mu.
func (h *Host) greeting() (msgs []any, frame int) {
	msgs = append(msgs, preferencesPush(h.prefs))
	for _, s := range h.sessions.ordered() {
		msgs = append(msgs, upsert(s))
		if s.state == AwaitingInput && !s.acknowledged.IsZero() {
			msgs = append(msgs, acknowledge(s))
		}
	}
	frame = -1
	if i, raw, ok := h.showFrame(); ok {
		frame = i
		msgs = append(msgs, protocol.NewRawModeEnter(), raw)
	}
	if h.preview != nil {
		msgs = append(msgs, h.previewEnter())
	}
	return msgs, frame
}

// listen applies what the human does on the Device until it is gone.
func (h *Host) listen(dev *devicelink.Device) {
	for {
		select {
		case <-dev.Acknowledged():
			h.acknowledgeAll()
		case orientation := <-dev.Orientations():
			h.orient(dev, orientation)
		case <-dev.Gone():
			return
		}
	}
}

// acknowledgeAll applies an Acknowledge on the Device: every Done session is
// cleared and every Escalation restarts. The Device restarted Escalation
// itself, so only the Session State changes go back to it. The Acknowledge
// is the newest event of each session it cleared, so a slow hook stamped
// before it, arriving later, is dropped rather than setting it back to Done.
// A waiting session's events stay as they were: an interrupt shortly before
// the Acknowledge must still return it to Idle.
func (h *Host) acknowledgeAll() {
	h.mu.Lock()
	defer h.unlock()
	now := h.clock.Now()
	for _, s := range h.sessions.ordered() {
		if s.acknowledge(now) {
			s.seen(now)
			h.send(upsert(s))
		}
	}
}

// HeartbeatInterval is how often the Host tells a claimed Device it is alive.
const HeartbeatInterval = time.Second

// send writes to the claimed Device, if any, and to every Watcher. Callers
// hold h.mu.
func (h *Host) send(msg any) {
	if len(h.watchers) > 0 {
		line, err := protocol.Encode(msg)
		if err != nil {
			slog.Warn("encode for the Watchers", "err", err)
		} else {
			h.mirrorLine(line)
		}
	}
	h.sendDevice(msg)
}

// sendDevice writes to the claimed Device, if any. A failed write drops the
// Device; the next Tick claims one again. Callers hold h.mu.
func (h *Host) sendDevice(msg any) {
	if h.device == nil {
		return
	}
	if err := h.device.Send(msg); err != nil {
		slog.Info("Device dropped", "err", err)
		h.drop()
	}
}

// listening reports whether anything hears what the Host sends: a claimed
// Device or a Watcher. Callers hold h.mu.
func (h *Host) listening() bool { return h.device != nil || len(h.watchers) > 0 }

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
	h.orientation = 0
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

// acknowledge tells the Device when the human last Acknowledged a
// session Awaiting Input, so it restarts Escalation from then.
func acknowledge(s *session) protocol.Acknowledge {
	return protocol.NewAcknowledge(s.id, s.acknowledged.UnixMilli())
}

func upsert(s *session) protocol.SessionUpsert {
	return protocol.NewSessionUpsert(s.id, string(s.state), s.projectColor, s.stateEntered.UnixMilli(), s.tile)
}

// Close says goodbye to the Device and releases it; the Host claims no other
// afterwards. The Host Daemon closes the Host when it exits cleanly (its exit
// grace over, or stopped), so the Device goes dark rather than show
// Disconnected, which stays for a Host gone silent without a word.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.unlock()
	if h.closed {
		return
	}
	h.closed = true
	if h.showing != nil {
		h.showing.end()
		h.showing = nil
	}
	h.send(protocol.NewGoodbye())
	if h.device != nil {
		h.drop()
	}
	for w := range h.watchers {
		h.dropWatcher(w)
	}
}
