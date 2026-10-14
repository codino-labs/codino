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

// SessionStates lists every Session State, least urgent first.
var SessionStates = []SessionState{Idle, Working, Done, Error, AwaitingInput}

// urgency orders Session States for the Aggregate State: Awaiting Input >
// Error > Done > Working > Idle.
func (s SessionState) urgency() int {
	for i, state := range SessionStates {
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
	id           string
	project      string
	state        SessionState
	stateEntered time.Time
	projectColor string
	tile         int
}

// tracker is the Session Tracker: every Agent Session, keyed by session id.
type tracker struct {
	sessions map[string]*session
}

func newTracker() *tracker { return &tracker{sessions: map[string]*session{}} }

// apply records the event at now. It returns the session when it changed (nil
// when it didn't) and whether it was removed.
func (t *tracker) apply(e Event, now time.Time) (s *session, removed bool) {
	s, known := t.sessions[e.SessionID]
	if e.End {
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
	changed := !known
	if e.State != s.state {
		s.state = e.State
		s.stateEntered = now
		changed = true
	}
	if e.Project != "" {
		s.project = e.Project
	}
	if !changed {
		return nil, false
	}
	return s, false
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
