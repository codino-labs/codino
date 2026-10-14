package host

import (
	"errors"
	"time"

	"github.com/codino-labs/codino/internal/protocol"
)

// previewing is the web UI's preview-state override: the Device shows a lone
// session that entered state at entered, in place of its sessions.
type previewing struct {
	state   SessionState
	entered time.Time
}

// Preview forces the Device into a Session State, as if a lone session had
// been in it for elapsed (so Awaiting Input shows its Escalation step), until
// EndPreview. A Device claimed meanwhile gets it too.
func (h *Host) Preview(state SessionState, elapsed time.Duration) error {
	if _, err := ParseSessionState(string(state)); err != nil {
		return err
	}
	if elapsed < 0 {
		return errors.New("a preview's elapsed time can't be negative")
	}
	h.mu.Lock()
	defer h.unlock()
	h.preview = &previewing{state: state, entered: h.clock.Now().Add(-elapsed)}
	h.sendPreview()
	return nil
}

// EndPreview returns the Device to its sessions. Nothing happens without a
// preview.
func (h *Host) EndPreview() {
	h.mu.Lock()
	defer h.unlock()
	if h.preview == nil {
		return
	}
	h.preview = nil
	h.send(protocol.NewPreviewExit())
}

// sendPreview sends the preview, if any, with the time elapsed in it until
// now. Callers hold h.mu.
func (h *Host) sendPreview() {
	if h.preview == nil {
		return
	}
	h.send(h.previewEnter())
}

// previewEnter is the preview's message now. Callers hold h.mu and have a
// preview.
func (h *Host) previewEnter() protocol.PreviewEnter {
	elapsed := h.clock.Now().Sub(h.preview.entered)
	return protocol.NewPreviewEnter(string(h.preview.state), elapsed.Milliseconds())
}
