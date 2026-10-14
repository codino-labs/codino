package host_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/protocol"
)

// watched collects what a Watcher sees: the profiles of the Devices it
// starts and the lines it is sent.
type watched struct {
	profiles []protocol.HardwareProfile
	lines    []string
}

// drain reads what w has seen so far, without waiting for more.
func drain(t *testing.T, w *host.Watcher) watched {
	t.Helper()
	var got watched
	for {
		select {
		case m, ok := <-w.Mirrored():
			if !ok {
				return got
			}
			if m.Device != nil {
				got.profiles = append(got.profiles, *m.Device)
			} else {
				got.lines = append(got.lines, string(m.Line))
			}
		default:
			return got
		}
	}
}

// watch starts watching h, closing the Watcher when the test ends.
func watch(t *testing.T, h *host.Host) *host.Watcher {
	t.Helper()
	w := h.Watch()
	t.Cleanup(w.Close)
	return w
}

// referenceProfile is the Hardware Profile of hello-reply.jsonl.
var referenceProfile = protocol.HardwareProfile{Width: 8, Height: 8, DataPin: 14, ColorOrder: "RGB",
	BrightnessCap: 48, CurrentCapMA: 300, IMU: true}

func TestAWatcherStartsLikeTheClaimedDeviceThenGetsWhatItIsSent(t *testing.T) {
	h, dev, clock := claimedHost(t)
	apply(t, h, host.Event{SessionID: fixtureSession, State: host.AwaitingInput})

	w := watch(t, h)
	start := drain(t, w)
	n := len(dev.Received())
	clock.Advance(time.Second)
	h.Tick()
	apply(t, h, host.Event{SessionID: "b", State: host.Working})
	preview(t, h, host.Done, 0)

	if !reflect.DeepEqual(start.profiles, []protocol.HardwareProfile{referenceProfile}) {
		t.Errorf("profiles: got %+v, want the claimed Device's", start.profiles)
	}
	assertLines(t, start.lines, dev.Received()[:n]) // hello, the Preferences, the session
	then := drain(t, w)
	if len(then.profiles) != 0 {
		t.Errorf("a new Device without a new claim: %+v", then.profiles)
	}
	assertLines(t, then.lines, since(dev, n))
}

func TestWithoutADeviceAWatcherGetsTheReferenceMatrixAndHeartbeats(t *testing.T) {
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices)
	defer h.Close()

	w := watch(t, h)
	clock.Advance(time.Second)
	h.Tick()

	got := drain(t, w)
	if len(got.profiles) != 1 || got.profiles[0].Width != 8 || got.profiles[0].Height != 8 {
		t.Fatalf("profiles: got %+v, want one 8x8 matrix", got.profiles)
	}
	if n := len(got.lines); n != 3 || !strings.Contains(got.lines[2], `"heartbeat"`) {
		t.Fatalf("got %q, want hello, the Preferences and a heartbeat", got.lines)
	}
}

func TestAWatcherStartsAgainWhenADeviceIsClaimed(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	var plugged atomic.Bool
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		if !plugged.Load() {
			return nil
		}
		return devices(dev).Candidates()
	}))
	defer h.Close()
	w := watch(t, h)
	drain(t, w)

	plugged.Store(true)
	h.Tick()

	got := drain(t, w)
	if !reflect.DeepEqual(got.profiles, []protocol.HardwareProfile{referenceProfile}) {
		t.Fatalf("profiles: got %+v, want the claimed Device's", got.profiles)
	}
	if len(got.lines) < 2 {
		t.Fatalf("got %q, want hello and the Preferences last", got.lines)
	}
	// After the heartbeat that went before the claim.
	assertMessages(t, got.lines[len(got.lines)-2:], []string{fixture(t, "hello.jsonl"), defaultPreferences})
}

func TestAWatcherGetsTheInstalledThemesTheHostHasCopiesOf(t *testing.T) {
	copies := &fakeCopies{}
	dev := newReferenceDevice(t)
	dev.answer = func(line string) string {
		if strings.Contains(line, `"install"`) {
			return fixture(t, "install-reply.jsonl")
		}
		return ""
	}
	h := host.New(clockAt(1760432400000), devices(dev), host.WithThemeCopies(copies))
	defer h.Close()
	h.Tick()
	if _, err := h.Install(context.Background(), installOf(t, "install-theme.jsonl")); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got := drain(t, watch(t, h))

	if len(got.lines) < 2 {
		t.Fatalf("got %q", got.lines)
	}
	assertMessages(t, got.lines[:2], []string{fixture(t, "install-theme.jsonl"), fixture(t, "hello.jsonl")})
	if len(copies.saved) != 1 {
		t.Errorf("copies saved: %d, want 1", len(copies.saved))
	}
}

func TestAClosedWatcherSeesNothingMore(t *testing.T) {
	h, _, _ := claimedHost(t)
	w := h.Watch()
	drain(t, w)

	w.Close()
	apply(t, h, host.Event{SessionID: "a", State: host.Working})

	if _, ok := <-w.Mirrored(); ok {
		t.Fatal("a closed Watcher still sees lines")
	}
}

// fakeCopies keeps the Host's copies of installed Themes and Sprite Packs in
// memory.
type fakeCopies struct {
	mu    sync.Mutex
	saved []protocol.Install
}

func (c *fakeCopies) Load() []protocol.Install {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.Install(nil), c.saved...)
}

func (c *fakeCopies) Save(install protocol.Install) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Drop(install.Kind, install.Name)
	c.saved = append(c.saved, install)
	return nil
}

func (c *fakeCopies) Remove(kind protocol.Kind, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Drop(kind, name)
	return nil
}

// Drop forgets a copy. Callers hold c.mu.
func (c *fakeCopies) Drop(kind protocol.Kind, name string) {
	for i, s := range c.saved {
		if s.Kind == kind && s.Name == name {
			c.saved = append(c.saved[:i], c.saved[i+1:]...)
			return
		}
	}
}
