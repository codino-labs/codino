package host

import "time"

// Status is what `codino status` shows: the known sessions and the Device.
type Status struct {
	AggregateState SessionState     `json:"aggregate_state"`
	Sessions       []SessionSummary `json:"sessions"`
	Device         DeviceConnection `json:"device"`
	// Showing is true while a picture is shown in Raw Mode (`codino show`).
	Showing bool `json:"showing"`
	// Theme is the theme Preference in force.
	Theme string `json:"theme"`
	// Preview is the Session State the web UI forces the Device into, if
	// any (Preview).
	Preview SessionState `json:"preview,omitempty"`
	// PreferencesProblem is why the config file is ignored (it is invalid);
	// empty when its Preferences are in force.
	PreferencesProblem string `json:"preferences_problem,omitempty"`
}

// SessionSummary is one Agent Session as the Session Tracker holds it.
type SessionSummary struct {
	ID           string       `json:"id"`
	State        SessionState `json:"state"`
	StateEntered time.Time    `json:"state_entered"`
	Project      string       `json:"project,omitempty"`
	ProjectColor string       `json:"project_color"`
	Tile         int          `json:"tile"`
	// PID is the Agent's process, 0 when unknown.
	PID int `json:"pid,omitempty"`
	// PIDStarted is when PID's process started (platform units), 0 when
	// unknown.
	PIDStarted uint64 `json:"pid_started,omitempty"`
	// PIDGuessed means PID was guessed, not named by the Agent: it never
	// reaps the session.
	PIDGuessed bool   `json:"pid_guessed,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// DeviceConnection describes the link to the Device.
type DeviceConnection struct {
	Claimed  bool   `json:"claimed"`
	Name     string `json:"name,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Protocol int    `json:"protocol"`
	// Problem explains why no Device is claimed, e.g. a firmware to update.
	Problem string `json:"problem,omitempty"`
	// UpdateURL is where to update the firmware (the web flasher) when the
	// Device is refused for speaking an older protocol version.
	UpdateURL string `json:"update_url,omitempty"`
	// Themes and SpritePacks the claimed Device has, built-in ones first.
	Themes      []string `json:"themes,omitempty"`
	SpritePacks []string `json:"sprite_packs,omitempty"`
}

// Status reports the sessions, by Tile slot, and the Device connection.
func (h *Host) Status() Status {
	h.mu.Lock()
	defer h.unlock()
	h.dropGoneDevice()
	st := Status{AggregateState: h.sessions.aggregateState(), Sessions: []SessionSummary{}, Showing: h.showing != nil,
		PreferencesProblem: h.prefsProblem, Theme: h.prefs.Theme}
	if h.preview != nil {
		st.Preview = h.preview.state
	}
	for _, s := range h.sessions.ordered() {
		st.Sessions = append(st.Sessions, SessionSummary{
			ID: s.id, State: s.state, StateEntered: s.stateEntered,
			Project: s.project, ProjectColor: s.projectColor, Tile: s.tile,
			PID: s.liveness.pid, PIDStarted: s.liveness.started, PIDGuessed: s.liveness.guessed, Transcript: s.transcript,
		})
	}
	if h.device != nil {
		st.Device = DeviceConnection{
			Claimed:  true,
			Name:     h.device.Name,
			Firmware: h.device.HelloReply.Firmware,
			Protocol: h.device.HelloReply.Protocol,

			Themes:      h.onDevice.themes,
			SpritePacks: h.onDevice.spritePacks,
		}
	} else {
		st.Device.Problem, st.Device.UpdateURL = h.deviceProblem, h.deviceUpdateURL
	}
	return st
}
