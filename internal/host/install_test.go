package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/protocol"
)

// fileOf is a fixture as a Theme or Sprite Pack file holds it: the install
// message without its type.
func fileOf(t *testing.T, name string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(fixture(t, name)), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "type")
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func installOf(t *testing.T, name string) protocol.Install {
	t.Helper()
	install, err := protocol.NewInstall(fileOf(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return install
}

// claimedDevice is a Host that has claimed a reference Device answering
// installs with reply.
func claimedDevice(t *testing.T, reply func(line string) string) (*host.Host, *fakeDevice) {
	t.Helper()
	dev := newReferenceDevice(t)
	dev.answer = reply
	h := host.New(clockAt(1760432400000), devices(dev))
	t.Cleanup(h.Close)
	h.Tick()
	return h, dev
}

// sentAfterHandshake is what the Host sent after hello and the Preferences.
func sentAfterHandshake(dev *fakeDevice) []string {
	var out []string
	for _, line := range dev.Received()[2:] {
		if !strings.Contains(line, `"heartbeat"`) {
			out = append(out, line)
		}
	}
	return out
}

func TestInstallingAThemeSendsTheInstallFixtureAndReturnsTheDevicesReply(t *testing.T) {
	h, dev := claimedDevice(t, func(line string) string {
		if strings.Contains(line, `"install"`) {
			return fixture(t, "install-reply.jsonl")
		}
		return ""
	})

	reply, err := h.Install(context.Background(), installOf(t, "install-theme.jsonl"))

	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, sentAfterHandshake(dev), []string{fixture(t, "install-theme.jsonl")})
	if !reply.OK || reply.Name != "ember" {
		t.Errorf("reply %+v", reply)
	}
	// What the Device has now shows in the status.
	st := h.Status()
	if !reflect.DeepEqual(st.Device.Themes, []string{"default", "minimal", "ember"}) ||
		!reflect.DeepEqual(st.Device.SpritePacks, []string{"nino", "dots"}) {
		t.Errorf("status lists themes %q, sprite packs %q", st.Device.Themes, st.Device.SpritePacks)
	}
}

func TestUninstallingSendsTheUninstallFixture(t *testing.T) {
	h, dev := claimedDevice(t, func(line string) string {
		return `{"type":"install_reply","kind":"theme","name":"ember","ok":true,"themes":["default","minimal"],"sprite_packs":["nino"]}`
	})

	reply, err := h.Uninstall(context.Background(), protocol.KindTheme, "ember")

	if err != nil || !reply.OK {
		t.Fatalf("reply %+v, err %v", reply, err)
	}
	assertMessages(t, sentAfterHandshake(dev), []string{fixture(t, "uninstall.jsonl")})
	if got := h.Status().Device.Themes; !reflect.DeepEqual(got, []string{"default", "minimal"}) {
		t.Errorf("themes %q", got)
	}
}

func TestAnInstallTheDeviceRefusesComesBackWithItsReason(t *testing.T) {
	h, _ := claimedDevice(t, func(line string) string {
		return `{"type":"install_reply","kind":"theme","name":"ember","ok":false,"error":"states.idle: palette must hold 1 to 16 colors","themes":["default","minimal"],"sprite_packs":["nino"]}`
	})

	reply, err := h.Install(context.Background(), installOf(t, "install-theme.jsonl"))

	if err != nil {
		t.Fatal(err)
	}
	if reply.OK || !strings.Contains(reply.Error, "palette") {
		t.Errorf("reply %+v", reply)
	}
}

func TestTheStatusListsTheThemesTheClaimedDeviceHas(t *testing.T) {
	h, _ := claimedDevice(t, nil)

	st := h.Status()

	if !reflect.DeepEqual(st.Device.Themes, []string{"default", "minimal"}) || !reflect.DeepEqual(st.Device.SpritePacks, []string{"nino"}) {
		t.Errorf("themes %q, sprite packs %q", st.Device.Themes, st.Device.SpritePacks)
	}
	if st.Theme != "default" {
		t.Errorf("theme %q, want the Preference's", st.Theme)
	}
}

func TestInstallingWithoutADeviceFails(t *testing.T) {
	h := host.New(clockAt(1760432400000), noDevices)
	defer h.Close()
	h.Tick()

	_, err := h.Install(context.Background(), installOf(t, "install-theme.jsonl"))

	if !errors.Is(err, host.ErrNoDevice) {
		t.Errorf("got %v, want ErrNoDevice", err)
	}
}

func TestADeviceThatNeverAnswersAnInstallFailsWhenTheCallerStopsWaiting(t *testing.T) {
	h, _ := claimedDevice(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.Install(ctx, installOf(t, "install-theme.jsonl"))

	if err == nil || !strings.Contains(err.Error(), "no reply") {
		t.Errorf("got %v, want no reply", err)
	}
}

func TestALateReplyToAnEarlierInstallIsNotTakenForTheAnswer(t *testing.T) {
	h, _ := claimedDevice(t, func(line string) string {
		// The reply to an earlier install the Host stopped waiting for
		// arrives first, then the answer.
		return `{"type":"install_reply","kind":"sprite_pack","name":"dots","ok":false,"error":"late","themes":["default","minimal"],"sprite_packs":["nino"]}` +
			"\n" + fixture(t, "install-reply.jsonl")
	})

	reply, err := h.Install(context.Background(), installOf(t, "install-theme.jsonl"))

	if err != nil {
		t.Fatal(err)
	}
	if !reply.OK || reply.Kind != protocol.KindTheme || reply.Name != "ember" {
		t.Errorf("reply %+v, want the one for theme ember", reply)
	}
	if got := h.Status().Device.Themes; !reflect.DeepEqual(got, []string{"default", "minimal", "ember"}) {
		t.Errorf("themes %q", got)
	}
}
