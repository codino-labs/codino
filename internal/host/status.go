package host

import "time"

// Status is what `codino status` shows: the known sessions and the Device.
type Status struct {
	AggregateState SessionState     `json:"aggregate_state"`
	Sessions       []SessionSummary `json:"sessions"`
	Device         DeviceConnection `json:"device"`
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
	PID        int    `json:"pid,omitempty"`
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
}

// Status reports the sessions, by Tile slot, and the Device connection.
func (h *Host) Status() Status {
	h.mu.Lock()
	defer h.unlock()
	h.dropGoneDevice()
	st := Status{AggregateState: h.sessions.aggregateState(), Sessions: []SessionSummary{}}
	for _, s := range h.sessions.ordered() {
		st.Sessions = append(st.Sessions, SessionSummary{
			ID: s.id, State: s.state, StateEntered: s.stateEntered,
			Project: s.project, ProjectColor: s.projectColor, Tile: s.tile,
			PID: s.pid, Transcript: s.transcript,
		})
	}
	if h.device != nil {
		st.Device = DeviceConnection{
			Claimed:  true,
			Name:     h.device.Name,
			Firmware: h.device.HelloReply.Firmware,
			Protocol: h.device.HelloReply.Protocol,
		}
	} else {
		st.Device.Problem = h.deviceProblem
	}
	return st
}
