package host

import "hash/fnv"

// ProjectColors is the palette the Project Color Allocator hashes repo roots
// onto: well-separated hues, so two projects are easy to tell apart on the
// matrix.
func ProjectColors() []string { return append([]string(nil), projectColors...) }

var projectColors = []string{
	"#ff3030", // red
	"#ff8c00", // orange
	"#e6e600", // yellow
	"#30e040", // green
	"#00d0d0", // cyan
	"#3a86ff", // blue
	"#a040ff", // violet
	"#ff40c0", // pink
}

// ProjectRootProbe finds the root a session's project directory is colored
// by: its git repo root, shared by every worktree of the repo, or the
// directory itself when it isn't in a repo.
type ProjectRootProbe interface {
	Root(dir string) string
}

// WithProjectRoots sets how the Host finds a project's repo root (default:
// the project directory itself).
func WithProjectRoots(p ProjectRootProbe) Option {
	return func(h *Host) { h.roots = p }
}

// WithPinnedProjectColors pins a Project Color ("#rrggbb") to the sessions of
// a repo root, whatever the hash says. Preferences carry the pins (#11).
func WithPinnedProjectColors(pins map[string]string) Option {
	return func(h *Host) { h.sessions.pin(pins) }
}

// pin sets the pinned Project Colors, by repo root.
func (t *tracker) pin(pins map[string]string) {
	t.pins = map[string]string{}
	for root, color := range pins {
		t.pins[root] = color
	}
}

// sameDir is the default ProjectRootProbe: no repo lookup.
type sameDir struct{}

func (sameDir) Root(dir string) string { return dir }

// projectColor is the Project Color Allocator: the Project Color for s, a
// session of the project at root. A pinned color wins; then the color of an
// active session of the same project; then the hue root hashes to, or the
// next one no active session holds. s keeps what it gets for its lifetime.
func (t *tracker) projectColor(s *session, root string) string {
	if pinned, ok := t.pins[root]; ok {
		return pinned
	}
	held := map[string]bool{}
	for _, other := range t.sessions {
		if other == s {
			continue
		}
		if other.root == root {
			return other.projectColor
		}
		held[other.projectColor] = true
	}
	hash := fnv.New32a()
	hash.Write([]byte(root))
	first := int(hash.Sum32() % uint32(len(projectColors)))
	for i := range projectColors {
		if color := projectColors[(first+i)%len(projectColors)]; !held[color] {
			return color
		}
	}
	return projectColors[first]
}
