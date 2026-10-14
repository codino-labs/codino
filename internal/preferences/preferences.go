// Package preferences is the Preferences Store: the user's Preferences in a
// config file, which is canonical. It loads and validates the file, and
// `codino get/set` (and the `/codino` slash command through them) read and
// edit it; the Host Daemon watches it and pushes the Preferences to the
// Device.
package preferences

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Layout is how the matrix area is used.
type Layout string

const (
	// Adaptive gives one Tile per Agent Session.
	Adaptive Layout = "adaptive"
	// Aggregate shows the Aggregate State on the whole matrix.
	Aggregate Layout = "aggregate"
)

// Escalation is when each step of Escalation starts, counted from when a
// session entered Awaiting Input or was last Acknowledged.
type Escalation struct {
	// Faster: the gentle pulse turns faster and brighter.
	Faster time.Duration
	// Takeover: the waiting session takes the whole matrix over.
	Takeover time.Duration
	// Steady: Escalation settles into a steady glow.
	Steady time.Duration
}

// Preferences are the user's choices about how states look and behave.
type Preferences struct {
	// Theme is the Theme's name. Only "default" until Themes land (#12).
	Theme string
	// Brightness is a percentage (1-100) of the Hardware Profile's
	// brightness cap, which the Device enforces whatever this says.
	Brightness int
	Layout     Layout
	// Rotation is how many degrees (0, 90, 180, 270) the picture is turned
	// clockwise on the matrix.
	Rotation int
	// QuietMode shows nothing but Awaiting Input.
	QuietMode bool
	// DoneDecay is how long a session stays Done before it decays to Idle.
	DoneDecay time.Duration
	// DisconnectedTimeout is how long the Host may stay silent before the
	// Device shows Disconnected.
	DisconnectedTimeout time.Duration
	Escalation          Escalation
	// ProjectColors are the pinned Project Colors ("#rrggbb"), by the
	// absolute path of the project's repo root.
	ProjectColors map[string]string
}

// Defaults are the Preferences without a config file.
func Defaults() Preferences {
	return Preferences{
		Theme:               "default",
		Brightness:          100,
		Layout:              Adaptive,
		DoneDecay:           10 * time.Minute,
		DisconnectedTimeout: 5 * time.Second,
		Escalation:          Escalation{Faster: 30 * time.Second, Takeover: 2 * time.Minute, Steady: 10 * time.Minute},
		ProjectColors:       map[string]string{},
	}
}

// Themes are the Theme names a Preference may give.
var themes = []string{"default"}

// Bounds of the durations.
const (
	minDoneDecay           = time.Second
	minDisconnectedTimeout = 2 * time.Second // the Host sends a heartbeat every second
	maxDisconnectedTimeout = time.Hour
	maxEscalation          = 24 * time.Hour
)

// The Preferences under the [project_colors] table, by repo root.
const projectColorsTable = "project_colors"

// kind is how a Preference is written in the file and on the command line.
type kind int

const (
	stringKind kind = iota
	integerKind
	boolKind
	durationKind
	colorKind
)

// setting is one Preference: its key, how it is written, how it is read
// from Preferences and set on them.
type setting struct {
	path []string
	kind kind
	get  func(p Preferences) value
	// set validates v and sets it on p.
	set func(p *Preferences, v value) error
}

func (s setting) key() string { return strings.Join(s.path, ".") }

// settings are every Preference but the pinned Project Colors, in the order
// `codino get` lists them.
var settings = []setting{
	{
		path: []string{"theme"}, kind: stringKind,
		get: func(p Preferences) value { return value{kind: stringValue, text: p.Theme} },
		set: func(p *Preferences, v value) error {
			for _, t := range themes {
				if v.text == t {
					p.Theme = v.text
					return nil
				}
			}
			return fmt.Errorf("want one of %s, got %q", strings.Join(themes, ", "), v.text)
		},
	},
	{
		path: []string{"brightness"}, kind: integerKind,
		get: func(p Preferences) value { return value{kind: integerValue, num: int64(p.Brightness)} },
		set: func(p *Preferences, v value) error {
			if v.num < 1 || v.num > 100 {
				return fmt.Errorf("want a percentage of the brightness cap from 1 to 100, got %d", v.num)
			}
			p.Brightness = int(v.num)
			return nil
		},
	},
	{
		path: []string{"layout"}, kind: stringKind,
		get: func(p Preferences) value { return value{kind: stringValue, text: string(p.Layout)} },
		set: func(p *Preferences, v value) error {
			if l := Layout(v.text); l != Adaptive && l != Aggregate {
				return fmt.Errorf("want %q or %q, got %q", Adaptive, Aggregate, v.text)
			}
			p.Layout = Layout(v.text)
			return nil
		},
	},
	{
		path: []string{"rotation"}, kind: integerKind,
		get: func(p Preferences) value { return value{kind: integerValue, num: int64(p.Rotation)} },
		set: func(p *Preferences, v value) error {
			if v.num != 0 && v.num != 90 && v.num != 180 && v.num != 270 {
				return fmt.Errorf("want 0, 90, 180 or 270 degrees, got %d", v.num)
			}
			p.Rotation = int(v.num)
			return nil
		},
	},
	{
		path: []string{"quiet_mode"}, kind: boolKind,
		get: func(p Preferences) value { return value{kind: boolValue, flag: p.QuietMode} },
		set: func(p *Preferences, v value) error { p.QuietMode = v.flag; return nil },
	},
	durationSetting([]string{"done_decay"}, func(p *Preferences) *time.Duration { return &p.DoneDecay },
		minDoneDecay, 0),
	durationSetting([]string{"disconnected_timeout"}, func(p *Preferences) *time.Duration { return &p.DisconnectedTimeout },
		minDisconnectedTimeout, maxDisconnectedTimeout),
	durationSetting([]string{"escalation", "faster"}, func(p *Preferences) *time.Duration { return &p.Escalation.Faster },
		0, maxEscalation),
	durationSetting([]string{"escalation", "takeover"}, func(p *Preferences) *time.Duration { return &p.Escalation.Takeover },
		0, maxEscalation),
	durationSetting([]string{"escalation", "steady"}, func(p *Preferences) *time.Duration { return &p.Escalation.Steady },
		0, maxEscalation),
}

// durationSetting is a Preference written as a duration string ("30s",
// "2m"), from min to max (0: no bound).
func durationSetting(path []string, field func(p *Preferences) *time.Duration, min, max time.Duration) setting {
	return setting{
		path: path, kind: durationKind,
		get: func(p Preferences) value { return value{kind: stringValue, text: formatDuration(*field(&p))} },
		set: func(p *Preferences, v value) error {
			d, err := time.ParseDuration(v.text)
			if err != nil {
				return fmt.Errorf("want a duration such as \"30s\" or \"2m\", got %q", v.text)
			}
			if d < min || (max > 0 && d > max) {
				if max > 0 {
					return fmt.Errorf("want a duration from %s to %s, got %s", formatDuration(min), formatDuration(max), v.text)
				}
				return fmt.Errorf("want a duration of at least %s, got %s", formatDuration(min), v.text)
			}
			*field(p) = d
			return nil
		},
	}
}

// pinSetting is the Preference pinning a Project Color to the repo root at
// root (as written; `~/` is the home directory).
func pinSetting(root string) setting {
	return setting{
		path: []string{projectColorsTable, root}, kind: colorKind,
		get: func(p Preferences) value {
			canonical, _ := canonicalRoot(root)
			return value{kind: stringValue, text: p.ProjectColors[canonical]}
		},
		set: func(p *Preferences, v value) error {
			canonical, err := canonicalRoot(root)
			if err != nil {
				return err
			}
			color, ok := parseColor(v.text)
			if !ok {
				return fmt.Errorf("want a Project Color as \"#rrggbb\", got %q", v.text)
			}
			p.ProjectColors[canonical] = color
			return nil
		},
	}
}

// lookup finds the Preference at path.
func lookup(path []string) (setting, error) {
	if len(path) == 2 && path[0] == projectColorsTable {
		return pinSetting(path[1]), nil
	}
	for _, s := range settings {
		if equal(s.path, path) {
			return s, nil
		}
	}
	if len(path) == 1 && (path[0] == "escalation" || path[0] == projectColorsTable) {
		return setting{}, fmt.Errorf("%s is a table: give one of its keys, such as %s", path[0], exampleKey(path[0]))
	}
	return setting{}, fmt.Errorf("unknown Preference %q (known: %s)", strings.Join(path, "."), knownKeys())
}

func exampleKey(table string) string {
	if table == projectColorsTable {
		return projectColorsTable + "./path/to/repo"
	}
	return table + ".faster"
}

func knownKeys() string {
	keys := make([]string, 0, len(settings)+1)
	for _, s := range settings {
		keys = append(keys, s.key())
	}
	return strings.Join(append(keys, projectColorsTable+".PATH"), ", ")
}

// canonicalRoot is the absolute, clean path a pinned Project Color is keyed
// by: `~/` is the home directory.
func canonicalRoot(root string) (string, error) {
	if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("project %q: %w", root, err)
		}
		root = filepath.Join(home, root[1:])
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("want the absolute path of a project's repo root, got %q", root)
	}
	return filepath.Clean(root), nil
}

// parseColor reads "#rrggbb", in either case, as lower case.
func parseColor(s string) (string, bool) {
	if len(s) != 7 || s[0] != '#' {
		return "", false
	}
	if _, err := strconv.ParseUint(s[1:], 16, 32); err != nil {
		return "", false
	}
	return strings.ToLower(s), true
}

// formatDuration writes d in its shortest form: "1m30s", "2m" (where
// time.Duration writes "2m0s").
func formatDuration(d time.Duration) string {
	if d <= 0 || d%time.Millisecond != 0 {
		return d.String()
	}
	var b strings.Builder
	for _, unit := range []struct {
		d    time.Duration
		name string
	}{{time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}, {time.Millisecond, "ms"}} {
		if n := d / unit.d; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, unit.name)
			d -= n * unit.d
		}
	}
	return b.String()
}

// fromDocument reads the Preferences from the parsed file: the defaults,
// with what the file sets.
func fromDocument(doc document) (Preferences, error) {
	p := Defaults()
	seen := map[string]int{}
	for i, l := range doc.lines {
		n := i + 1
		switch l.kind {
		case header:
			if len(l.path) != 1 || (l.path[0] != "escalation" && l.path[0] != projectColorsTable) {
				return Preferences{}, lineError(n, "unknown table [%s]: the tables are [escalation] and [%s]",
					strings.Join(l.path, "."), projectColorsTable)
			}
		case keyValue:
			s, err := lookup(l.path)
			if err != nil {
				return Preferences{}, lineError(n, "%v", err)
			}
			id := s.key()
			if s.kind == colorKind {
				root, err := canonicalRoot(l.path[1])
				if err != nil {
					return Preferences{}, lineError(n, "%s: %v", projectColorsTable, err)
				}
				id = projectColorsTable + "." + root
			}
			if first, ok := seen[id]; ok {
				return Preferences{}, lineError(n, "%s is already set on line %d", s.key(), first)
			}
			seen[id] = n
			if err := checkKind(s, l.value); err != nil {
				return Preferences{}, lineError(n, "%s: %v", s.key(), err)
			}
			if err := s.set(&p, l.value); err != nil {
				return Preferences{}, lineError(n, "%s: %v", s.key(), err)
			}
		}
	}
	if err := p.validate(); err != nil {
		return Preferences{}, err
	}
	return p, nil
}

// checkKind reports a value of the wrong TOML type for the setting.
func checkKind(s setting, v value) error {
	want := map[kind]valueKind{
		stringKind: stringValue, durationKind: stringValue, colorKind: stringValue,
		integerKind: integerValue, boolKind: boolValue,
	}[s.kind]
	if v.kind == want {
		return nil
	}
	switch s.kind {
	case integerKind:
		return errors.New("want an integer")
	case boolKind:
		return errors.New("want true or false")
	case durationKind:
		return errors.New("want a duration as a string, such as \"30s\" or \"2m\"")
	default:
		return errors.New("want a \"string\"")
	}
}

// validate checks what involves several Preferences.
func (p Preferences) validate() error {
	e := p.Escalation
	if e.Faster > e.Takeover || e.Takeover > e.Steady {
		return fmt.Errorf("escalation: the steps come in order, faster (%s) ≤ takeover (%s) ≤ steady (%s)",
			formatDuration(e.Faster), formatDuration(e.Takeover), formatDuration(e.Steady))
	}
	return nil
}

// Setting is one Preference and its current value, as `codino get` shows it.
type Setting struct {
	Key   string
	Value string
}

// List returns every Preference with its value, the pinned Project Colors
// last, by repo root.
func (p Preferences) List() []Setting {
	var all []Setting
	for _, s := range settings {
		all = append(all, Setting{Key: s.key(), Value: display(s.get(p))})
	}
	roots := make([]string, 0, len(p.ProjectColors))
	for root := range p.ProjectColors {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		all = append(all, Setting{Key: projectColorsTable + "." + root, Value: p.ProjectColors[root]})
	}
	return all
}

// Get returns the value of the Preference at key, as `codino get` shows
// it. A project without a pinned Project Color gives "".
func (p Preferences) Get(key string) (string, error) {
	s, err := lookup(splitKey(key))
	if err != nil {
		return "", err
	}
	return display(s.get(p)), nil
}

// display is a value as the command line shows it: strings unquoted.
func display(v value) string {
	if v.kind == stringValue {
		return v.text
	}
	return v.format()
}

// splitKey splits a command-line key: "escalation.faster", or
// "project_colors.PATH" where PATH may hold dots of its own.
func splitKey(key string) []string {
	if rest, ok := strings.CutPrefix(key, projectColorsTable+"."); ok {
		return []string{projectColorsTable, rest}
	}
	return strings.Split(key, ".")
}

// parseArgument reads a value given on the command line for s.
func parseArgument(s setting, text string) (value, error) {
	switch s.kind {
	case integerKind:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return value{}, fmt.Errorf("%s: want an integer, got %q", s.key(), text)
		}
		return value{kind: integerValue, num: n}, nil
	case boolKind:
		switch text {
		case "true", "on", "yes":
			return value{kind: boolValue, flag: true}, nil
		case "false", "off", "no":
			return value{kind: boolValue, flag: false}, nil
		}
		return value{}, fmt.Errorf("%s: want true or false, got %q", s.key(), text)
	case durationKind:
		// Written back in its shortest form.
		if d, err := time.ParseDuration(text); err == nil && d >= 0 {
			text = formatDuration(d)
		}
	case colorKind:
		text = strings.ToLower(text)
	}
	return value{kind: stringValue, text: text}, nil
}
