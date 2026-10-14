package host_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
)

// The session id used by protocol/fixtures/session-*.jsonl.
const fixtureSession = "2f0c6a1e-5b7d-4c1a-9e8f-3d2b1a0c9e7f"

// fakeClock is the injected clock: tests move time by hand and never sleep.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func clockAt(unixMilli int64) *fakeClock { return &fakeClock{now: time.UnixMilli(unixMilli)} }

// fakeDevice is the fake Device Link transport: it records every protocol line
// the Host writes and answers hello with a scripted reply.
type fakeDevice struct {
	// reply is sent back for every hello, one line per element; the port
	// closes after the last line unless it is a hello_reply.
	reply []string

	mu       sync.Mutex
	received []string
	toHost   chan []byte
	pending  []byte
	closed   chan struct{}
	once     sync.Once

	// hang, when set, makes Close block until it is closed, like a Device
	// that stopped answering; closing is closed when Close is entered.
	hang    chan struct{}
	closing chan struct{}
	// failWrites makes every write fail, like a Device that stopped reading.
	failWrites  atomic.Bool
	closingOnce sync.Once
	// answer, when set, is the Device's reply to each line other than hello
	// ("" for none).
	answer func(line string) string
}

func newFakeDevice(reply ...string) *fakeDevice {
	return &fakeDevice{reply: reply, toHost: make(chan []byte, 16), closed: make(chan struct{})}
}

// newReferenceDevice answers hello like the reference build in the fixtures.
func newReferenceDevice(t *testing.T) *fakeDevice {
	return newFakeDevice(fixture(t, "hello-reply.jsonl"))
}

func (d *fakeDevice) Write(p []byte) (int, error) {
	if d.failWrites.Load() {
		return 0, io.ErrShortWrite
	}
	select {
	case <-d.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(p), "\n"), "\n") {
		d.mu.Lock()
		d.received = append(d.received, line)
		d.mu.Unlock()
		if strings.Contains(line, `"type":"hello"`) {
			for _, r := range d.reply {
				d.toHost <- []byte(r + "\n")
			}
			if n := len(d.reply); n == 0 || !strings.Contains(d.reply[n-1], `"hello_reply"`) {
				d.Close()
			}
		} else if d.answer != nil {
			if reply := d.answer(line); reply != "" {
				d.toHost <- []byte(reply + "\n")
			}
		}
	}
	return len(p), nil
}

func (d *fakeDevice) Read(p []byte) (int, error) {
	if len(d.pending) == 0 {
		select {
		case d.pending = <-d.toHost:
		case <-d.closed:
			return 0, io.EOF
		}
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

func (d *fakeDevice) Close() error {
	d.once.Do(func() { close(d.closed) })
	if d.hang != nil {
		d.closingOnce.Do(func() { close(d.closing) })
		<-d.hang
	}
	return nil
}

// newHungDevice answers the handshake like the reference build, then hangs
// when closed until the returned release is called.
func newHungDevice(t *testing.T) (d *fakeDevice, release func()) {
	d = newReferenceDevice(t)
	d.hang = make(chan struct{})
	d.closing = make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(d.hang) }) }
	t.Cleanup(release)
	return d, release
}

// Received returns the protocol lines the Host sent, in order.
func (d *fakeDevice) Received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.received...)
}

// Unplug simulates the Device going away.
func (d *fakeDevice) Unplug() { d.Close() }

// devices offers the given fake devices as candidates, in order.
func devices(ds ...*fakeDevice) devicelink.Discoverer {
	return devicelink.DiscovererFunc(func() []devicelink.Candidate {
		var cs []devicelink.Candidate
		for i, d := range ds {
			cs = append(cs, devicelink.Candidate{
				Name: "fake" + string(rune('0'+i)),
				Open: func() (io.ReadWriteCloser, error) { return d, nil },
			})
		}
		return cs
	})
}

// noDevices is a Host with nothing plugged in.
var noDevices = devicelink.NoDevices

// fixture reads the single message of a shared protocol fixture.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "fixtures", name))
	if err != nil {
		t.Fatalf("missing fixture: %v", err)
	}
	return string(bytes.TrimRight(data, "\r\n"))
}

// assertMessages compares protocol lines as JSON values, so field order and
// spacing don't matter but every field and value does.
func assertMessages(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range got {
		var g, w any
		if err := json.Unmarshal([]byte(got[i]), &g); err != nil {
			t.Fatalf("message %d is not JSON: %q", i, got[i])
		}
		if err := json.Unmarshal([]byte(want[i]), &w); err != nil {
			t.Fatalf("expected message %d is not JSON: %q", i, want[i])
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("message %d\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
}

func TestHostGreetsTheDeviceWithTheHelloFixture(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()

	h.Tick()

	assertMessages(t, dev.Received(), []string{fixture(t, "hello.jsonl"), defaultPreferences})
}

func TestDeviceClaimedLaterReceivesTheKnownSessions(t *testing.T) {
	clock := clockAt(1760432395000)
	dev := newReferenceDevice(t)
	plugged := false
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		if !plugged {
			return nil
		}
		return devices(dev).Candidates()
	}))
	defer h.Close()

	if err := h.Apply(host.Event{SessionID: fixtureSession, State: host.AwaitingInput}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Second)
	plugged = true
	h.Tick()

	assertMessages(t, dev.Received(), []string{
		fixture(t, "hello.jsonl"),
		defaultPreferences,
		fixture(t, "session-upsert.jsonl"),
	})
}

func TestClaimedDeviceGetsAHeartbeatEverySecond(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()
	clock.Advance(500 * time.Millisecond)
	h.Tick()
	clock.Advance(500 * time.Millisecond)
	h.Tick()

	assertMessages(t, dev.Received(), []string{
		fixture(t, "hello.jsonl"),
		defaultPreferences,
		fixture(t, "heartbeat.jsonl"),
	})
}

func TestEndingASessionRemovesItFromTheDevice(t *testing.T) {
	clock := clockAt(1760432395000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()
	if err := h.Apply(host.Event{SessionID: fixtureSession, State: host.AwaitingInput}); err != nil {
		t.Fatal(err)
	}

	if err := h.Apply(host.Event{SessionID: fixtureSession, End: true}); err != nil {
		t.Fatal(err)
	}

	got := dev.Received()
	assertMessages(t, got[len(got)-1:], []string{fixture(t, "session-remove.jsonl")})
}

// apply applies events, failing the test on a rejected one.
func apply(t *testing.T, h *host.Host, events ...host.Event) {
	t.Helper()
	for _, e := range events {
		if err := h.Apply(e); err != nil {
			t.Fatalf("Apply(%+v): %v", e, err)
		}
	}
}

// since returns the messages received after the first n.
func since(dev *fakeDevice, n int) []string { return dev.Received()[n:] }

func TestOnlyStateChangesReachTheDeviceAndRestartTheStateClock(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()

	apply(t, h, host.Event{SessionID: "a", State: host.Working, Project: "/src/app"})
	clock.Advance(300 * time.Millisecond)
	apply(t, h, host.Event{SessionID: "a", State: host.Working})
	clock.Advance(300 * time.Millisecond)
	apply(t, h, host.Event{SessionID: "a", State: host.Done})

	assertMessages(t, since(dev, 2), []string{
		`{"type":"session_upsert","id":"a","state":"working","project_color":"#ff3030","state_entered":1760432400000,"tile":0}`,
		`{"type":"session_upsert","id":"a","state":"done","project_color":"#ff3030","state_entered":1760432400600,"tile":0}`,
	})
}

func TestSessionsKeepTheirTileAndNewcomersTakeTheFirstFreeOne(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()

	// Four sessions are quadrants; three still are, so nobody moves.
	apply(t, h,
		host.Event{SessionID: "a", State: host.Working},
		host.Event{SessionID: "b", State: host.Working},
		host.Event{SessionID: "c", State: host.Working},
		host.Event{SessionID: "d", State: host.Working},
		host.Event{SessionID: "a", End: true},
		host.Event{SessionID: "c", State: host.Done},
		host.Event{SessionID: "e", State: host.Idle},
	)

	assertMessages(t, since(dev, 2), []string{
		`{"type":"session_upsert","id":"a","state":"working","project_color":"#a040ff","state_entered":1760432400000,"tile":0}`,
		`{"type":"session_upsert","id":"b","state":"working","project_color":"#30e040","state_entered":1760432400000,"tile":1}`,
		`{"type":"session_upsert","id":"c","state":"working","project_color":"#ff3030","state_entered":1760432400000,"tile":2}`,
		`{"type":"session_upsert","id":"d","state":"working","project_color":"#3a86ff","state_entered":1760432400000,"tile":3}`,
		`{"type":"session_remove","id":"a"}`,
		`{"type":"session_upsert","id":"c","state":"done","project_color":"#ff3030","state_entered":1760432400000,"tile":2}`,
		`{"type":"session_upsert","id":"e","state":"idle","project_color":"#e6e600","state_entered":1760432400000,"tile":0}`,
	})
}

func TestHostClaimsOnlyADeviceThatAnswersTheHandshakeEvenAfterBootNoise(t *testing.T) {
	clock := clockAt(1760432400000)
	silent := newFakeDevice()                                          // closes without a reply
	otherBoard := newFakeDevice("ets Jun  8 2016 00:22:57 rst:0x1")    // some other ESP32's boot log
	incomplete := newFakeDevice(`{"type":"hello_reply","protocol":0}`) // no firmware, no Hardware Profile
	reference := newFakeDevice("ESP-ROM:esp32s3-20210327", "", fixture(t, "hello-reply.jsonl"))
	h := host.New(clock, devices(silent, otherBoard, incomplete, reference))
	defer h.Close()
	apply(t, h, host.Event{SessionID: "a", State: host.Working})

	h.Tick()

	for name, d := range map[string]*fakeDevice{"silent": silent, "other board": otherBoard, "incomplete": incomplete} {
		if got := d.Received(); len(got) != 1 {
			t.Errorf("%s got %q, want only the hello", name, got)
		}
	}
	assertMessages(t, since(reference, 2), []string{
		`{"type":"session_upsert","id":"a","state":"working","project_color":"#a040ff","state_entered":1760432400000,"tile":0}`,
	})
	if dev := h.Status().Device; !dev.Claimed || dev.Name != "fake3" || dev.Firmware != "0.1.0" {
		t.Errorf("Status().Device = %+v, want fake3 claimed with firmware 0.1.0", dev)
	}
}

func TestHostRefusesADeviceOnAnotherProtocolVersion(t *testing.T) {
	clock := clockAt(1760432400000)
	newer := newFakeDevice(strings.Replace(fixture(t, "hello-reply.jsonl"), `"protocol":0`, `"protocol":1`, 1))
	h := host.New(clock, devices(newer))
	defer h.Close()
	apply(t, h, host.Event{SessionID: "a", State: host.Working})

	h.Tick()

	assertMessages(t, newer.Received(), []string{fixture(t, "hello.jsonl")})
	dev := h.Status().Device
	if dev.Claimed || !strings.Contains(dev.Problem, "update the firmware") {
		t.Errorf("Status().Device = %+v, want unclaimed with an update-firmware problem", dev)
	}
}

func TestReplugGetsTheHandshakeAndEverySessionAgain(t *testing.T) {
	clock := clockAt(1760432400000)
	first := newReferenceDevice(t)
	plugged := first
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		return devices(plugged).Candidates()
	}))
	defer h.Close()
	h.Tick()
	apply(t, h,
		host.Event{SessionID: "a", State: host.Working},
		host.Event{SessionID: "b", State: host.AwaitingInput},
	)

	first.Unplug()
	plugged = newReferenceDevice(t)
	clock.Advance(time.Second)
	h.Tick()

	assertMessages(t, plugged.Received(), []string{
		`{"type":"hello","protocol":0,"host_time":1760432401000}`,
		defaultPreferences,
		`{"type":"session_upsert","id":"a","state":"working","project_color":"#a040ff","state_entered":1760432400000,"tile":0}`,
		`{"type":"session_upsert","id":"b","state":"awaiting_input","project_color":"#30e040","state_entered":1760432400000,"tile":1}`,
	})
}

func TestAggregateStateIsTheMostUrgentSessionState(t *testing.T) {
	tests := []struct {
		name   string
		states []host.SessionState
		want   host.SessionState
	}{
		{"no sessions", nil, host.Idle},
		{"one idle", []host.SessionState{host.Idle}, host.Idle},
		{"working beats idle", []host.SessionState{host.Idle, host.Working}, host.Working},
		{"done beats working", []host.SessionState{host.Working, host.Done, host.Idle}, host.Done},
		{"error beats done", []host.SessionState{host.Done, host.Error, host.Working}, host.Error},
		{"awaiting input beats everything", []host.SessionState{host.Error, host.AwaitingInput, host.Done, host.Working, host.Idle}, host.AwaitingInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := host.New(clockAt(1760432400000), noDevices)
			defer h.Close()
			for i, s := range tt.states {
				apply(t, h, host.Event{SessionID: string(rune('a' + i)), State: s})
			}
			if got := h.Status().AggregateState; got != tt.want {
				t.Errorf("AggregateState = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusListsSessionsByTile(t *testing.T) {
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices)
	defer h.Close()
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working, Project: "/src/app"})
	clock.Advance(time.Second)
	apply(t, h,
		host.Event{SessionID: "b", State: host.Done},
		host.Event{SessionID: "a", State: host.AwaitingInput},
	)

	st := h.Status()

	want := []host.SessionSummary{
		{ID: "a", State: host.AwaitingInput, StateEntered: time.UnixMilli(1760432401000), Project: "/src/app", ProjectColor: "#ff3030", Tile: 0},
		{ID: "b", State: host.Done, StateEntered: time.UnixMilli(1760432401000), ProjectColor: "#30e040", Tile: 1},
	}
	if !reflect.DeepEqual(st.Sessions, want) {
		t.Errorf("Sessions =\n%+v\nwant\n%+v", st.Sessions, want)
	}
	if st.Device.Claimed || st.Device.Problem != "no Device found" {
		t.Errorf("Device = %+v, want unclaimed: no Device found", st.Device)
	}
}

func TestInvalidEventsAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		event host.Event
	}{
		{"missing session id", host.Event{State: host.Working}},
		{"missing state", host.Event{SessionID: "a"}},
		{"unknown state", host.Event{SessionID: "a", State: "busy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := host.New(clockAt(1760432400000), noDevices)
			defer h.Close()
			if err := h.Apply(tt.event); err == nil {
				t.Fatal("Apply accepted the event")
			}
			if n := len(h.Status().Sessions); n != 0 {
				t.Errorf("%d sessions after a rejected event, want 0", n)
			}
		})
	}
}

func TestAfterAFailedClaimTheHostWaitsBeforeLookingAgain(t *testing.T) {
	clock := clockAt(1760432400000)
	var plugged []*fakeDevice
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		return devices(plugged...).Candidates()
	}))
	defer h.Close()
	h.Tick() // nothing plugged in

	dev := newReferenceDevice(t)
	plugged = append(plugged, dev)
	clock.Advance(host.ClaimRetryInterval - time.Millisecond)
	h.Tick()
	if got := dev.Received(); len(got) != 0 {
		t.Fatalf("looked for a Device before the retry interval: %q", got)
	}
	clock.Advance(time.Millisecond)
	h.Tick()

	if !h.Status().Device.Claimed {
		t.Error("Device not claimed after the retry interval")
	}
}

// retired reports whether the Host has retired, without waiting.
func retired(h *host.Host) bool {
	select {
	case <-h.Retired():
		return true
	default:
		return false
	}
}

func TestHostRetiresAfterTheExitGraceWithNoSessions(t *testing.T) {
	const grace = 5 * time.Minute
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices, host.WithExitGrace(grace))
	defer h.Close()
	apply(t, h, host.Event{SessionID: "a", State: host.Working})
	clock.Advance(time.Hour)
	apply(t, h, host.Event{SessionID: "a", End: true})

	clock.Advance(grace - time.Millisecond)
	h.Tick()
	if retired(h) {
		t.Fatal("retired before the exit grace was over")
	}
	clock.Advance(time.Millisecond)
	h.Tick()

	if !retired(h) {
		t.Error("not retired after the exit grace with no sessions")
	}
}

func TestHostStaysWhileSessionsRunAndTheGraceRestartsWhenTheLastOneEnds(t *testing.T) {
	const grace = time.Minute
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices, host.WithExitGrace(grace))
	defer h.Close()

	clock.Advance(grace - time.Second) // started with no sessions
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Working})
	for range 10 {
		clock.Advance(grace)
		h.Tick()
	}
	apply(t, h, host.Event{SessionID: "a", End: true})
	clock.Advance(grace - time.Second)
	h.Tick()

	if retired(h) {
		t.Error("retired with sessions running or before the grace after the last one ended")
	}
}

func TestHostWithoutAnySessionRetiresAfterTheGraceFromStart(t *testing.T) {
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices)
	defer h.Close()
	apply(t, h, host.Event{SessionID: "never-started", End: true})

	clock.Advance(host.DefaultExitGrace)
	h.Tick()

	if !retired(h) {
		t.Error("not retired after the default exit grace")
	}
}

func TestARetiredHostRefusesEventsSoTheClientSpawnsANewHostDaemon(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), host.WithExitGrace(time.Minute))
	defer h.Close()
	h.Tick()
	clock.Advance(time.Minute)
	h.Tick()
	sent := len(dev.Received())

	err := h.Apply(host.Event{SessionID: "a", State: host.Working})

	if !errors.Is(err, host.ErrRetired) {
		t.Errorf("Apply after retiring: err = %v, want ErrRetired", err)
	}
	if got := since(dev, sent); len(got) != 0 {
		t.Errorf("a retired Host sent %q", got)
	}
	if n := len(h.Status().Sessions); n != 0 {
		t.Errorf("%d sessions after a refused event, want 0", n)
	}
}

// returnsPromptly fails the test when f doesn't return while a Device hangs
// in Close. The bound only catches a deadlock; nothing here sleeps.
func returnsPromptly(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked while a Device was closing", what)
	}
}

func TestClosingAHungDeviceDoesNotHoldUpTheHost(t *testing.T) {
	tests := []struct {
		name string
		drop func(h *host.Host, dev *fakeDevice)
	}{
		{"the Host closes", func(h *host.Host, _ *fakeDevice) { h.Close() }},
		{"a write fails", func(h *host.Host, dev *fakeDevice) {
			dev.failWrites.Store(true)
			h.Apply(host.Event{SessionID: "b", State: host.Done})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := clockAt(1760432400000)
			dev, release := newHungDevice(t)
			h := host.New(clock, devices(dev))
			h.Tick()
			if !h.Status().Device.Claimed {
				t.Fatal("Device not claimed")
			}

			go tt.drop(h, dev)
			<-dev.closing

			returnsPromptly(t, "Status", func() { h.Status() })
			returnsPromptly(t, "Apply", func() {
				if err := h.Apply(host.Event{SessionID: "a", State: host.Working}); err != nil {
					t.Errorf("Apply: %v", err)
				}
			})
			release()
			h.Close()
		})
	}
}
