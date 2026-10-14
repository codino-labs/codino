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
	// State is the Session State the session enters. Required unless End or
	// Acknowledge; kept when empty.
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
	// it): it clears Done and restarts Escalation, for this session only.
	// Applied before State, so a new prompt to a Done session makes it
	// Working. An Acknowledge of an unknown session is ignored.
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
	if e.End || (e.Acknowledge && e.State == "") {
		return nil
	}
	if _, err := ParseSessionState(string(e.State)); err != nil {
		return fmt.Errorf("event: %w", err)
	}
	return nil
}

// session is one Agent Session as the Session Tracker holds it.
type session struct {
	id string
	// started is when the session started, which orders reflows.
	started time.Time
	// lastEventAt is the At of the newest event applied.
	lastEventAt time.Time
	project     string
	// root is the repo root of project, which the Project Color is
	// allocated by.
	root         string
	state        SessionState
	stateEntered time.Time
	projectColor string
	tile         int
	liveness     livenessHandle
	transcript   string
	// acknowledged is when the human last Acknowledged the session since it
	// entered its state; zero when they haven't. The Device restarts
	// Escalation from it.
	acknowledged time.Time
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
	// pins are the pinned Project Colors, by repo root.
	pins map[string]string
}

func newTracker() *tracker {
	return &tracker{sessions: map[string]*session{}, ended: map[string]time.Time{}}
}

// endedMemory is how long the tracker remembers a removed session: much
// longer than any async hook takes to reach the Host Daemon.
const endedMemory = time.Minute

// change is what applying an event did to a session.
type change struct {
	// s is the session, nil when the event changed nothing.
	s       *session
	removed bool
	// upserted: s changed and the Device needs a session_upsert.
	upserted bool
	// acknowledged: s is Awaiting Input and was Acknowledged at
	// s.acknowledged, which the Device needs to restart Escalation.
	acknowledged bool
	// moved are the other sessions whose Tile slot changed: the Layout
	// reflowed because s started or ended.
	moved []*session
}

// apply records the event at now and returns what changed. root is the repo
// root of e.Project.
func (t *tracker) apply(e Event, root string, now time.Time) change {
	at := notAfter(e.At, now)
	for id, ended := range t.ended {
		if now.Sub(ended) > endedMemory {
			delete(t.ended, id)
		}
	}
	s, known := t.sessions[e.SessionID]
	if known && at.Before(s.lastEventAt) {
		return change{}
	}
	if ended, ok := t.ended[e.SessionID]; ok && !known && !at.After(ended) {
		return change{}
	}
	if e.End {
		if !known {
			t.ended[e.SessionID] = at
			return change{}
		}
		return change{s: s, removed: true, moved: t.forget(e.SessionID, at)}
	}
	if !known && e.State == "" {
		return change{} // an Acknowledge of a session the Host doesn't know
	}
	if !known {
		s = &session{id: e.SessionID, root: root, started: at, tile: noTile}
		s.projectColor = t.projectColor(s, root)
		t.sessions[e.SessionID] = s
	}
	var moved []*session
	if !known {
		for _, m := range t.assignTiles() {
			if m != s {
				moved = append(moved, m)
			}
		}
	}
	s.lastEventAt = at
	changed := !known
	if e.Acknowledge {
		changed = s.acknowledge(at) || changed
	}
	if e.State != "" && e.State != s.state {
		s.enter(e.State, at)
		changed = true
	}
	if e.Project != "" {
		if known && s.project == "" {
			// The session learns its project: it is colored by it from now on.
			s.root = root
			if color := t.projectColor(s, root); color != s.projectColor {
				s.projectColor = color
				changed = true
			}
		}
		s.project = e.Project
	}
	if e.PID != 0 {
		s.liveness = s.liveness.update(e.PID, e.PIDStarted)
	}
	if e.Transcript != "" {
		s.transcript = e.Transcript
	}
	acknowledged := e.Acknowledge && s.state == AwaitingInput && !s.acknowledged.IsZero()
	if !changed && !acknowledged {
		return change{}
	}
	return change{s: s, upserted: changed, acknowledged: acknowledged, moved: moved}
}

// acknowledge is the human seeing the session at at: Done is cleared to
// Idle, and Awaiting Input stays but its Escalation restarts. It reports
// whether the Session State changed.
func (s *session) acknowledge(at time.Time) bool {
	switch s.state {
	case Done:
		s.enter(Idle, at)
		return true
	case AwaitingInput:
		s.acknowledged = at
	}
	return false
}

// enter puts the session in state, entered at at.
func (s *session) enter(state SessionState, at time.Time) {
	s.state = state
	s.stateEntered = at
	s.acknowledged = time.Time{}
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
	s.enter(Idle, at)
	return s
}

// decayDone returns to Idle every session that has been Done for decay by
// now, entered when the decay was due: the Done decay timer Acknowledges it.
// It returns the sessions that changed, by Tile slot.
func (t *tracker) decayDone(now time.Time, decay time.Duration) []*session {
	var decayed []*session
	for _, s := range t.ordered() {
		if s.state == Done && now.Sub(s.stateEntered) >= decay {
			s.enter(Idle, s.stateEntered.Add(decay))
			decayed = append(decayed, s)
		}
	}
	return decayed
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
	s.enter(Idle, now)
	return s
}

// update is the handle after an event named pid, started at started (0 when
// that hook couldn't read it): the same pid keeps its known start time.
func (l livenessHandle) update(pid int, started uint64) livenessHandle {
	if pid == l.pid && started == 0 {
		return l
	}
	return livenessHandle{pid: pid, started: started}
}

// reap removes a session whose Agent exited at now, unless an event has
// meanwhile named another Agent for it (e.g. the session was resumed). It
// returns the session when it was removed, and the sessions whose Tile slot
// changed.
func (t *tracker) reap(id string, gone livenessHandle, now time.Time) (*session, []*session) {
	s := t.sessions[id]
	if s == nil || s.liveness != gone {
		return nil, nil
	}
	return s, t.forget(id, now)
}

// forget removes a session that ended at at, and remembers it ended so an
// older event arriving later doesn't bring it back. It returns the sessions
// whose Tile slot changed.
func (t *tracker) forget(id string, at time.Time) []*session {
	delete(t.sessions, id)
	t.ended[id] = at
	return t.assignTiles()
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

// noTile is the Tile slot of a session that has none yet.
const noTile = -1

// overflowFrom is the first Tile slot of the Overflow Tile: with more
// sessions than the eight columns hold, the eighth column stands for the rest.
const overflowFrom = 7

// tileRoom is how many Tiles the adaptive Layout has room for with n
// sessions: the whole matrix, halves, quadrants, eight columns, then seven
// columns and the Overflow Tile.
func tileRoom(n int) int {
	switch {
	case n <= 1:
		return 1
	case n == 2:
		return 2
	case n <= 4:
		return 4
	case n <= 8:
		return 8
	default:
		return overflowFrom
	}
}

// assignTiles gives every session a Tile slot for the adaptive Layout of
// this many sessions, and returns the sessions whose slot changed. A session
// keeps a slot that fits the Layout for its whole life. The others (the
// Layout shrank, a newcomer, a session in the Overflow Tile) take the first
// free slots in order of session start time; those beyond the room keep or
// take a slot from overflowFrom up, the Overflow Tile.
func (t *tracker) assignTiles() []*session {
	room := tileRoom(len(t.sessions))
	byStart := make([]*session, 0, len(t.sessions))
	for _, s := range t.sessions {
		byStart = append(byStart, s)
	}
	sort.Slice(byStart, func(i, j int) bool {
		a, b := byStart[i], byStart[j]
		if !a.started.Equal(b.started) {
			return a.started.Before(b.started)
		}
		return a.id < b.id
	})
	before := map[*session]int{}
	used := map[int]bool{}
	var homeless []*session
	for _, s := range byStart {
		before[s] = s.tile
		if s.tile != noTile && s.tile < room && !used[s.tile] {
			used[s.tile] = true
		} else {
			homeless = append(homeless, s)
		}
	}
	var overflow []*session
	for _, s := range homeless {
		if slot := firstFree(used, 0); slot < room {
			used[slot] = true
			s.tile = slot
		} else {
			overflow = append(overflow, s)
		}
	}
	// The Overflow Tile: a session keeps its slot there, a newcomer takes
	// the first free one.
	var newcomers []*session
	for _, s := range overflow {
		if s.tile >= overflowFrom && !used[s.tile] {
			used[s.tile] = true
		} else {
			newcomers = append(newcomers, s)
		}
	}
	for _, s := range newcomers {
		s.tile = firstFree(used, overflowFrom)
		used[s.tile] = true
	}
	var moved []*session
	for _, s := range byStart {
		if s.tile != before[s] {
			moved = append(moved, s)
		}
	}
	return moved
}

// firstFree is the lowest slot from from up that isn't used.
func firstFree(used map[int]bool, from int) int {
	for used[from] {
		from++
	}
	return from
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
