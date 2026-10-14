package protocol_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/codino-labs/codino/internal/protocol"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(data, "\r\n")
}

// withoutType is a fixture as a Theme or Sprite Pack file holds it: the
// install message without its type.
func withoutType(t *testing.T, line []byte) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "type")
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("not JSON: %q", got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("not JSON: %q", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAThemeOrSpritePackFileBecomesTheInstallFixture(t *testing.T) {
	for _, name := range []string{"install-theme.jsonl", "install-sprite-pack.jsonl"} {
		t.Run(name, func(t *testing.T) {
			install, err := protocol.NewInstall(withoutType(t, fixture(t, name)))
			if err != nil {
				t.Fatal(err)
			}
			line, err := protocol.Encode(install)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(line, []byte("\n")) != 1 || !bytes.HasSuffix(line, []byte("\n")) {
				t.Fatalf("not one line: %q", line)
			}
			sameJSON(t, line, fixture(t, name))
		})
	}
}

func TestInstallNamesWhatItInstalls(t *testing.T) {
	install, err := protocol.NewInstall(withoutType(t, fixture(t, "install-theme.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	if install.Kind != protocol.KindTheme || install.Name != "ember" {
		t.Errorf("got %q %q, want theme ember", install.Kind, install.Name)
	}
}

func TestAFileThatIsNoThemeOrSpritePackIsRefusedBeforeItReachesTheDevice(t *testing.T) {
	long := `{"kind":"theme","name":"big","padding":"` + strings.Repeat("x", protocol.MaxLine) + `"}`
	for _, tc := range []struct{ name, file, want string }{
		{"not JSON", `theme`, "JSON"},
		{"not an object", `["theme"]`, "JSON object"},
		{"no kind", `{"name":"x"}`, "kind"},
		{"unknown kind", `{"kind":"font","name":"x"}`, "kind"},
		{"no name", `{"kind":"theme"}`, "name"},
		{"bad name", `{"kind":"theme","name":"My Theme"}`, "name"},
		{"too long", long, "bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocol.NewInstall([]byte(tc.file))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error about %s", err, tc.want)
			}
		})
	}
}

func TestUninstallIsTheUninstallFixture(t *testing.T) {
	line, err := protocol.Encode(protocol.NewUninstall(protocol.KindTheme, "ember"))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, line, fixture(t, "uninstall.jsonl"))
}

func TestTheInstallReplyFixtureDecodes(t *testing.T) {
	reply, err := protocol.DecodeInstallReply(fixture(t, "install-reply.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.InstallReply{Type: "install_reply", Kind: protocol.KindTheme, Name: "ember", OK: true,
		Themes: []string{"default", "minimal", "ember"}, SpritePacks: []string{"nino", "dots"}}
	if !reflect.DeepEqual(reply, want) {
		t.Errorf("got %+v\nwant %+v", reply, want)
	}
	if _, err := protocol.DecodeInstallReply([]byte(`{"type":"acknowledge"}`)); err != protocol.ErrNotInstallReply {
		t.Errorf("an acknowledge: got %v, want ErrNotInstallReply", err)
	}
}

func TestTheHelloReplyFixtureListsTheDevicesThemesAndSpritePacks(t *testing.T) {
	reply, err := protocol.DecodeHelloReply(fixture(t, "hello-reply.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reply.Themes, []string{"default", "minimal"}) || !reflect.DeepEqual(reply.SpritePacks, []string{"nino"}) {
		t.Errorf("got themes %q, sprite packs %q", reply.Themes, reply.SpritePacks)
	}
}
