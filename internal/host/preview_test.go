package host_test

import (
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// preview starts a preview, failing the test when the Host refuses.
func preview(t *testing.T, h *host.Host, state host.SessionState, elapsed time.Duration) {
	t.Helper()
	if err := h.Preview(state, elapsed); err != nil {
		t.Fatalf("Preview: %v", err)
	}
}

func TestPreviewForcesTheDeviceIntoASessionState(t *testing.T) {
	h, dev, _ := claimedHost(t)

	preview(t, h, host.AwaitingInput, 150*time.Second)

	assertMessages(t, since(dev, 2), []string{fixture(t, "preview-enter.jsonl")})
}

func TestEndingThePreviewRestoresTheDevice(t *testing.T) {
	h, dev, _ := claimedHost(t)
	preview(t, h, host.Error, 0)

	h.EndPreview()
	h.EndPreview() // nothing more: no preview is on

	assertMessages(t, since(dev, 3), []string{fixture(t, "preview-exit.jsonl")})
}

func TestPreviewRefusesWhatIsNoSessionState(t *testing.T) {
	h, dev, _ := claimedHost(t)

	for _, state := range []host.SessionState{"", "asleep"} {
		if err := h.Preview(state, 0); err == nil {
			t.Errorf("Preview(%q) succeeded", state)
		}
	}
	if err := h.Preview(host.Done, -time.Second); err == nil {
		t.Error("Preview with a negative elapsed time succeeded")
	}
	assertMessages(t, since(dev, 2), nil)
}

func TestADeviceClaimedDuringAPreviewGetsItAfterTheSessions(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	apply(t, h, host.Event{SessionID: fixtureSession, State: host.AwaitingInput})
	preview(t, h, host.AwaitingInput, 140*time.Second)

	clock.Advance(10 * time.Second)
	h.Tick()

	got := dev.Received()
	if len(got) != 4 {
		t.Fatalf("got %q, want hello, preferences, the session and the preview", got)
	}
	assertMessages(t, got[3:], []string{fixture(t, "preview-enter.jsonl")})
}

func TestAPreviewIsSentAgainAfterTheHostWentSilent(t *testing.T) {
	h, dev, clock := claimedHost(t)
	preview(t, h, host.Working, 0)
	n := len(dev.Received())

	clock.Advance(6 * time.Second) // a laptop asleep: the Device took it for Disconnected
	h.Tick()

	got := since(dev, n)
	if len(got) != 2 || !strings.Contains(got[0], `"heartbeat"`) {
		t.Fatalf("got %q, want a heartbeat and the preview", got)
	}
	assertMessages(t, got[1:], []string{`{"type":"preview_enter","state":"working","elapsed_ms":6000}`})
}

func TestAHoldKeepsTheHostDaemonUpWithNoSessions(t *testing.T) {
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices, host.WithExitGrace(time.Minute))
	defer h.Close()

	hold, err := h.Hold()
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	clock.Advance(10 * time.Minute)
	h.Tick()
	if retired(h) {
		t.Fatal("retired while held")
	}

	hold.Release()
	hold.Release() // once is enough
	clock.Advance(59 * time.Second)
	h.Tick()
	if retired(h) {
		t.Fatal("retired before the exit grace after the hold was released")
	}
	clock.Advance(time.Second)
	h.Tick()
	if !retired(h) {
		t.Fatal("not retired an exit grace after the hold was released")
	}
	if _, err := h.Hold(); err != host.ErrRetired {
		t.Fatalf("Hold on a retired Host: got %v, want ErrRetired", err)
	}
}
