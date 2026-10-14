package devicelink

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// With no Device plugged in, the Host looks for one every couple of
// seconds. A port that answered the handshake with anything but a valid
// hello_reply is some other board (user story 10): it isn't opened again
// until it is unplugged, or for RefusedRetry.

type fakePorts struct {
	ports []string
	now   time.Time
}

func (f *fakePorts) serial() *USBSerial {
	return &USBSerial{list: func() []string { return f.ports }, now: func() time.Time { return f.now }}
}

func names(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func refuse(t *testing.T, cs []Candidate, name string) {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			c.Refused(errors.New("no hello_reply"))
			return
		}
	}
	t.Fatalf("no candidate %s", name)
}

func TestAPortThatRefusedTheHandshakeIsNotTriedAgain(t *testing.T) {
	f := &fakePorts{ports: []string{"/dev/ttyACM0", "/dev/ttyACM1"}, now: time.Unix(1000, 0)}
	s := f.serial()
	refuse(t, s.Candidates(), "/dev/ttyACM0")
	f.now = f.now.Add(10 * time.Second)
	if got := names(s.Candidates()); !slices.Equal(got, []string{"/dev/ttyACM1"}) {
		t.Fatalf("candidates %q, want only the port that didn't refuse", got)
	}
}

func TestAPortThatRefusedIsTriedAgainOnceReplugged(t *testing.T) {
	// Reflashing a Device that spoke another protocol replugs it.
	f := &fakePorts{ports: []string{"/dev/ttyACM0"}, now: time.Unix(1000, 0)}
	s := f.serial()
	refuse(t, s.Candidates(), "/dev/ttyACM0")
	f.ports = nil
	s.Candidates()
	f.ports = []string{"/dev/ttyACM0"}
	if got := names(s.Candidates()); !slices.Equal(got, []string{"/dev/ttyACM0"}) {
		t.Fatalf("candidates %q after a replug, want the port", got)
	}
}

func TestAPortThatRefusedIsTriedAgainAfterAWhile(t *testing.T) {
	f := &fakePorts{ports: []string{"/dev/ttyACM0"}, now: time.Unix(1000, 0)}
	s := f.serial()
	refuse(t, s.Candidates(), "/dev/ttyACM0")
	f.now = f.now.Add(RefusedRetry)
	if got := names(s.Candidates()); !slices.Equal(got, []string{"/dev/ttyACM0"}) {
		t.Fatalf("candidates %q after RefusedRetry, want the port", got)
	}
}

// port is a candidate's transport: what the "Device" says, then EOF.
type port struct {
	io.Reader
	io.Writer
}

func (port) Close() error { return nil }

func TestClaimTellsACandidateThatRefusedTheHandshake(t *testing.T) {
	refused := map[string]bool{}
	candidate := func(name string, open func() (io.ReadWriteCloser, error)) Candidate {
		return Candidate{Name: name, Open: open, Refused: func(error) { refused[name] = true }}
	}
	_, err := Claim([]Candidate{
		candidate("busy", func() (io.ReadWriteCloser, error) { return nil, errors.New("in use") }),
		candidate("other board", func() (io.ReadWriteCloser, error) {
			return port{Reader: strings.NewReader("boot noise\n"), Writer: io.Discard}, nil
		}),
	}, time.UnixMilli(1760432400000))
	if err == nil {
		t.Fatal("claimed a board that never replied hello_reply")
	}
	if refused["busy"] {
		t.Error("a port that could not be opened was told it refused: it never heard hello")
	}
	if !refused["other board"] {
		t.Error("a port that answered no hello_reply was not told it refused")
	}
}

// A Codino that speaks another protocol version is refused like another
// board, but it is the human's to fix (update the firmware, or Codino on the
// Host), so the Host keeps saying why while it stays plugged in, without
// sending it hello again: the Device keeps showing "update me" meanwhile.

func refuseAsMismatch(t *testing.T, cs []Candidate, name string, deviceProtocol int) {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			c.Refused(&VersionMismatchError{Candidate: name, Device: deviceProtocol, Firmware: "0.0.9", Host: deviceProtocol + 1})
			return
		}
	}
	t.Fatalf("no candidate %s", name)
}

func TestADeviceOnAnotherProtocolIsReportedWhileItStaysPluggedIn(t *testing.T) {
	f := &fakePorts{ports: []string{"/dev/ttyACM0"}, now: time.Unix(1000, 0)}
	s := f.serial()
	refuseAsMismatch(t, s.Candidates(), "/dev/ttyACM0", 0)
	f.now = f.now.Add(10 * time.Second)

	// Opening the port would fail here (no such device): an error other than
	// the mismatch would mean Claim tried it again.
	_, err := Claim(s.Candidates(), f.now)
	var mismatch *VersionMismatchError
	if !errors.As(err, &mismatch) || mismatch.Device != 0 || mismatch.Firmware != "0.0.9" {
		t.Fatalf("Claim() error = %v, want the remembered version mismatch", err)
	}

	f.ports = nil
	if _, err := Claim(s.Candidates(), f.now); errors.As(err, &mismatch) {
		t.Errorf("Claim() error = %v once unplugged, want no mismatch", err)
	}
}

func TestADeviceOnAnotherProtocolIsAskedAgainAfterAWhile(t *testing.T) {
	// Its "update me" may have been lost to a reset meanwhile: a new hello
	// shows it again.
	f := &fakePorts{ports: []string{"/dev/ttyACM0"}, now: time.Unix(1000, 0)}
	s := f.serial()
	refuseAsMismatch(t, s.Candidates(), "/dev/ttyACM0", 0)
	f.now = f.now.Add(RefusedRetry)
	cs := s.Candidates()
	if len(cs) != 1 || cs[0].Incompatible != nil {
		t.Fatalf("candidates %+v after RefusedRetry, want the port to try again", cs)
	}
}

func TestAVersionMismatchSaysWhatToUpdate(t *testing.T) {
	older := (&VersionMismatchError{Candidate: "/dev/ttyACM0", Device: 0, Firmware: "0.1.0", Host: 1}).Error()
	for _, want := range []string{"/dev/ttyACM0", "firmware 0.1.0", "protocol 0", "protocol 1", "update the firmware", FlasherURL} {
		if !strings.Contains(older, want) {
			t.Errorf("older Device: %q doesn't say %q", older, want)
		}
	}
	newer := (&VersionMismatchError{Candidate: "/dev/ttyACM0", Device: 2, Firmware: "0.3.0", Host: 1}).Error()
	for _, want := range []string{"protocol 2", "protocol 1", "update Codino on this Host"} {
		if !strings.Contains(newer, want) {
			t.Errorf("newer Device: %q doesn't say %q", newer, want)
		}
	}
	if strings.Contains(newer, FlasherURL) {
		t.Errorf("newer Device: %q sends the human to the flasher, but the Host is the one to update", newer)
	}
}
