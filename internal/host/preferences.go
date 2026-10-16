package host

import (
	"log/slog"
	"maps"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/preferences"
	"github.com/codino-labs/codino/internal/protocol"
)

// ReloadPreferences reads the config file now rather than at the next poll:
// the web UI changed it.
func (h *Host) ReloadPreferences() {
	if h.prefsSource != nil {
		h.reloadPreferences()
	}
}

// reloadPreferences reads the config file (file I/O: outside h.mu) and
// applies what changed. An invalid file is reported, once per problem, and
// ignored: the Host keeps the Preferences it has.
func (h *Host) reloadPreferences() {
	p, err := h.prefsSource.Load()
	if err == nil {
		h.prepareRotatedShow(p.Rotation)
	}
	h.mu.Lock()
	defer h.unlock()
	h.lastPrefsPoll = h.clock.Now()
	if err != nil {
		if problem := err.Error(); problem != h.prefsProblem {
			slog.Warn("Preferences ignored: the config file is invalid", "err", err)
			h.prefsProblem = problem
		}
		return
	}
	if h.prefsProblem != "" {
		slog.Info("Preferences valid again")
		h.prefsProblem = ""
	}
	h.setPreferences(p)
}

// setPreferences puts p in force: the Device gets them when what it renders
// by changed, and running sessions of a project whose pinned Project Color
// changed take it. Callers hold h.mu.
func (h *Host) setPreferences(p preferences.Preferences) {
	old, turnedFrom := h.prefs, h.rotation()
	h.prefs = p
	if preferencesPush(old) != preferencesPush(p) {
		h.send(preferencesPush(p))
		h.resendTurnedShow(turnedFrom) // scaled to the turned matrix by prepareRotatedShow
	}
	if !maps.Equal(old.ProjectColors, p.ProjectColors) {
		for _, s := range h.sessions.pin(p.ProjectColors) {
			h.send(upsert(s))
		}
	}
}

// preferencesPush is the push of the Preferences the Device renders by.
func preferencesPush(p preferences.Preferences) protocol.Preferences {
	return protocol.NewPreferences(protocol.Preferences{
		Theme:                 p.Theme,
		Brightness:            p.Brightness,
		Layout:                string(p.Layout),
		Rotation:              pushedRotation(p.Rotation),
		QuietMode:             p.QuietMode,
		DisconnectedTimeoutMS: p.DisconnectedTimeout.Milliseconds(),
		EscalationFasterMS:    p.Escalation.Faster.Milliseconds(),
		EscalationTakeoverMS:  p.Escalation.Takeover.Milliseconds(),
		EscalationSteadyMS:    p.Escalation.Steady.Milliseconds(),
	})
}

// pushedRotation is the rotation Preference as the push carries it.
func pushedRotation(rotation int) protocol.Rotation {
	if rotation == preferences.AutoRotation {
		return protocol.AutoRotation
	}
	return protocol.Rotation(rotation)
}

// rotation is the rotation the claimed Device turns the picture by. Callers
// hold h.mu.
func (h *Host) rotation() int { return effectiveRotation(h.prefs.Rotation, h.orientation) }

// effectiveRotation is the rotation a Device turns the picture by: the
// rotation Preference, or under auto the orientation its IMU senses.
func effectiveRotation(preference, orientation int) int {
	if preference == preferences.AutoRotation {
		return orientation
	}
	return preference
}

// orientationOf is the orientation a Device said in its hello_reply: 0
// without an IMU.
func orientationOf(dev *devicelink.Device) int {
	if o := dev.HelloReply.Orientation; o != nil {
		return *o
	}
	return 0
}

// sideways reports whether a rotation swaps the matrix's width and height.
func sideways(rotation int) bool { return rotation == 90 || rotation == 270 }
