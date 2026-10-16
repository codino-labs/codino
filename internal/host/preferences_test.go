package host_test

import (
	"errors"
	"maps"
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

// fakePreferencesFile is the config file as the Host reads it: tests change it by
// hand, or break it.
type fakePreferencesFile struct {
	mu  sync.Mutex
	p   preferences.Preferences
	err error
}

func newFakePreferencesFile(change func(*preferences.Preferences)) *fakePreferencesFile {
	p := preferences.Defaults()
	if change != nil {
		change(&p)
	}
	return &fakePreferencesFile{p: p}
}

func (c *fakePreferencesFile) Load() (preferences.Preferences, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.p
	p.ProjectColors = maps.Clone(p.ProjectColors) // the Host's from now on
	return p, c.err
}

// Change edits the file.
func (c *fakePreferencesFile) Change(change func(*preferences.Preferences)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = nil
	change(&c.p)
}

// Break makes the file invalid.
func (c *fakePreferencesFile) Break(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// withPreferences is a Host whose config file sets what change does.
func withPreferences(change func(*preferences.Preferences)) host.Option {
	return host.WithPreferences(newFakePreferencesFile(change))
}

// fixturePreferences are the Preferences protocol/fixtures/preferences.jsonl
// carries.
func fixturePreferences(p *preferences.Preferences) {
	p.Theme = "minimal"
	p.Brightness = 50
	p.Layout = preferences.Aggregate
	p.Rotation = 180
	p.DisconnectedTimeout = 10 * time.Second
	p.Escalation = preferences.Escalation{Faster: 20 * time.Second, Takeover: time.Minute, Steady: 5 * time.Minute}
}

// defaultPreferences is the push of the default Preferences.
const defaultPreferences = `{"type":"preferences","theme":"default","brightness":100,"layout":"adaptive","quiet_mode":false,"disconnected_timeout_ms":5000,"escalation_faster_ms":30000,"escalation_takeover_ms":120000,"escalation_steady_ms":600000}`

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
	file := newFakePreferencesFile(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(file))

	file.Change(func(p *preferences.Preferences) { p.QuietMode = true })
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
	file := newFakePreferencesFile(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(file))

	file.Change(func(p *preferences.Preferences) {
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
	file := newFakePreferencesFile(func(p *preferences.Preferences) { p.Brightness = 40 })
	h, dev, clock := claimedHost(t, host.WithPreferences(file))

	file.Break(errors.New("config.toml: line 3: brightness: want a percentage"))
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	if got := preferencesPushes(since(dev, 2)); len(got) != 0 {
		t.Errorf("pushed %q for an invalid file", got)
	}
	if got := h.Status().PreferencesProblem; !strings.Contains(got, "line 3: brightness") {
		t.Errorf("Status().PreferencesProblem = %q", got)
	}

	// Fixed, the problem is gone and the change goes through.
	file.Change(func(p *preferences.Preferences) { p.Brightness = 60 })
	clock.Advance(host.PreferencesPollInterval)
	h.Tick()

	if got := h.Status().PreferencesProblem; got != "" {
		t.Errorf("Status().PreferencesProblem = %q after the fix", got)
	}
	want := strings.Replace(defaultPreferences, `"brightness":100`, `"brightness":60`, 1)
	assertMessages(t, preferencesPushes(since(dev, 2)), []string{want})
}

func TestAConfigFileInvalidFromTheStartLeavesTheDefaults(t *testing.T) {
	file := newFakePreferencesFile(nil)
	file.Break(errors.New("config.toml: line 1: unknown Preference"))

	h, dev, _ := claimedHost(t, host.WithPreferences(file))

	assertMessages(t, dev.Received(), []string{fixture(t, "hello.jsonl"), defaultPreferences})
	if got := h.Status().PreferencesProblem; !strings.Contains(got, "unknown Preference") {
		t.Errorf("Status().PreferencesProblem = %q", got)
	}
}

func TestTheDoneDecayTimeFollowsTheConfigFile(t *testing.T) {
	file := newFakePreferencesFile(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(file))
	file.Change(func(p *preferences.Preferences) { p.DoneDecay = time.Minute })
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
	file := newFakePreferencesFile(nil)
	h, dev, clock := claimedHost(t, host.WithPreferences(file), host.WithProjectRoots(fakeRoots{"/src/app-wt": "/src/app"}))
	apply(t, h,
		host.Event{SessionID: "app", State: host.Working, Project: "/src/app-wt"},
		host.Event{SessionID: "other", State: host.Working, Project: "/src/other"},
	)
	sent := len(dev.Received())

	file.Change(func(p *preferences.Preferences) { p.ProjectColors = map[string]string{"/src/app": "#123456"} })
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

func TestTheShowWaitsForTheDisconnectedTimeoutPreferenceBeforeEnteringRawModeAgain(t *testing.T) {
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

// helloReplyWith is the hello_reply fixture with each old, new pair of
// strings replaced.
func helloReplyWith(t *testing.T, oldNew ...string) string {
	t.Helper()
	return strings.NewReplacer(oldNew...).Replace(fixture(t, "hello-reply.jsonl"))
}

// lastFrameOn is the last Raw Mode frame a Device of 4 x 2 LEDs, whose
// hello_reply ends with `orientation`, gets when the rotation Preference is
// `rotation` and the Host shows the fixture picture.
func lastFrameOn(t *testing.T, rotation int, orientation string) string {
	t.Helper()
	wide := newFakeDevice(helloReplyWith(t, `"width":8`, `"width":4`, `"height":8`, `"height":2`, `"orientation":0`, orientation))
	h := host.New(clockAt(1760432400000), devices(wide), withPreferences(func(p *preferences.Preferences) { p.Rotation = rotation }))
	defer h.Close()
	h.Tick()
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	frames := since(wide, 2)
	return frames[len(frames)-1]
}

func TestUnderAutoRotationAPictureIsScaledToTheOrientationTheDeviceReports(t *testing.T) {
	turned := lastFrameOn(t, 90, `"orientation":0`)
	if upright := lastFrameOn(t, 0, `"orientation":0`); upright == turned {
		t.Fatal("the fixture picture looks the same turned: it can't tell rotations apart")
	}

	if got := lastFrameOn(t, preferences.AutoRotation, `"orientation":90`); got != turned {
		t.Errorf("frame %q, want the one turned by 90 %q", got, turned)
	}
}

func TestUnderAutoRotationTurningTheDeviceSidewaysRescalesThePicture(t *testing.T) {
	turned := lastFrameOn(t, 90, `"orientation":0`)
	wide := newFakeDevice(helloReplyWith(t, `"width":8`, `"width":4`, `"height":8`, `"height":2`))
	h := host.New(clockAt(1760432400000), devices(wide))
	defer h.Close()
	h.Tick()
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	sent := len(wide.Received())

	wide.toHost <- []byte(fixture(t, "orientation.jsonl") + "\n")

	waitFor(t, func() bool { return len(wide.Received()) > sent })
	frames := since(wide, sent)
	if got := frames[len(frames)-1]; got != turned {
		t.Errorf("frame %q, want the one turned by 90 %q", got, turned)
	}
}

func TestTheStatusSaysWhichWayUpTheDeviceStands(t *testing.T) {
	h, dev, _ := claimedHost(t)
	if o := h.Status().Device.Orientation; o == nil || *o != 0 {
		t.Fatalf("orientation %v after the hello_reply, want 0", o)
	}

	dev.toHost <- []byte(fixture(t, "orientation.jsonl") + "\n")

	waitFor(t, func() bool { o := h.Status().Device.Orientation; return o != nil && *o == 90 })
}

func TestTheStatusOfADeviceWithoutAnIMUHasNoOrientation(t *testing.T) {
	plain := newFakeDevice(helloReplyWith(t, `"imu":true`, `"imu":false`, `,"orientation":0`, ""))
	h := host.New(clockAt(1760432400000), devices(plain))
	defer h.Close()
	h.Tick()

	if o := h.Status().Device.Orientation; o != nil {
		t.Errorf("orientation %d", *o)
	}
}
