package host_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
)

// Done decay and Acknowledge (#9). Escalation itself is rendered by the
// Device; the Host keeps when each session was last Acknowledged and tells
// the Device.

func TestDoneDecaysToIdleAfterTheDoneDecayTime(t *testing.T) {
	tests := []struct {
		name    string
		opts    []host.Option
		elapsed time.Duration
		want    []string
	}{
		{"default, not yet", nil, host.DefaultDoneDecay - time.Millisecond, []string{"done"}},
		{"default, decayed", nil, host.DefaultDoneDecay, []string{"done", "idle"}},
		{"configured, not yet", []host.Option{host.WithDoneDecay(time.Minute)}, time.Minute - time.Millisecond, []string{"done"}},
		{"configured, decayed", []host.Option{host.WithDoneDecay(time.Minute)}, time.Minute, []string{"done", "idle"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := clockAt(1760432400000)
			dev := newReferenceDevice(t)
			h := host.New(clock, devices(dev), tt.opts...)
			defer h.Close()
			h.Tick()
			apply(t, h, host.Event{SessionID: "a", State: host.Done})

			for elapsed := time.Duration(0); elapsed < tt.elapsed; {
				step := min(time.Second, tt.elapsed-elapsed)
				clock.Advance(step)
				elapsed += step
				h.Tick()
			}

			assertStates(t, sessionStates(t, dev.Received()), tt.want...)
		})
	}
}

func TestDoneDecayStampsIdleWhenItHappened(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithDoneDecay(time.Minute))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working}, host.Event{SessionID: "b", State: host.Done})
	sent := len(dev.Received())

	clock.Advance(time.Minute)
	h.Tick()

	assertMessages(t, withoutHeartbeats(since(dev, sent)), []string{
		`{"type":"session_upsert","id":"b","state":"idle","project_color":"#3a86ff","state_entered":1760432460000,"tile":1}`,
	})
}

// withoutHeartbeats drops the heartbeats from protocol lines.
func withoutHeartbeats(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !strings.Contains(line, `"type":"heartbeat"`) {
			out = append(out, line)
		}
	}
	return out
}

func TestAnAcknowledgeFromTheHostSendsTheAcknowledgeFixture(t *testing.T) {
	clock := clockAt(1760432395000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: fixtureSession, State: host.AwaitingInput})
	clock.Advance(15 * time.Second)
	sent := len(dev.Received())

	apply(t, h, host.Event{SessionID: fixtureSession, Acknowledge: true})

	assertMessages(t, withoutHeartbeats(since(dev, sent)), []string{fixture(t, "acknowledge.jsonl")})
}

func TestAnAcknowledgeOfOneSessionAffectsOnlyThatSession(t *testing.T) {
	tests := []struct {
		name  string
		event host.Event
		want  []string
	}{
		{"Done is cleared", host.Event{SessionID: "done", Acknowledge: true}, []string{
			`{"type":"session_upsert","id":"done","state":"idle","project_color":"#3a86ff","state_entered":1760432460000,"tile":0}`,
		}},
		{"Escalation restarts and Awaiting Input stays", host.Event{SessionID: "waiting", Acknowledge: true}, []string{
			`{"type":"acknowledge","id":"waiting","host_time":1760432460000}`,
		}},
		{"a new prompt makes it Working", host.Event{SessionID: "done", State: host.Working, Acknowledge: true}, []string{
			`{"type":"session_upsert","id":"done","state":"working","project_color":"#3a86ff","state_entered":1760432460000,"tile":0}`,
		}},
		{"a new prompt to a waiting session", host.Event{SessionID: "waiting", State: host.Working, Acknowledge: true}, []string{
			`{"type":"session_upsert","id":"waiting","state":"working","project_color":"#3a86ff","state_entered":1760432460000,"tile":1}`,
		}},
		{"an Idle session has nothing to clear", host.Event{SessionID: "idle", Acknowledge: true}, nil},
		{"an unknown session is not created", host.Event{SessionID: "unknown", Acknowledge: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := clockAt(1760432400000)
			dev := newReferenceDevice(t)
			h := host.New(clock, devices(dev))
			defer h.Close()
			h.Tick()
			apply(t, h,
				host.Event{SessionID: "done", State: host.Done},
				host.Event{SessionID: "waiting", State: host.AwaitingInput},
				host.Event{SessionID: "idle", State: host.Idle},
				host.Event{SessionID: "other-done", State: host.Done},
				host.Event{SessionID: "other-waiting", State: host.AwaitingInput},
			)
			clock.Advance(time.Minute)
			sent := len(dev.Received())

			apply(t, h, tt.event)

			assertMessages(t, withoutHeartbeats(since(dev, sent)), tt.want)
			if n := len(h.Status().Sessions); n != 5 {
				t.Errorf("%d sessions, want 5", n)
			}
		})
	}
}

func TestADeviceClaimedAgainLearnsWhenEachWaitingSessionWasAcknowledged(t *testing.T) {
	clock := clockAt(1760432400000)
	first := newReferenceDevice(t)
	plugged := first
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		return devices(plugged).Candidates()
	}))
	defer h.Close()
	h.Tick()
	apply(t, h,
		host.Event{SessionID: "a", State: host.AwaitingInput},
		host.Event{SessionID: "b", State: host.AwaitingInput},
	)
	clock.Advance(time.Minute)
	apply(t, h, host.Event{SessionID: "a", Acknowledge: true})

	first.Unplug()
	plugged = newReferenceDevice(t)
	clock.Advance(time.Second)
	h.Tick()

	assertMessages(t, plugged.Received(), []string{
		`{"type":"hello","protocol":0,"host_time":1760432461000}`,
		`{"type":"session_upsert","id":"a","state":"awaiting_input","project_color":"#3a86ff","state_entered":1760432400000,"tile":0}`,
		`{"type":"acknowledge","id":"a","host_time":1760432460000}`,
		`{"type":"session_upsert","id":"b","state":"awaiting_input","project_color":"#3a86ff","state_entered":1760432400000,"tile":1}`,
	})
}

// acknowledgeOnDevice makes the fake Device send its Acknowledge fixture and
// waits until the Host has sent want more lines in response.
func acknowledgeOnDevice(t *testing.T, dev *fakeDevice, want int) {
	t.Helper()
	sent := len(dev.Received())
	dev.toHost <- []byte(fixture(t, "device-acknowledge.jsonl") + "\n")
	waitFor(t, func() bool { return len(dev.Received()) >= sent+want })
}

// waitFor waits for the Host to react to something the Device sent, on the
// Device Link's own goroutine. The bound only catches a Host that never does.
func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("the Host did not react to the Device")
		}
		runtime.Gosched()
	}
}

func TestAnAcknowledgeOnTheDeviceClearsEveryDoneSessionAndRestartsEveryEscalation(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	plugged := dev
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		return devices(plugged).Candidates()
	}))
	defer h.Close()
	h.Tick()
	apply(t, h,
		host.Event{SessionID: "a", State: host.Done},
		host.Event{SessionID: "b", State: host.AwaitingInput},
		host.Event{SessionID: "c", State: host.Done},
		host.Event{SessionID: "d", State: host.Working},
	)
	clock.Advance(time.Minute)
	sent := len(dev.Received())

	acknowledgeOnDevice(t, dev, 2)

	// The Device restarted Escalation itself: no acknowledge goes back.
	assertMessages(t, since(dev, sent), []string{
		`{"type":"session_upsert","id":"a","state":"idle","project_color":"#3a86ff","state_entered":1760432460000,"tile":0}`,
		`{"type":"session_upsert","id":"c","state":"idle","project_color":"#3a86ff","state_entered":1760432460000,"tile":2}`,
	})
	// But the Host keeps it, for a Device claimed again.
	dev.Unplug()
	plugged = newReferenceDevice(t)
	clock.Advance(time.Second)
	h.Tick()
	assertMessages(t, since(plugged, 3), []string{
		`{"type":"acknowledge","id":"b","host_time":1760432460000}`,
		`{"type":"session_upsert","id":"c","state":"idle","project_color":"#3a86ff","state_entered":1760432460000,"tile":2}`,
		`{"type":"session_upsert","id":"d","state":"working","project_color":"#3a86ff","state_entered":1760432400000,"tile":3}`,
	})
}
