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
			c.Refused()
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
		return Candidate{Name: name, Open: open, Refused: func() { refused[name] = true }}
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
