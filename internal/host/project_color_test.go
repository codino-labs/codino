package host_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/codino-labs/codino/internal/host"
)

// Project Color (#10): a pinned color, else a stable hash of the project's
// repo root onto host.ProjectColors; a collision with an active session of
// another project bumps the newcomer to the next free hue for its lifetime.

// fakeRoots is the injected ProjectRootProbe: the repo root of each project
// directory, the directory itself when it isn't listed (not in a repo).
type fakeRoots map[string]string

func (r fakeRoots) Root(dir string) string {
	if root, ok := r[dir]; ok {
		return root
	}
	return dir
}

// upserted is one session_upsert a fake Device received.
type upserted struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	ProjectColor string `json:"project_color"`
	Tile         int    `json:"tile"`
}

// upserts returns the session_upserts among protocol lines, in order.
func upserts(t *testing.T, lines []string) []upserted {
	t.Helper()
	var out []upserted
	for _, line := range lines {
		var m struct {
			Type string `json:"type"`
			upserted
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		if m.Type == "session_upsert" {
			out = append(out, m.upserted)
		}
	}
	return out
}

// colorsOf returns the latest Project Color the Device was sent for each
// session.
func colorsOf(t *testing.T, dev *fakeDevice) map[string]string {
	t.Helper()
	colors := map[string]string{}
	for _, u := range upserts(t, dev.Received()) {
		colors[u.ID] = u.ProjectColor
	}
	return colors
}

// projectColorHost is a claimed Host for Project Color tests.
func projectColorHost(t *testing.T, opts ...host.Option) (*host.Host, *fakeDevice) {
	t.Helper()
	dev := newReferenceDevice(t)
	h := host.New(clockAt(1760432400000), devices(dev), opts...)
	t.Cleanup(h.Close)
	h.Tick()
	return h, dev
}

// colorAlone is the Project Color a session in project gets on a Host of its
// own: the color its repo root hashes to.
func colorAlone(t *testing.T, project string) string {
	t.Helper()
	h, dev := projectColorHost(t)
	apply(t, h, host.Event{SessionID: "alone", State: host.Working, Project: project})
	return colorsOf(t, dev)["alone"]
}

func TestProjectColorIsAStableHashOfTheRepoRootOntoThePalette(t *testing.T) {
	roots := fakeRoots{
		"/src/app/cmd/server":    "/src/app",
		"/src/app-feature-x":     "/src/app", // a worktree of /src/app
		"/src/app/internal/host": "/src/app",
	}
	h, dev := projectColorHost(t, host.WithProjectRoots(roots))

	apply(t, h,
		host.Event{SessionID: "main", State: host.Working, Project: "/src/app/cmd/server"},
		host.Event{SessionID: "worktree", State: host.Working, Project: "/src/app-feature-x"},
		host.Event{SessionID: "subdir", State: host.Working, Project: "/src/app/internal/host"},
	)

	colors := colorsOf(t, dev)
	if !slices.Contains(host.ProjectColors, colors["main"]) {
		t.Errorf("Project Color %q is not in the palette %q", colors["main"], host.ProjectColors)
	}
	if colors["worktree"] != colors["main"] || colors["subdir"] != colors["main"] {
		t.Errorf("sessions of one repo got different Project Colors: %v", colors)
	}
	// Stable: the same as on a Host of its own, and as in every release
	// (changing the hash would recolor everyone's projects).
	if alone := colorAlone(t, "/src/app"); colors["main"] != alone {
		t.Errorf("Project Color %q, alone %q", colors["main"], alone)
	}
	if got, want := colorAlone(t, "/src/app"), "#ff3030"; got != want {
		t.Errorf("/src/app hashes to %q, want %q", got, want)
	}
}

func TestPaletteHasAboutEightWellSeparatedHues(t *testing.T) {
	if n := len(host.ProjectColors); n < 6 || n > 10 {
		t.Fatalf("%d hues, want about 8", n)
	}
	for i, a := range host.ProjectColors {
		for _, b := range host.ProjectColors[i+1:] {
			if d := hueDistance(t, a, b); d < 25 {
				t.Errorf("%s and %s are only %.0f° apart", a, b, d)
			}
		}
	}
}

// collidingProjects returns two projects whose repo roots hash to the same
// hue, found by asking fresh Hosts.
func collidingProjects(t *testing.T) (string, string) {
	t.Helper()
	seen := map[string]string{}
	for i := 0; i < 100; i++ {
		project := fmt.Sprintf("/src/project-%d", i)
		color := colorAlone(t, project)
		if other, ok := seen[color]; ok {
			return other, project
		}
		seen[color] = project
	}
	t.Fatal("no two projects share a hue")
	return "", ""
}

func TestACollisionBumpsTheNewcomerToTheNextFreeHueForItsLifetime(t *testing.T) {
	first, second := collidingProjects(t)
	hashed := colorAlone(t, second)
	h, dev := projectColorHost(t)

	apply(t, h,
		host.Event{SessionID: "first", State: host.Working, Project: first},
		host.Event{SessionID: "second", State: host.Working, Project: second},
	)

	colors := colorsOf(t, dev)
	next := host.ProjectColors[(slices.Index(host.ProjectColors, hashed)+1)%len(host.ProjectColors)]
	if colors["second"] != next {
		t.Errorf("newcomer got %q, want the next hue %q after its own %q", colors["second"], next, hashed)
	}

	// Its lifetime: the hue freed by the first session doesn't take it back,
	// and a later session of its project shares it.
	apply(t, h,
		host.Event{SessionID: "first", End: true},
		host.Event{SessionID: "second", State: host.Done},
		host.Event{SessionID: "second-again", State: host.Working, Project: second},
	)
	colors = colorsOf(t, dev)
	if colors["second"] != next || colors["second-again"] != next {
		t.Errorf("after the first session ended: %v, want %q for both", colors, next)
	}
}

func TestACollisionSkipsEveryHueAnActiveSessionHolds(t *testing.T) {
	first, second := collidingProjects(t)
	hashed := colorAlone(t, second)
	next := host.ProjectColors[(slices.Index(host.ProjectColors, hashed)+1)%len(host.ProjectColors)]
	// A project pinned to the next hue holds it too.
	h, dev := projectColorHost(t, host.WithPinnedProjectColors(map[string]string{"/src/pinned": next}))

	apply(t, h,
		host.Event{SessionID: "first", State: host.Working, Project: first},
		host.Event{SessionID: "pinned", State: host.Working, Project: "/src/pinned"},
		host.Event{SessionID: "second", State: host.Working, Project: second},
	)

	colors := colorsOf(t, dev)
	if colors["second"] == hashed || colors["second"] == next || !slices.Contains(host.ProjectColors, colors["second"]) {
		t.Errorf("newcomer got %q; %q and %q are held", colors["second"], hashed, next)
	}
}

func TestAPinnedProjectColorWinsOverTheHash(t *testing.T) {
	roots := fakeRoots{"/src/app-feature-x": "/src/app"}
	h, dev := projectColorHost(t,
		host.WithProjectRoots(roots),
		host.WithPinnedProjectColors(map[string]string{"/src/app": "#123456"}))

	apply(t, h, host.Event{SessionID: "worktree", State: host.Working, Project: "/src/app-feature-x"})

	if got := colorsOf(t, dev)["worktree"]; got != "#123456" {
		t.Errorf("Project Color %q, want the pinned #123456", got)
	}
}

func TestEveryHueTakenSharesTheHashedOne(t *testing.T) {
	h, dev := projectColorHost(t)
	for i := range len(host.ProjectColors) + 1 {
		apply(t, h, host.Event{SessionID: fmt.Sprint(i), State: host.Working, Project: fmt.Sprintf("/src/p%d", i)})
	}

	colors := colorsOf(t, dev)
	held := map[string]bool{}
	for i := range len(host.ProjectColors) {
		held[colors[fmt.Sprint(i)]] = true
	}
	if len(held) != len(host.ProjectColors) {
		t.Errorf("the first %d projects got %d hues, want all of them", len(host.ProjectColors), len(held))
	}
	last := fmt.Sprintf("/src/p%d", len(host.ProjectColors))
	if got, want := colors[fmt.Sprint(len(host.ProjectColors))], colorAlone(t, last); got != want {
		t.Errorf("with every hue held the newcomer got %q, want its own %q", got, want)
	}
}

func TestASessionThatLearnsItsProjectLaterGetsItsProjectColor(t *testing.T) {
	h, dev := projectColorHost(t)

	apply(t, h,
		host.Event{SessionID: "a", State: host.Working},
		host.Event{SessionID: "a", State: host.Done, Project: "/src/app"},
	)

	if got, want := colorsOf(t, dev)["a"], colorAlone(t, "/src/app"); got != want {
		t.Errorf("Project Color %q, want %q", got, want)
	}
}

// hueDistance is how far apart two "#rrggbb" colors are on the hue circle,
// in degrees.
func hueDistance(t *testing.T, a, b string) float64 {
	t.Helper()
	d := hue(t, a) - hue(t, b)
	if d < 0 {
		d = -d
	}
	return min(d, 360-d)
}

func hue(t *testing.T, color string) float64 {
	t.Helper()
	var r, g, b int
	if _, err := fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("not a color: %q", color)
	}
	hi, lo := max(r, g, b), min(r, g, b)
	if hi == lo {
		return 0
	}
	c := float64(hi - lo)
	var h float64
	switch hi {
	case r:
		h = float64(g-b) / c
	case g:
		h = float64(b-r)/c + 2
	default:
		h = float64(r-g)/c + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h
}
