package host

import "sync"

// Hold keeps the Host Daemon from retiring while it is held, as a session
// does: the web UI holds it while its page is open.
type Hold struct {
	host *Host
	once sync.Once
}

// Hold holds the Host until Release. It fails with ErrRetired once the Host
// has retired or closed.
func (h *Host) Hold() (*Hold, error) {
	h.mu.Lock()
	defer h.unlock()
	if h.hasRetired() || h.closed {
		return nil, ErrRetired
	}
	h.holds++
	h.updateExitGrace(h.clock.Now())
	return &Hold{host: h}, nil
}

// Release lets the Host retire again after the exit grace. Releasing twice
// is releasing once.
func (hd *Hold) Release() {
	hd.once.Do(func() {
		h := hd.host
		h.mu.Lock()
		defer h.unlock()
		h.holds--
		h.updateExitGrace(h.clock.Now())
	})
}
