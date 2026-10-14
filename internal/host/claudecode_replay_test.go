package host_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/claudecode"
	"github.com/codino-labs/codino/internal/host"
)

// recordedCall is one hook call of a fixtures/hooks/claude-code recording.
type recordedCall struct {
	T        float64         `json:"t"`
	Event    string          `json:"event"`
	Input    json.RawMessage `json:"input"`
	Liveness struct {
		ClaudePID int `json:"CLAUDE_PID"`
	} `json:"liveness"`
}

// recording reads a hook spike recording, skipping its meta line.
func recording(t *testing.T, scenario string) []recordedCall {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "fixtures", "hooks", "claude-code", scenario+".jsonl"))
	if err != nil {
		t.Fatalf("missing recording: %v", err)
	}
	defer f.Close()
	var calls []recordedCall
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for first := true; sc.Scan(); first = false {
		if first {
			continue
		}
		var c recordedCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		calls = append(calls, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

// recordingStart is the Host time of a recording's first hook call.
const recordingStart = 1760432400000

// hookCall translates one recorded call through the Claude Code Adapter, as
// `codino hook` would when it received it at receivedAt.
func hookCall(t *testing.T, c recordedCall, receivedAt time.Time) []host.Event {
	t.Helper()
	events, err := claudecode.Events(claudecode.Hook{Input: c.Input, ReceivedAt: receivedAt, PID: c.Liveness.ClaudePID})
	if err != nil {
		t.Fatalf("%s at t=%v: %v", c.Event, c.T, err)
	}
	return events
}

// replay feeds a recording through the Adapter into a Host, moving the fake
// clock to each call's time, and returns the Session States the Device was
// sent, in order ("removed" for a session_remove).
func replay(t *testing.T, scenario string, opts ...host.Option) []string {
	t.Helper()
	return replayOn(t, clockAt(recordingStart), scenario, opts...)
}

func replayOn(t *testing.T, clock *fakeClock, scenario string, opts ...host.Option) []string {
	t.Helper()
	return replayThen(t, clock, recording(t, scenario), 0, opts...)
}

// replayThen replays calls like replayOn, after which the Host Daemon keeps
// ticking for tail with no more hook calls.
func replayThen(t *testing.T, clock *fakeClock, calls []recordedCall, tail time.Duration, opts ...host.Option) []string {
	t.Helper()
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), opts...)
	defer h.Close()
	h.Tick()
	for _, c := range calls {
		at := time.UnixMilli(recordingStart).Add(time.Duration(c.T * float64(time.Second)))
		// The Host Daemon ticks between hook calls.
		for clock.Now().Add(time.Second).Before(at) {
			clock.Advance(time.Second)
			h.Tick()
		}
		clock.now = at
		apply(t, h, hookCall(t, c, clock.Now())...)
	}
	for end := clock.Now().Add(tail); clock.Now().Before(end); {
		clock.Advance(time.Second)
		h.Tick()
	}
	return sessionStates(t, dev.Received())
}

// fakeLiveness is the fake liveness probe: each Agent, by pid, exits at a
// scripted time.
type fakeLiveness struct {
	clock *fakeClock
	// exits is when each pid's Agent exits; absent pids live forever.
	exits map[int]time.Time
	// started, when set, is the start time a live pid's Agent has: a
	// different one means the pid was reused.
	started map[int]uint64
	probed  []int
}

func (f *fakeLiveness) Gone(pid int, started uint64) bool {
	f.probed = append(f.probed, pid)
	if exit, ok := f.exits[pid]; ok && !f.clock.Now().Before(exit) {
		return true
	}
	if now, ok := f.started[pid]; ok && started != 0 && started != now {
		return true
	}
	return false
}

// killedAfter returns a liveness probe under which the Agent of a recording
// exits d after its last hook call.
func killedAfter(t *testing.T, clock *fakeClock, scenario string, d time.Duration) *fakeLiveness {
	t.Helper()
	calls := recording(t, scenario)
	last := calls[len(calls)-1]
	at := time.UnixMilli(recordingStart).Add(time.Duration(last.T * float64(time.Second)))
	return &fakeLiveness{clock: clock, exits: map[int]time.Time{last.Liveness.ClaudePID: at.Add(d)}}
}

// fakeTranscripts is the fake transcript probe: the human interrupts the
// turn at a scripted time, after which every transcript shows it.
type fakeTranscripts struct {
	clock       *fakeClock
	interrupted time.Time
	probed      []string
}

func (f *fakeTranscripts) Read(path string) host.Transcript {
	f.probed = append(f.probed, path)
	// Written along with every hook: only the interrupt is news.
	tr := host.Transcript{Modified: f.clock.Now()}
	if !f.interrupted.IsZero() && !f.clock.Now().Before(f.interrupted) {
		tr.Interrupted = f.interrupted
	}
	return tr
}

// interruptAfter returns a probe that shows the human interrupting the turn
// d after the first hook call of the given event in a recording.
func interruptAfter(t *testing.T, scenario, event string, d time.Duration) *fakeTranscripts {
	t.Helper()
	for _, c := range recording(t, scenario) {
		if c.Event == event {
			at := time.UnixMilli(recordingStart).Add(time.Duration(c.T * float64(time.Second)))
			return &fakeTranscripts{interrupted: at.Add(d)}
		}
	}
	t.Fatalf("%s has no %s", scenario, event)
	return nil
}

// sessionStates reduces protocol lines to the Session State of each
// session_upsert and "removed" for each session_remove.
func sessionStates(t *testing.T, lines []string) []string {
	t.Helper()
	var states []string
	for _, line := range lines {
		var m struct{ Type, State string }
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		switch m.Type {
		case "session_upsert":
			states = append(states, m.State)
		case "session_remove":
			states = append(states, "removed")
		}
	}
	return states
}

func assertStates(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Session States sent to the Device\n got: %q\nwant: %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Session States sent to the Device\n got: %q\nwant: %q", got, want)
		}
	}
}

func TestClaudeCodeTurnGoesIdleWorkingDoneThenRemoved(t *testing.T) {
	assertStates(t, replay(t, "plain-turn-done"), "idle", "working", "done", "removed")
}

func TestClaudeCodePermissionPromptIsAwaitingInputUntilTheToolFinishes(t *testing.T) {
	assertStates(t, replay(t, "permission-approved"), "idle", "working", "awaiting_input", "working", "done", "removed")
}

func TestClaudeCodeQuestionsAndPlanApprovalsAreAwaitingInput(t *testing.T) {
	for _, scenario := range []string{"question-tool", "question-tool-async-hooks", "plan-approval"} {
		t.Run(scenario, func(t *testing.T) {
			assertStates(t, replay(t, scenario), "idle", "working", "awaiting_input", "working", "done", "removed")
		})
	}
}

func TestClaudeCodeAutomaticDenialInPrintModeGoesBackToWorking(t *testing.T) {
	assertStates(t, replay(t, "print-permission-denied"), "idle", "working", "awaiting_input", "working", "done", "removed")
}

func TestClaudeCodeStopFailureIsError(t *testing.T) {
	for _, scenario := range []string{"stopfailure-model-not-found", "print-stopfailure-model-not-found"} {
		t.Run(scenario, func(t *testing.T) {
			assertStates(t, replay(t, scenario), "idle", "working", "error", "removed")
		})
	}
}

func TestClaudeCodeStopWithBackgroundSubagentsStaysWorking(t *testing.T) {
	assertStates(t, replay(t, "subagent-stop-before-handback"), "idle", "working", "done", "removed")
}

func TestClaudeCodeSubagentsFoldIntoTheirParentSession(t *testing.T) {
	assertStates(t, replay(t, "subagents-parallel"), "idle", "working", "done", "removed")
}

func TestClaudeCodeSessionKilledWithoutSessionEndIsReaped(t *testing.T) {
	// kill -9 fires no SessionEnd: the Host notices the Agent is gone.
	for scenario, want := range map[string][]string{
		"killed-working-sigkill": {"idle", "working", "removed"},
		"killed-idle-sigkill":    {"idle", "working", "done", "removed"},
	} {
		t.Run(scenario, func(t *testing.T) {
			clock := clockAt(recordingStart)
			liveness := killedAfter(t, clock, scenario, 3*time.Second)
			assertStates(t, replayThen(t, clock, recording(t, scenario), 10*time.Second, host.WithLivenessProbe(liveness)), want...)
		})
	}
}

func TestClaudeCodeKilledSessionWithoutALivenessHandleFallsBackToIdle(t *testing.T) {
	// No pid (CLAUDE_PID unset, no /proc): the session can't be reaped, but
	// its quiet transcript stops it haunting the matrix as Working.
	clock := clockAt(recordingStart)
	calls := recording(t, "killed-working-sigkill")
	last := time.UnixMilli(recordingStart).Add(time.Duration(calls[len(calls)-1].T * float64(time.Second)))
	transcript := &quietTranscript{modified: last}
	liveness := &fakeLiveness{clock: clock, exits: map[int]time.Time{calls[0].Liveness.ClaudePID: last}}
	for i := range calls {
		calls[i].Liveness.ClaudePID = 0
	}

	got := replayThen(t, clock, calls, host.StaleAfter+time.Second,
		host.WithTranscriptProbe(transcript), host.WithLivenessProbe(liveness))

	assertStates(t, got, "idle", "working", "idle")
}

func TestClaudeCodeSessionStaysWhileItsAgentLives(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock}
	assertStates(t, replayThen(t, clock, recording(t, "killed-idle-sigkill"), time.Minute, host.WithLivenessProbe(liveness)),
		"idle", "working", "done")
	if len(liveness.probed) == 0 {
		t.Fatal("the Agent's liveness was never probed")
	}
}

func TestClaudeCodeSessionEndingNormallyIsRemovedOnce(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := killedAfter(t, clock, "sigterm-idle", 0)
	assertStates(t, replayThen(t, clock, recording(t, "sigterm-idle"), 10*time.Second, host.WithLivenessProbe(liveness)),
		"idle", "working", "done", "removed")
}

func TestClaudeCodeHookArrivingLateDoesNotUndoANewerState(t *testing.T) {
	// Async hooks race: PreToolUse fired 7 ms before PermissionRequest but
	// reaches the Host Daemon after it.
	clock := clockAt(recordingStart)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()
	calls := recording(t, "question-tool-async-hooks")
	at := func(c recordedCall) time.Time {
		return time.UnixMilli(recordingStart).Add(time.Duration(c.T * float64(time.Second)))
	}
	for i := 0; i < len(calls); i++ {
		c := calls[i]
		if c.Event == "PreToolUse" && calls[i+1].Event == "PermissionRequest" {
			late, next := c, calls[i+1]
			clock.now = at(next).Add(30 * time.Millisecond)
			apply(t, h, hookCall(t, next, at(next))...)
			sent := len(dev.Received())
			apply(t, h, hookCall(t, late, at(late))...)
			if got := since(dev, sent); len(got) > 0 {
				t.Fatalf("the late PreToolUse reached the Device: %q", got)
			}
			i++
			continue
		}
		clock.now = at(c)
		apply(t, h, hookCall(t, c, at(c))...)
	}

	assertStates(t, sessionStates(t, dev.Received()), "idle", "working", "awaiting_input", "working", "done", "removed")
}

func TestClaudeCodeHookArrivingAfterSessionEndDoesNotBringTheSessionBack(t *testing.T) {
	clock := clockAt(recordingStart)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()
	calls := recording(t, "sigterm-idle")
	stop, end := calls[len(calls)-2], calls[len(calls)-1]
	if stop.Event != "Stop" || end.Event != "SessionEnd" {
		t.Fatalf("recording changed: %s, %s", stop.Event, end.Event)
	}
	base := time.UnixMilli(recordingStart)
	for _, c := range calls[:len(calls)-2] {
		apply(t, h, hookCall(t, c, base)...)
	}

	clock.Advance(time.Second)
	apply(t, h, hookCall(t, end, clock.Now())...)
	sent := len(dev.Received())
	apply(t, h, hookCall(t, stop, clock.Now().Add(-10*time.Millisecond))...)

	if got := since(dev, sent); len(got) > 0 {
		t.Fatalf("the late Stop brought the session back: %q", got)
	}
	if n := len(h.Status().Sessions); n != 0 {
		t.Fatalf("%d sessions after SessionEnd, want none", n)
	}
}

func TestClaudeCodeInterruptedTurnGoesBackToIdle(t *testing.T) {
	for _, tc := range []struct {
		scenario, lastHook string
		want               []string
	}{
		{"esc-interrupt-tool", "PreToolUse", []string{"idle", "working", "idle", "working", "done", "removed"}},
		{"ctrl-c-interrupt-tool", "PreToolUse", []string{"idle", "working", "idle", "working", "done", "removed"}},
		{"esc-interrupt-streaming", "UserPromptSubmit", []string{"idle", "working", "idle", "working", "done", "removed"}},
		{"permission-denied", "PermissionRequest", []string{"idle", "working", "awaiting_input", "idle", "removed"}},
		{"permission-denied-esc", "PermissionRequest", []string{"idle", "working", "awaiting_input", "idle", "removed"}},
		{"plan-rejected-esc", "PermissionRequest", []string{"idle", "working", "awaiting_input", "idle", "removed"}},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			transcripts := interruptAfter(t, tc.scenario, tc.lastHook, 5*time.Second)
			clock := clockAt(recordingStart)
			transcripts.clock = clock
			assertStates(t, replayOn(t, clock, tc.scenario, host.WithTranscriptProbe(transcripts)), tc.want...)
		})
	}
}

func TestInterruptRightAfterAHookWhoseProcessStartedLateStillGoesIdle(t *testing.T) {
	// A hook stamps its event when its process starts, a little after Claude
	// Code fired it; the human may press Esc in between.
	clock := clockAt(recordingStart)
	transcripts := &fakeTranscripts{clock: clock, interrupted: clock.Now().Add(time.Second)}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcripts))
	defer h.Close()
	h.Tick()
	clock.Advance(time.Second + 100*time.Millisecond)
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Transcript: "/t/a.jsonl",
		At: transcripts.interrupted.Add(50 * time.Millisecond)})

	clock.Advance(2 * time.Second)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "working", "idle")
}

func TestInterruptBeforeTheNextPromptKeepsTheNewTurnWorking(t *testing.T) {
	clock := clockAt(recordingStart)
	transcripts := &fakeTranscripts{clock: clock, interrupted: clock.Now()}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcripts))
	defer h.Close()
	h.Tick()
	clock.Advance(2 * time.Second)
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Transcript: "/t/a.jsonl"})

	clock.Advance(2 * time.Second)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "working")
	if len(transcripts.probed) == 0 {
		t.Fatal("the transcript was never probed")
	}
}
