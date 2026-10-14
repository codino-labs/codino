package host_test

import (
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// Stuck-session cleanup (#8): sessions whose Agent exited without ending them
// are reaped, and Working sessions gone quiet fall back to Idle.

func TestSessionWhoseAgentPIDWasReusedIsReaped(t *testing.T) {
	clock := clockAt(recordingStart)
	// pid 4242 is alive, but it is another process started later.
	liveness := &fakeLiveness{clock: clock, started: map[int]uint64{4242: 9001}}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithLivenessProbe(liveness))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Done, PID: 4242, PIDStarted: 1234})

	clock.Advance(host.ProbeInterval)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "done", "removed")
	if n := len(h.Status().Sessions); n != 0 {
		t.Fatalf("%d sessions after reaping, want none", n)
	}
}

func TestSessionWithoutALivenessHandleIsNeverReaped(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock, exits: map[int]time.Time{0: clock.Now()}}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithLivenessProbe(liveness))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Done})

	for range 10 {
		clock.Advance(host.ProbeInterval)
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "done")
	if len(liveness.probed) != 0 {
		t.Fatalf("probed pids %v of a session without one", liveness.probed)
	}
}

func TestResumedSessionIsNotReapedForItsPreviousAgent(t *testing.T) {
	// claude --resume continues a session in a new process: the old pid's
	// exit must not remove it.
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock, exits: map[int]time.Time{100: clock.Now()}}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithLivenessProbe(liveness))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Idle, PID: 100})
	clock.Advance(100 * time.Millisecond)
	apply(t, h, host.Event{SessionID: "a", State: host.Working, PID: 200})

	clock.Advance(host.ProbeInterval)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "idle", "working")
}

func TestLateHookDoesNotBringAReapedSessionBack(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock, exits: map[int]time.Time{100: clock.Now().Add(time.Second)}}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithLivenessProbe(liveness))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, PID: 100})
	clock.Advance(2 * time.Second)
	h.Tick()
	sent := len(dev.Received())

	// An async hook fired before the kill arrives after the reap.
	apply(t, h, host.Event{SessionID: "a", State: host.Working, PID: 100, At: clock.Now().Add(-1500 * time.Millisecond)})

	if got := since(dev, sent); len(got) > 0 {
		t.Fatalf("the late hook brought the session back: %q", got)
	}
}

func TestReapingTheLastSessionStartsTheExitGrace(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock, exits: map[int]time.Time{100: clock.Now().Add(time.Second)}}
	h := host.New(clock, devices(newReferenceDevice(t)), host.WithLivenessProbe(liveness), host.WithExitGrace(time.Minute))
	defer h.Close()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, PID: 100})

	for range 62 {
		clock.Advance(time.Second)
		h.Tick()
	}

	select {
	case <-h.Retired():
	default:
		t.Fatal("the Host did not retire an exit grace after reaping its last session")
	}
}

// quietTranscript is a transcript last written at a fixed time.
type quietTranscript struct{ modified time.Time }

func (q *quietTranscript) Read(string) host.Transcript { return host.Transcript{Modified: q.modified} }

func TestWorkingSessionWhoseTranscriptStaysUnchangedFallsBackToIdle(t *testing.T) {
	clock := clockAt(recordingStart)
	transcript := &quietTranscript{modified: clock.Now().Add(time.Minute)}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcript))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Transcript: "/t/a.jsonl"})

	// Quiet since the transcript was last written, a minute after the hook.
	for clock.Now().Before(transcript.modified.Add(host.StaleAfter - time.Second)) {
		clock.Advance(time.Second)
		h.Tick()
	}
	assertStates(t, sessionStates(t, dev.Received()), "working")
	clock.Advance(time.Second)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "working", "idle")
}

func TestWorkingSessionStaysWorkingWhileItsTranscriptChanges(t *testing.T) {
	clock := clockAt(recordingStart)
	transcript := &quietTranscript{}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcript))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Transcript: "/t/a.jsonl"})

	for end := clock.Now().Add(3 * host.StaleAfter); clock.Now().Before(end); {
		clock.Advance(time.Second)
		transcript.modified = clock.Now().Add(-30 * time.Second)
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "working")
}

func TestOnlyWorkingSessionsFallBackToIdle(t *testing.T) {
	// Awaiting Input with no hook is a human not answering yet; Done, Error
	// and Idle have nothing to fall back from. A session without a
	// transcript (codino event) can't be judged stale.
	for _, e := range []host.Event{
		{SessionID: "a", State: host.AwaitingInput, Transcript: "/t/a.jsonl"},
		{SessionID: "a", State: host.Done, Transcript: "/t/a.jsonl"},
		{SessionID: "a", State: host.Error, Transcript: "/t/a.jsonl"},
		{SessionID: "a", State: host.Working},
	} {
		t.Run(string(e.State)+" "+e.Transcript, func(t *testing.T) {
			clock := clockAt(recordingStart)
			dev := newReferenceDevice(t)
			// Done decay is another way back to Idle, not under test here.
			h := host.New(clock, devices(dev), host.WithTranscriptProbe(&quietTranscript{modified: clock.Now()}),
				host.WithDoneDecay(3*host.StaleAfter))
			defer h.Close()
			h.Tick()
			apply(t, h, e)

			for end := clock.Now().Add(2 * host.StaleAfter); clock.Now().Before(end); {
				clock.Advance(time.Second)
				h.Tick()
			}

			assertStates(t, sessionStates(t, dev.Received()), string(e.State))
		})
	}
}

func TestHookAfterQuietTranscriptKeepsTheSessionWorking(t *testing.T) {
	// A long tool call writes nothing to the transcript, but its subagents'
	// hooks still arrive.
	clock := clockAt(recordingStart)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(&quietTranscript{modified: clock.Now()}))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Transcript: "/t/a.jsonl"})

	for end := clock.Now().Add(3 * host.StaleAfter); clock.Now().Before(end); {
		clock.Advance(time.Second)
		apply(t, h, host.Event{SessionID: "a", State: host.Working})
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "working")
}

func TestHookThatCouldNotReadTheStartTimeKeepsTheKnownOne(t *testing.T) {
	clock := clockAt(recordingStart)
	liveness := &fakeLiveness{clock: clock, started: map[int]uint64{4242: 9001}}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithLivenessProbe(liveness))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, PID: 4242, PIDStarted: 1234})
	apply(t, h, host.Event{SessionID: "a", State: host.Done, PID: 4242})

	clock.Advance(host.ProbeInterval)
	h.Tick()

	assertStates(t, sessionStates(t, dev.Received()), "working", "done", "removed")
}
