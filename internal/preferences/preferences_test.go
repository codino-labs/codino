package preferences_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/preferences"
)

// configFile returns the path of a config file holding content, in a fresh
// directory; with content "" there is no file yet.
func configFile(t *testing.T, content string) preferences.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codino", "config.toml")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return preferences.File{Path: path}
}

func read(t *testing.T, f preferences.File) string {
	t.Helper()
	data, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func load(t *testing.T, f preferences.File) preferences.Preferences {
	t.Helper()
	p, err := f.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

func TestWithoutAConfigFileThePreferencesAreTheDefaults(t *testing.T) {
	f := configFile(t, "")

	got := load(t, f)

	want := preferences.Preferences{
		Theme:               "default",
		Brightness:          100,
		Layout:              preferences.Adaptive,
		Rotation:            0,
		QuietMode:           false,
		DoneDecay:           10 * time.Minute,
		DisconnectedTimeout: 5 * time.Second,
		Escalation:          preferences.Escalation{Faster: 30 * time.Second, Takeover: 2 * time.Minute, Steady: 10 * time.Minute},
		ProjectColors:       map[string]string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(preferences.Defaults(), want) {
		t.Errorf("Defaults() = %+v", preferences.Defaults())
	}
}

const fullConfig = `# My Preferences
theme = "default"
brightness = 40      # percent of the cap
layout = "aggregate"
rotation = 270
quiet_mode = true
done_decay = "1h"
disconnected_timeout = "10s"

[escalation]
faster = "45s"
takeover = "5m"
steady = "15m"

[project_colors]
"/home/me/src/app" = "#FF8C00"
'/home/me/src/lib' = "#123abc"
`

func TestTheConfigFileSetsEveryPreference(t *testing.T) {
	got := load(t, configFile(t, fullConfig))

	want := preferences.Preferences{
		Theme:               "default",
		Brightness:          40,
		Layout:              preferences.Aggregate,
		Rotation:            270,
		QuietMode:           true,
		DoneDecay:           time.Hour,
		DisconnectedTimeout: 10 * time.Second,
		Escalation:          preferences.Escalation{Faster: 45 * time.Second, Takeover: 5 * time.Minute, Steady: 15 * time.Minute},
		ProjectColors:       map[string]string{"/home/me/src/app": "#ff8c00", "/home/me/src/lib": "#123abc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestAPreferenceMissingFromTheFileKeepsItsDefault(t *testing.T) {
	got := load(t, configFile(t, "brightness = 70\n[escalation]\nsteady = \"20m\"\n"))

	want := preferences.Defaults()
	want.Brightness = 70
	want.Escalation.Steady = 20 * time.Minute
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestDottedKeysAreTheSameAsTables(t *testing.T) {
	got := load(t, configFile(t, "escalation.faster = \"10s\"\nproject_colors.\"/src/app\" = \"#00ff00\"\n"))

	if got.Escalation.Faster != 10*time.Second || got.ProjectColors["/src/app"] != "#00ff00" {
		t.Errorf("got %+v", got)
	}
}

func TestAPinnedProjectColorUnderTheHomeDirectoryMayStartWithATilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got := load(t, configFile(t, "[project_colors]\n\"~/src/app\" = \"#00ff00\"\n"))

	if c := got.ProjectColors[filepath.Join(home, "src", "app")]; c != "#00ff00" {
		t.Errorf("pins %v", got.ProjectColors)
	}
}

func TestAnInvalidConfigFileIsReportedWithTheLineAtFault(t *testing.T) {
	for _, tt := range []struct {
		name, content, want string
	}{
		{"brightness too low", "brightness = 0", "line 1: brightness"},
		{"brightness too high", "\nbrightness = 101", "line 2: brightness"},
		{"brightness not a number", `brightness = "high"`, "brightness"},
		{"unknown Layout", `layout = "tiles"`, "layout"},
		{"rotation off the quarter turns", "rotation = 45", "rotation"},
		{"quiet_mode not a bool", `quiet_mode = "yes"`, "quiet_mode"},
		{"theme empty", `theme = ""`, "theme"},
		{"duration without a unit", `done_decay = "10"`, "done_decay"},
		{"duration as a number", `done_decay = 600`, "done_decay"},
		{"Disconnected timeout too short", `disconnected_timeout = "1s"`, "disconnected_timeout"},
		{"Escalation out of order", "[escalation]\nfaster = \"3m\"", "escalation"},
		{"Project Color not #rrggbb", "[project_colors]\n\"/src/app\" = \"orange\"", "line 2: project_colors"},
		{"Project Color for a relative path", "[project_colors]\n\"src/app\" = \"#ff0000\"", "absolute"},
		{"unknown key", "brightnes = 50", "line 1: unknown"},
		{"unknown table", "[colours]", "line 1: unknown"},
		{"duplicate key", "brightness = 50\nbrightness = 60", "line 2"},
		{"no value", "brightness =", "line 1"},
		{"not a key = value line", "brightness 50", "line 1"},
		{"unterminated string", `theme = "default`, "line 1"},
		{"array", "rotation = [90]", "line 1"},
		{"float", "brightness = 50.5", "line 1"},
		{"junk after the value", "brightness = 50 50", "line 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := configFile(t, tt.content).Load()

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestStringsMayCarryEscapes(t *testing.T) {
	got := load(t, configFile(t, `layout = "aggregate"`+"\n"+`"project_colors"."/src/a\"b\\" = '#00ff00'`+"\n"))

	if got.Layout != preferences.Aggregate || got.ProjectColors[`/src/a"b\`] != "#00ff00" {
		t.Errorf("got %+v", got)
	}
}

func TestSetCreatesTheConfigFileWhenThereIsNone(t *testing.T) {
	f := configFile(t, "")

	if err := f.Set("brightness", "30"); err != nil {
		t.Fatal(err)
	}

	if got := load(t, f).Brightness; got != 30 {
		t.Errorf("brightness %d, want 30", got)
	}
	if v, err := f.Get("brightness"); err != nil || v != "30" {
		t.Errorf("Get = %q, %v", v, err)
	}
}

func TestSetChangesOnlyTheValueAndKeepsTheRestOfTheFile(t *testing.T) {
	f := configFile(t, fullConfig)

	if err := f.Set("brightness", "60"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("escalation.faster", "1m"); err != nil {
		t.Fatal(err)
	}

	want := strings.Replace(fullConfig, "brightness = 40      # percent", "brightness = 60      # percent", 1)
	want = strings.Replace(want, `faster = "45s"`, `faster = "1m"`, 1)
	if got := read(t, f); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetAddsAMissingPreferenceWhereItBelongs(t *testing.T) {
	f := configFile(t, "# mine\nbrightness = 40\n\n[escalation]\nfaster = \"45s\"\n\n[project_colors]\n\"/src/a\" = \"#ff0000\"\n")

	for _, kv := range [][2]string{{"layout", "aggregate"}, {"escalation.steady", "20m"}, {"quiet_mode", "true"}} {
		if err := f.Set(kv[0], kv[1]); err != nil {
			t.Fatalf("Set(%s): %v", kv[0], err)
		}
	}

	want := "# mine\nbrightness = 40\nlayout = \"aggregate\"\nquiet_mode = true\n\n[escalation]\nfaster = \"45s\"\nsteady = \"20m\"\n\n[project_colors]\n\"/src/a\" = \"#ff0000\"\n"
	if got := read(t, f); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetAddsATableThatIsMissing(t *testing.T) {
	f := configFile(t, "brightness = 40\n")

	if err := f.Set("escalation.takeover", "3m"); err != nil {
		t.Fatal(err)
	}

	if got, want := read(t, f), "brightness = 40\n\n[escalation]\ntakeover = \"3m\"\n"; got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	if got := load(t, f).Escalation.Takeover; got != 3*time.Minute {
		t.Errorf("takeover %v", got)
	}
}

func TestSetPinsAndUnsetUnpinsAProjectColor(t *testing.T) {
	f := configFile(t, "")

	if err := f.Set("project_colors./home/me/my.app", "#00FF00"); err != nil {
		t.Fatal(err)
	}
	if got := load(t, f).ProjectColors; !reflect.DeepEqual(got, map[string]string{"/home/me/my.app": "#00ff00"}) {
		t.Fatalf("pins %v", got)
	}
	if err := f.Unset("project_colors./home/me/my.app"); err != nil {
		t.Fatal(err)
	}
	if got := load(t, f).ProjectColors; len(got) != 0 {
		t.Errorf("pins %v after Unset", got)
	}
}

func TestUnsetReturnsAPreferenceToItsDefault(t *testing.T) {
	f := configFile(t, fullConfig)

	if err := f.Unset("rotation"); err != nil {
		t.Fatal(err)
	}
	if err := f.Unset("rotation"); err != nil {
		t.Fatal(err) // already the default: nothing to do
	}

	if got := load(t, f).Rotation; got != 0 {
		t.Errorf("rotation %d", got)
	}
	if strings.Contains(read(t, f), "rotation") {
		t.Errorf("file still sets rotation:\n%s", read(t, f))
	}
}

func TestSetRefusesAnInvalidValueAndLeavesTheFileAlone(t *testing.T) {
	for _, kv := range [][2]string{
		{"brightness", "0"},
		{"brightness", "bright"},
		{"layout", "tiles"},
		{"rotation", "45"},
		{"quiet_mode", "maybe"},
		{"done_decay", "soon"},
		{"escalation.faster", "1h"}, // after the steady glow
		{"project_colors./src/app", "orange"},
		{"project_colors.src/app", "#ff0000"},
		{"brightnes", "50"},
		{"escalation", "fast"},
	} {
		t.Run(kv[0]+"="+kv[1], func(t *testing.T) {
			f := configFile(t, fullConfig)

			if err := f.Set(kv[0], kv[1]); err == nil {
				t.Errorf("Set(%q, %q) succeeded", kv[0], kv[1])
			}

			if got := read(t, f); got != fullConfig {
				t.Errorf("file changed:\n%s", got)
			}
		})
	}
}

func TestSetRefusesToEditAFileThatIsAlreadyInvalid(t *testing.T) {
	f := configFile(t, "brightness = 500\n")

	err := f.Set("layout", "aggregate")

	if err == nil || !strings.Contains(err.Error(), "brightness") {
		t.Errorf("Set error %v, want the existing problem", err)
	}
}

func TestSetFixesTheValueThatMadeTheFileInvalid(t *testing.T) {
	f := configFile(t, "brightness = 500\n")

	if err := f.Set("brightness", "50"); err != nil {
		t.Fatal(err)
	}

	if got := load(t, f).Brightness; got != 50 {
		t.Errorf("brightness %d", got)
	}
}

func TestSetWritesThroughASymlinkedConfigFile(t *testing.T) {
	dotfiles := configFile(t, "brightness = 40\n")
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(dotfiles.Path, link); err != nil {
		t.Skip("no symlinks here")
	}
	f := preferences.File{Path: link}

	if err := f.Set("brightness", "20"); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced")
	}
	if got := load(t, dotfiles).Brightness; got != 20 {
		t.Errorf("the dotfiles copy has brightness %d", got)
	}
}

func TestGetGivesTheCurrentValueOfEveryPreference(t *testing.T) {
	f := configFile(t, fullConfig)

	all, err := f.List()
	if err != nil {
		t.Fatal(err)
	}

	want := []preferences.Setting{
		{Key: "theme", Value: "default"},
		{Key: "brightness", Value: "40"},
		{Key: "layout", Value: "aggregate"},
		{Key: "rotation", Value: "270"},
		{Key: "quiet_mode", Value: "true"},
		{Key: "done_decay", Value: "1h"},
		{Key: "disconnected_timeout", Value: "10s"},
		{Key: "escalation.faster", Value: "45s"},
		{Key: "escalation.takeover", Value: "5m"},
		{Key: "escalation.steady", Value: "15m"},
		{Key: "project_colors./home/me/src/app", Value: "#ff8c00"},
		{Key: "project_colors./home/me/src/lib", Value: "#123abc"},
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("List() =\n%v\nwant\n%v", all, want)
	}
	for _, s := range want {
		if v, err := f.Get(s.Key); err != nil || v != s.Value {
			t.Errorf("Get(%q) = %q, %v; want %q", s.Key, v, err, s.Value)
		}
	}
	if _, err := f.Get("brightnes"); err == nil {
		t.Error("Get of an unknown key succeeded")
	}
	if v, err := f.Get("project_colors./elsewhere"); err != nil || v != "" {
		t.Errorf("Get of a project without a pin = %q, %v", v, err)
	}
}

func TestDurationsReadBackInTheirShortestForm(t *testing.T) {
	f := configFile(t, "")

	if err := f.Set("done_decay", "90s"); err != nil {
		t.Fatal(err)
	}

	if v, _ := f.Get("done_decay"); v != "1m30s" {
		t.Errorf("done_decay %q", v)
	}
}
