package host_test

import (
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// An approved permission prompt fires no hook until the tool finishes
// (docs/spikes/hooks.md): the transcript probe sees the tool start running.

func TestAnApprovedToolStartingTakesTheSessionBackToWorking(t *testing.T) {
	clock := clockAt(recordingStart)
	transcripts := &fakeTranscripts{clock: clock}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcripts))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.AwaitingInput, Transcript: "/t/a.jsonl"})
	clock.Advance(4 * time.Second)
	transcripts.toolStarted = clock.Now() // the human approves a 3-minute make test

	for range 3 * 60 {
		clock.Advance(time.Second)
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "awaiting_input", "working")
	if got := h.Status().Sessions[0]; got.State != host.Working || !got.StateEntered.Equal(transcripts.toolStarted) {
		t.Fatalf("session %s since %v, want working since the tool started at %v", got.State, got.StateEntered, transcripts.toolStarted)
	}
}

func TestAToolThatStartedBeforeThePermissionPromptLeavesTheSessionAwaitingInput(t *testing.T) {
	// A background task already running is no approval.
	clock := clockAt(recordingStart)
	transcripts := &fakeTranscripts{clock: clock, toolStarted: clock.Now()}
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcripts))
	defer h.Close()
	h.Tick()
	clock.Advance(time.Second)
	apply(t, h, host.Event{SessionID: "a", State: host.AwaitingInput, Transcript: "/t/a.jsonl"})

	for range 10 {
		clock.Advance(time.Second)
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "awaiting_input")
}

func TestAToolStartingChangesOnlyASessionAwaitingInput(t *testing.T) {
	for _, state := range []host.SessionState{host.Working, host.Done, host.Idle, host.Error} {
		t.Run(string(state), func(t *testing.T) {
			clock := clockAt(recordingStart)
			transcripts := &fakeTranscripts{clock: clock}
			dev := newReferenceDevice(t)
			h := host.New(clock, devices(dev), host.WithTranscriptProbe(transcripts))
			defer h.Close()
			h.Tick()
			apply(t, h, host.Event{SessionID: "a", State: state, Transcript: "/t/a.jsonl"})
			clock.Advance(time.Second)
			transcripts.toolStarted = clock.Now()

			for range 5 {
				clock.Advance(time.Second)
				h.Tick()
			}

			assertStates(t, sessionStates(t, dev.Received()), string(state))
		})
	}
}
