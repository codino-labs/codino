package host

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// SessionState is the status of one Agent Session. Its value is the wire name.
type SessionState string

const (
	Idle          SessionState = "idle"
	Working       SessionState = "working"
	AwaitingInput SessionState = "awaiting_input"
	Done          SessionState = "done"
	Error         SessionState = "error"
)

// sessionStates lists every Session State, least urgent first.
var sessionStates = []SessionState{Idle, Working, Done, Error, AwaitingInput}

// urgency orders Session States for the Aggregate State: Awaiting Input >
// Error > Done > Working > Idle.
func (s SessionState) urgency() int {
	for i, state := range sessionStates {
		if s == state {
			return i
		}
	}
	return -1
}

// ParseSessionState accepts a Session State by its wire name.
func ParseSessionState(name string) (SessionState, error) {
	s := SessionState(name)
	if s.urgency() < 0 {
		return "", fmt.Errorf("unknown Session State %q (want idle, working, awaiting_input, done or error)", name)
	}
	return s, nil
}

// Event is one change to an Agent Session, from `codino event` or an Adapter.
// It is the shared internal API every Agent's signals are translated into.
type Event struct {
	// SessionID keys the Agent Session. Required.
	SessionID string `json:"session_id"`
	// State is the Session State the session enters. Required unless End.
	State SessionState `json:"state,omitempty"`
	// Project is the session's project directory; kept when empty.
	Project string `json:"project,omitempty"`
	// End removes the session.
	End bool `json:"end,omitempty"`
	// At is when the Agent signalled the event; zero means when the Host
	// applies it. Events can reach the Host out of order (async hooks race):
	// one older than the last event applied to its session is dropped.
	At time.Time `json:"at,omitzero"`
	// Acknowledge means the human has seen the session (a prompt typed into
	// it). It travels with the event so the Host can act on it; Acknowledge
	// semantics (clearing Done, resetting Escalation) are not applied yet.
	Acknowledge bool `json:"acknowledge,omitempty"`
	// PID is the Agent's process, the session's liveness handle; kept when 0.
	PID int `json:"pid,omitempty"`
	// PIDStarted is when PID's process started, as the LivenessProbe's
	// platform measures it, so a reused pid isn't taken for the Agent; 0 when
	// unknown. It goes with PID.
	PIDStarted uint64 `json:"pid_started,omitempty"`
	// Transcript is the Agent's transcript file; kept when empty.
	Transcript string `json:"transcript,omitempty"`
}

// Validate reports whether the event can be applied.
func (e Event) Validate() error {
	if e.SessionID == "" {
		return errors.New("event: missing session id")
	}
	if e.End {
		return nil
	}
	if _, err := ParseSessionState(string(e.State)); err != nil {
		return fmt.Errorf("event: %w", err)
	}
	return nil
}

// DefaultProjectColor is every session's Project Color until the Project
// Color Allocator lands.
const DefaultProjectColor = "#3a86ff"

// session is one Agent Session as the Session Tracker holds it.
type session struct {
	id string
	// lastEventAt is the At of the newest event applied.
	lastEventAt  time.Time
	project      string
	state        SessionState
	stateEntered time.Time
	projectColor string
	tile         int
	liveness     livenessHandle
	transcript   string
}

// livenessHandle names the Agent's process: its pid and, when known, when it
// started. The zero value means the session can't be probed.
type livenessHandle struct {
	pid     int
	started uint64
}

// tracker is the Session Tracker: every Agent Session, keyed by session id.
type tracker struct {
	sessions map[string]*session
	// ended is when each recently removed session ended, so an event that
	// was older but arrives later doesn't bring the session back.
	ended map[string]time.Time
}

func newTracker() *tracker {
	return &tracker{sessions: map[string]*session{}, ended: map[string]time.Time{}}
}

// endedMemory is how long the tracker remembers a removed session: much
// longer than any async hook takes to reach the Host Daemon.
const endedMemory = time.Minute

// apply records the event at now. It returns the session when it changed (nil
// when it didn't) and whether it was removed.
func (t *tracker) apply(e Event, now time.Time) (s *session, removed bool) {
	at := notAfter(e.At, now)
	for id, ended := range t.ended {
		if now.Sub(ended) > endedMemory {
			delete(t.ended, id)
		}
	}
	s, known := t.sessions[e.SessionID]
	if known && at.Before(s.lastEventAt) {
		return nil, false
	}
	if ended, ok := t.ended[e.SessionID]; ok && !known && !at.After(ended) {
		return nil, false
	}
	if e.End {
		t.ended[e.SessionID] = at
		if !known {
			return nil, false
		}
		delete(t.sessions, e.SessionID)
		return s, true
	}
	if !known {
		s = &session{id: e.SessionID, projectColor: DefaultProjectColor, tile: t.freeTile()}
		t.sessions[e.SessionID] = s
	}
	s.lastEventAt = at
	changed := !known
	if e.State != s.state {
		s.state = e.State
		s.stateEntered = at
		changed = true
	}
	if e.Project != "" {
		s.project = e.Project
	}
	if e.PID != 0 {
		s.liveness = livenessHandle{pid: e.PID, started: e.PIDStarted}
	}
	if e.Transcript != "" {
		s.transcript = e.Transcript
	}
	if !changed {
		return nil, false
	}
	return s, false
}

// interrupt returns a Working or Awaiting Input session to Idle when the
// human interrupted it at at (zero: not interrupted), after its last event.
// It returns the session when it changed.
func (t *tracker) interrupt(id string, at, now time.Time) *session {
	s := t.sessions[id]
	if s == nil || at.IsZero() || !s.interruptible() || at.Before(s.lastEventAt.Add(-hookStartLag)) {
		return nil
	}
	at = notAfter(at, now)
	if at.After(s.lastEventAt) {
		s.lastEventAt = at
	}
	s.state = Idle
	s.stateEntered = at
	return s
}

// staleToIdle returns a Working session to Idle when neither a hook nor its
// transcript, last written at modified (zero when unknown), has shown any
// activity for StaleAfter. It returns the session when it changed.
func (t *tracker) staleToIdle(id string, modified, now time.Time) *session {
	s := t.sessions[id]
	if s == nil || s.state != Working || s.transcript == "" {
		return nil
	}
	active := s.lastEventAt
	if modified.After(active) {
		active = modified
	}
	if now.Sub(active) < StaleAfter {
		return nil
	}
	s.state = Idle
	s.stateEntered = now
	return s
}

// reap removes a session whose Agent exited at now, unless an event has
// meanwhile named another Agent for it (e.g. the session was resumed). It
// returns the session when it was removed.
func (t *tracker) reap(id string, gone livenessHandle, now time.Time) *session {
	s := t.sessions[id]
	if s == nil || s.liveness != gone {
		return nil
	}
	delete(t.sessions, id)
	t.ended[id] = now
	return s
}

// hookStartLag bounds how long after Claude Code fires a hook its process
// starts and stamps the event: an interrupt this much older than the last
// event may still have come after it. A human can't interrupt a turn and
// submit a new prompt that fast.
const hookStartLag = 250 * time.Millisecond

// interruptible reports whether the human interrupting the turn would leave
// the session stuck without a hook: Working or Awaiting Input.
func (s *session) interruptible() bool { return s.state == Working || s.state == AwaitingInput }

// notAfter clamps an Agent's timestamp to the Host clock.
func notAfter(at, now time.Time) time.Time {
	if at.IsZero() || at.After(now) {
		return now
	}
	return at
}

// transcriptsToProbe returns the transcript of every session a hook-less
// interrupt could leave stuck, by session id.
func (t *tracker) transcriptsToProbe() map[string]string {
	probe := map[string]string{}
	for id, s := range t.sessions {
		if s.transcript != "" && s.interruptible() {
			probe[id] = s.transcript
		}
	}
	return probe
}

// livenessToProbe returns the liveness handle of every session that has one,
// by session id. A session without one is never reaped.
func (t *tracker) livenessToProbe() map[string]livenessHandle {
	probe := map[string]livenessHandle{}
	for id, s := range t.sessions {
		if s.liveness.pid != 0 {
			probe[id] = s.liveness
		}
	}
	return probe
}

// freeTile is the lowest Tile slot no session holds. A session keeps its slot
// for its whole life.
func (t *tracker) freeTile() int {
	used := map[int]bool{}
	for _, s := range t.sessions {
		used[s.tile] = true
	}
	tile := 0
	for used[tile] {
		tile++
	}
	return tile
}

// ordered returns the sessions by Tile slot.
func (t *tracker) ordered() []*session {
	out := make([]*session, 0, len(t.sessions))
	for _, s := range t.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].tile < out[j].tile })
	return out
}

// aggregateState is the most urgent Session State; Idle when there are no
// sessions.
func (t *tracker) aggregateState() SessionState {
	aggregate := Idle
	for _, s := range t.sessions {
		if s.state.urgency() > aggregate.urgency() {
			aggregate = s.state
		}
	}
	return aggregate
}
