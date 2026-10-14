package host

import (
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/picture"
	"github.com/codino-labs/codino/internal/protocol"
)

// Showing is a picture the Host shows on the Device in Raw Mode (`codino
// show`), until it is stopped or a newer one replaces it. The Device decides
// what is visible: Awaiting Input always pre-empts Raw Mode (ADR-0001).
type Showing struct {
	host    *Host
	picture *picture.Picture
	// start is when the animation started; its frames loop from then.
	start time.Time
	ended chan struct{}
	once  sync.Once

	// mu guards frames, the picture rendered per matrix size.
	mu     sync.Mutex
	frames map[matrixSize][]picture.Frame
}

type matrixSize struct{ width, height int }

// Show puts p on the Device in Raw Mode, scaled to the claimed Device's
// matrix, looping an animation by its frame delays. It replaces the picture
// shown before, if any. A Device claimed later gets it too. While a picture
// is shown the Host Daemon doesn't retire. It fails with ErrRetired once the
// Host has retired or closed.
func (h *Host) Show(p *picture.Picture) (*Showing, error) {
	s := &Showing{host: h, picture: p, ended: make(chan struct{}), frames: map[matrixSize][]picture.Frame{}}
	// Scaling can take a while for a large animation: not under h.mu.
	if size, ok := h.claimedMatrix(); ok {
		s.framesFor(size)
	}
	h.mu.Lock()
	defer h.unlock()
	if h.hasRetired() || h.closed {
		return nil, ErrRetired
	}
	if h.showing != nil {
		h.showing.end()
	}
	s.start = h.clock.Now()
	h.showing = s
	h.updateExitGrace(s.start)
	h.enterRawMode()
	return s, nil
}

// ShowsAnimation reports whether an animation is shown (Show): its frames
// are due more often than a still picture's.
func (h *Host) ShowsAnimation() bool {
	h.mu.Lock()
	defer h.unlock()
	return h.showing != nil && h.showing.picture.Animated()
}

// Stop ends the show: the Device returns to its own rendering. Stopping a
// show that a newer one replaced changes nothing on the Device.
func (s *Showing) Stop() {
	h := s.host
	h.mu.Lock()
	if h.showing == s {
		h.showing = nil
		h.send(protocol.NewRawModeExit())
		h.updateExitGrace(h.clock.Now())
	}
	h.unlock()
	s.end()
}

// Ended is closed once the show is stopped, replaced by a newer one, or the
// Host is closed.
func (s *Showing) Ended() <-chan struct{} { return s.ended }

func (s *Showing) end() { s.once.Do(func() { close(s.ended) }) }

// framesFor renders the picture for a matrix size, once per size.
func (s *Showing) framesFor(size matrixSize) []picture.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.frames[size]
	if !ok {
		f = s.picture.Frames(size.width, size.height)
		s.frames[size] = f
	}
	return f
}

// prepareShow renders the show for a Device about to be claimed, so claiming
// it doesn't scale the picture under h.mu.
func (h *Host) prepareShow(dev *devicelink.Device) {
	h.mu.Lock()
	s := h.showing
	rotation := h.prefs.Rotation
	h.unlock()
	if size, ok := matrixOf(dev, rotation); s != nil && ok {
		s.framesFor(size)
	}
}

// prepareRotatedShow renders the show for the claimed Device turned by
// rotation, so a rotation Preference that turns the matrix sideways doesn't
// scale the picture under h.mu.
func (h *Host) prepareRotatedShow(rotation int) {
	h.mu.Lock()
	s, dev := h.showing, h.device
	h.unlock()
	if s == nil || dev == nil {
		return
	}
	if size, ok := matrixOf(dev, rotation); ok {
		s.framesFor(size)
	}
}

// claimedMatrix is the matrix size of the claimed Device, if any.
func (h *Host) claimedMatrix() (matrixSize, bool) {
	h.mu.Lock()
	defer h.unlock()
	return h.matrix()
}

// matrix is the claimed Device's matrix size as the picture is seen on it.
// Callers hold h.mu.
func (h *Host) matrix() (matrixSize, bool) {
	if h.device == nil {
		if len(h.watchers) == 0 {
			return matrixSize{}, false
		}
		return matrixOfProfile(PreviewProfile, h.prefs.Rotation)
	}
	return matrixOf(h.device, h.prefs.Rotation)
}

// matrixOf is a Device's matrix size, from its Hardware Profile, as the
// picture is seen on it: turned sideways by the rotation Preference, width
// and height swap.
func matrixOf(dev *devicelink.Device, rotation int) (matrixSize, bool) {
	return matrixOfProfile(dev.HelloReply.HardwareProfile, rotation)
}

func matrixOfProfile(hp protocol.HardwareProfile, rotation int) (matrixSize, bool) {
	if hp.Width < 1 || hp.Height < 1 {
		return matrixSize{}, false
	}
	if sideways(rotation) {
		return matrixSize{hp.Height, hp.Width}, true
	}
	return matrixSize{hp.Width, hp.Height}, true
}

// enterRawMode puts the claimed Device in Raw Mode with the current frame of
// the show. Callers hold h.mu.
func (h *Host) enterRawMode() {
	if h.showing == nil || !h.listening() {
		return
	}
	h.send(protocol.NewRawModeEnter())
	h.shownFrame = -1
	h.sendShowFrame()
}

// refreshShow is for each heartbeat during a show: after a silence the
// Device took for Disconnected (a Host asleep), which ended its Raw Mode, it
// enters Raw Mode again; otherwise it resends the frame shown, so a frame lost
// or garbled on the way is good again a heartbeat later. Callers hold h.mu.
func (h *Host) refreshShow(silent bool) {
	if silent {
		h.enterRawMode()
		return
	}
	h.shownFrame = -1
}

// sendShowFrame sends the frame of the show due now, unless the Device has
// it already. Callers hold h.mu.
func (h *Host) sendShowFrame() {
	i, frame, ok := h.showFrame()
	if !ok || i == h.shownFrame {
		return
	}
	h.shownFrame = i
	h.send(frame)
}

// showFrame is the frame of the show due now and its index; false without a
// show or a matrix to show it on. Callers hold h.mu.
func (h *Host) showFrame() (int, protocol.RawFrame, bool) {
	size, ok := h.matrix()
	if h.showing == nil || !ok {
		return 0, protocol.RawFrame{}, false
	}
	frames := h.showing.framesFor(size)
	i := frameAt(frames, h.clock.Now().Sub(h.showing.start))
	return i, protocol.NewRawFrame(frames[i].RGB), true
}

// frameAt is the index of the frame due `elapsed` into a looping animation.
func frameAt(frames []picture.Frame, elapsed time.Duration) int {
	var loop time.Duration
	for _, f := range frames {
		loop += f.Delay
	}
	if len(frames) < 2 || loop <= 0 {
		return 0
	}
	t := elapsed % loop
	for i, f := range frames {
		if t < f.Delay {
			return i
		}
		t -= f.Delay
	}
	return len(frames) - 1
}
