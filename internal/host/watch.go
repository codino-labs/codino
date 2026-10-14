package host

import (
	"bytes"
	"log/slog"
	"slices"
	"time"

	"github.com/codino-labs/codino/internal/protocol"
)

// Watcher sees what the Device sees: the web UI's live preview runs the
// Device core on the same protocol stream. It starts with a Device of its
// own (the claimed Device's Hardware Profile, or PreviewProfile without
// one), the Themes and Sprite Packs the Host has copies of, a hello and what
// a Device claimed now would hear; then every line the Device is sent, and
// the heartbeats even without a Device. A Device claimed later starts it
// over.
type Watcher struct {
	host     *Host
	mirrored chan Mirrored
}

// Mirrored is one thing a Watcher sees: a Device to start afresh, or one
// protocol line for it.
type Mirrored struct {
	// Device is the Hardware Profile of the Device to start; nil for a line.
	Device *protocol.HardwareProfile
	// Line is one protocol message without its newline: JSON, or a Raw Mode
	// frame.
	Line []byte
}

// PreviewProfile is the Hardware Profile a Watcher's Device has while no
// Device is claimed: the reference 8x8 matrix, uncapped.
var PreviewProfile = protocol.HardwareProfile{Width: 8, Height: 8, ColorOrder: "GRB", BrightnessCap: 255, CurrentCapMA: 500}

// watcherBacklog is how many Mirrored a Watcher may fall behind by. One that
// falls further is closed: it starts over by watching again.
const watcherBacklog = 1024

// Watch returns a Watcher of the Device's protocol stream, until Close.
func (h *Host) Watch() *Watcher {
	w := &Watcher{host: h, mirrored: make(chan Mirrored, watcherBacklog)}
	h.mu.Lock()
	defer h.unlock()
	if h.closed {
		close(w.mirrored)
		return w
	}
	if h.watchers == nil {
		h.watchers = map[*Watcher]bool{}
	}
	h.watchers[w] = true
	h.startWatcher(w, h.clock.Now())
	return w
}

// Mirrored receives what the Watcher sees. It is closed once the Watcher is
// closed, or fell too far behind.
func (w *Watcher) Mirrored() <-chan Mirrored { return w.mirrored }

// Close stops watching.
func (w *Watcher) Close() {
	h := w.host
	h.mu.Lock()
	defer h.unlock()
	h.dropWatcher(w)
}

// dropWatcher stops w. Callers hold h.mu.
func (h *Host) dropWatcher(w *Watcher) {
	if h.watchers[w] {
		delete(h.watchers, w)
		close(w.mirrored)
	}
}

// startWatcher starts w over on a Device of its own. Callers hold h.mu.
func (h *Host) startWatcher(w *Watcher, now time.Time) {
	profile := PreviewProfile
	if h.device != nil {
		profile = h.device.HelloReply.HardwareProfile
	}
	h.mirror(w, Mirrored{Device: &profile})
	for _, install := range h.copiesOnDevice() {
		h.mirrorMessage(w, install)
	}
	h.mirrorMessage(w, protocol.NewHello(now.UnixMilli()))
	msgs, _ := h.greeting()
	for _, msg := range msgs {
		h.mirrorMessage(w, msg)
	}
}

// mirrorMessage hands one message to w. Callers hold h.mu.
func (h *Host) mirrorMessage(w *Watcher, msg any) {
	line, err := protocol.Encode(msg)
	if err != nil {
		slog.Warn("encode for a Watcher", "err", err)
		return
	}
	h.mirror(w, mirroredLine(line))
}

// mirroredLine is a protocol line, newline included, as a Watcher sees it.
func mirroredLine(line []byte) Mirrored {
	return Mirrored{Line: bytes.TrimSuffix(line, []byte("\n"))}
}

// mirror hands m to w, closing a Watcher that fell too far behind. Callers
// hold h.mu.
func (h *Host) mirror(w *Watcher, m Mirrored) {
	if !h.watchers[w] {
		return
	}
	select {
	case w.mirrored <- m:
	default:
		slog.Info("a Watcher fell behind: closed")
		h.dropWatcher(w)
	}
}

// mirrorLine hands a line sent to the Device to every Watcher. Callers hold
// h.mu.
func (h *Host) mirrorLine(line []byte) {
	for w := range h.watchers {
		h.mirror(w, mirroredLine(line))
	}
}

// ThemeCopies keeps a copy of every Theme and Sprite Pack the Host installed
// on the Device, which the Host can't read back from it, so that the web
// UI's preview can render them too.
type ThemeCopies interface {
	Load() []protocol.Install
	Save(install protocol.Install) error
	Remove(kind protocol.Kind, name string) error
}

// WithThemeCopies keeps copies of what the Host installs in c, and gives
// them to every Watcher.
func WithThemeCopies(c ThemeCopies) Option {
	return func(h *Host) { h.copies = c }
}

// copiesOnDevice are the copies of what the claimed Device has installed,
// Sprite Packs first; every copy without a Device. Callers hold h.mu.
func (h *Host) copiesOnDevice() []protocol.Install {
	if h.copies == nil {
		return nil
	}
	var out []protocol.Install
	for _, kind := range []protocol.Kind{protocol.KindSpritePack, protocol.KindTheme} {
		has := h.onDevice.themes
		if kind == protocol.KindSpritePack {
			has = h.onDevice.spritePacks
		}
		for _, install := range h.themeCopies {
			if install.Kind == kind && (h.device == nil || slices.Contains(has, install.Name)) {
				out = append(out, install)
			}
		}
	}
	return out
}
