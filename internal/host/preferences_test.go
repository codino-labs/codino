package host_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/preferences"
)

// Preferences (#11): the config file is canonical; the Host pushes the
// Preferences to the Device on claiming it and whenever the file changes,
// and applies its own (Done decay, pinned Project Colors).

// fakeConfig is the config file as the Host reads it: tests change it by
// hand, or break it.
type fakeConfig struct {
	mu  sync.Mutex
	p   preferences.Preferences
	err error
}

func newFakeConfig(change func(*preferences.Preferences)) *fakeConfig {
	p := preferences.Defaults()
	if change != nil {
		change(&p)
	}
	return &fakeConfig{p: p}
}

func (c *fakeConfig) Load() (preferences.Preferences, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p, c.err
}

// Change edits the file.
func (c *fakeConfig) Change(change func(*preferences.Preferences)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = nil
	change(&c.p)
}

// Break makes the file invalid.
func (c *fakeConfig) Break(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// withPreferences is a Host whose config file sets what change does.
func withPreferences(change func(*preferences.Preferences)) host.Option {
	return host.WithPreferences(newFakeConfig(change))
}

// fixturePreferences are the Preferences protocol/fixtures/preferences.jsonl
// carries.
func fixturePreferences(p *preferences.Preferences) {
	p.Brightness = 50
	p.Layout = preferences.Aggregate
	p.Rotation = 180
	p.DisconnectedTimeout = 10 * time.Second
	p.Escalation = preferences.Escalation{Faster: 20 * time.Second, Takeover: time.Minute, Steady: 5 * time.Minute}
}

// defaultPreferences is the push of the default Preferences.
const defaultPreferences = `{"type":"preferences","theme":"default","brightness":100,"layout":"adaptive","rotation":0,"quiet_mode":false,"disconnected_timeout_ms":5000,"escalation_faster_ms":30000,"escalation_takeover_ms":120000,"escalation_steady_ms":600000}`

// preferencesPushes returns the preferences messages among lines.
func preferencesPushes(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.Contains(line, `"type":"preferences"`) {
			out = append(out, line)
		}
	}
	return out
}

func TestClaimingADevicePushesThePreferencesBeforeTheSessions(t *testing.T) {
	clock := clockAt(1760432395000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), withPreferences(fixturePreferences))
	defer h.Close()
	apply(t, h, host.Event{SessionID: fixtureSession, State: host.AwaitingInput})
	clock.Advance(5 * time.Second)

	h.Tick()

	assertMessages(t, dev.Received(), []string{
		fixture(t, "hello.jsonl"),
		fixture(t, "preferences.jsonl"),
		fixture(t, "session-upsert.jsonl"),
	})
}

func TestWithoutAConfigFileTheDefaultsArePushed(t *testing.T) {
	_, dev, _ := claimedHost(t)

	assertMessages(t, dev.Received(), []string{fixture(t, "hello.jsonl"), defaultPreferences})
}

func TestAChangedConfigFileReachesTheDeviceWithinAPoll(t *testing.T) {
	config := newFakeConfig(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(config))

	config.Change(func(p *preferences.Preferences) { p.QuietMode = true })
	clock.Advance(host.PreferencesPollInterval - time.Millisecond)
	h.Tick()
	if got := preferencesPushes(since(dev, 2)); len(got) != 0 {
		t.Fatalf("pushed before the next poll: %q", got)
	}
	clock.Advance(time.Millisecond)
	h.Tick()

	want := strings.Replace(defaultPreferences, `"quiet_mode":false`, `"quiet_mode":true`, 1)
	assertMessages(t, preferencesPushes(since(dev, 2)), []string{want})

	// Unchanged, it isn't pushed again.
	for range 5 {
		clock.Advance(host.PreferencesPollInterval)
		h.Tick()
	}
	assertMessages(t, preferencesPushes(since(dev, 2)), []string{want})
}

func TestAChangeOnlyTheHostUsesIsNotPushed(t *testing.T) {
	config := newFakeConfig(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(config))

	config.Change(func(p *preferences.Preferences) {
		p.DoneDecay = time.Minute
		p.ProjectColors = map[string]string{"/src/app": "#123456"}
	})
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	if got := preferencesPushes(since(dev, 2)); len(got) != 0 {
		t.Errorf("pushed %q", got)
	}
}

func TestAnInvalidConfigFileIsReportedAndIgnored(t *testing.T) {
	config := newFakeConfig(func(p *preferences.Preferences) { p.Brightness = 40 })
	h, dev, clock := claimedHost(t, host.WithPreferences(config))

	config.Break(errors.New("config.toml: line 3: brightness: want a percentage"))
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	if got := preferencesPushes(since(dev, 2)); len(got) != 0 {
		t.Errorf("pushed %q for an invalid file", got)
	}
	if got := h.Status().PreferencesProblem; !strings.Contains(got, "line 3: brightness") {
		t.Errorf("Status().PreferencesProblem = %q", got)
	}

	// Fixed, the problem is gone and the change goes through.
	config.Change(func(p *preferences.Preferences) { p.Brightness = 60 })
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	if got := h.Status().PreferencesProblem; got != "" {
		t.Errorf("Status().PreferencesProblem = %q after the fix", got)
	}
	want := strings.Replace(defaultPreferences, `"brightness":100`, `"brightness":60`, 1)
	assertMessages(t, preferencesPushes(since(dev, 2)), []string{want})
}

func TestAConfigFileInvalidFromTheStartLeavesTheDefaults(t *testing.T) {
	config := newFakeConfig(nil)
	config.Break(errors.New("config.toml: line 1: unknown Preference"))

	h, dev, _ := claimedHost(t, host.WithPreferences(config))

	assertMessages(t, dev.Received(), []string{fixture(t, "hello.jsonl"), defaultPreferences})
	if got := h.Status().PreferencesProblem; !strings.Contains(got, "unknown Preference") {
		t.Errorf("Status().PreferencesProblem = %q", got)
	}
}

func TestTheDoneDecayTimeFollowsTheConfigFile(t *testing.T) {
	config := newFakeConfig(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(config))
	config.Change(func(p *preferences.Preferences) { p.DoneDecay = time.Minute })
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()
	apply(t, h, host.Event{SessionID: "a", State: host.Done})

	for range 60 {
		clock.Advance(time.Second)
		h.Tick()
	}

	assertStates(t, sessionStates(t, dev.Received()), "done", "idle")
}

func TestPinningAProjectColorRecolorsTheRunningSessionsOfThatProject(t *testing.T) {
	config := newFakeConfig(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(config), host.WithProjectRoots(fakeRoots{"/src/app-wt": "/src/app"}))
	apply(t, h,
		host.Event{SessionID: "app", State: host.Working, Project: "/src/app-wt"},
		host.Event{SessionID: "other", State: host.Working, Project: "/src/other"},
	)
	sent := len(dev.Received())

	config.Change(func(p *preferences.Preferences) { p.ProjectColors = map[string]string{"/src/app": "#123456"} })
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	got := upserts(t, since(dev, sent))
	if len(got) != 1 || got[0].ID != "app" || got[0].ProjectColor != "#123456" {
		t.Errorf("upserts %+v, want app in #123456", got)
	}
	if colors := colorsOf(t, dev); colors["app"] != "#123456" {
		t.Errorf("colors %v", colors)
	}
}

func TestTheShowWaitsForTheConfiguredDisconnectedTimeoutBeforeEnteringRawModeAgain(t *testing.T) {
	h, dev, clock := claimedHost(t, withPreferences(func(p *preferences.Preferences) {
		p.DisconnectedTimeout = 10 * time.Second
	}))
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	n := len(dev.Received())

	// Silent for longer than the default timeout, not the configured one:
	// the Device is still in Raw Mode.
	clock.Advance(6 * time.Second)
	h.Tick()

	assertLines(t, since(dev, n+1), []string{fixture(t, "raw-frame.bin")})
}

func TestTurnedSidewaysAPictureIsScaledToTheTurnedMatrix(t *testing.T) {
	clock := clockAt(1760432400000)
	wide := newFakeDevice(strings.Replace(strings.Replace(fixture(t, "hello-reply.jsonl"),
		`"width":8`, `"width":4`, 1), `"height":8`, `"height":2`, 1))
	h := host.New(clock, devices(wide), withPreferences(func(p *preferences.Preferences) { p.Rotation = 90 }))
	defer h.Close()
	h.Tick()

	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))

	// 2 wide and 4 tall: the picture as the turned matrix shows it.
	frames := since(wide, 2)
	frame := frames[len(frames)-1]
	if len(frame) != 1+2*4*3 {
		t.Errorf("frame of %d bytes, want %d", len(frame), 1+2*4*3)
	}
}
