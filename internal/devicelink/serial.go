package devicelink

import (
	"errors"
	"io"
	"sync"
	"time"
)

// RefusedRetry is how long a port that refused the handshake is left alone
// while it stays plugged in.
const RefusedRetry = time.Minute

// USBSerial is the Discoverer of real Devices: the USB CDC serial ports
// plugged into the Host that may be a Device (serialports.go), in a stable
// order. Each is opened in raw mode for the handshake; Claim keeps only one
// that answers it.
//
// A port that refused the handshake is another board (user story 10): it
// is not opened again, and sent hello again, until it is unplugged and
// plugged back in (as reflashing a Device does), or for RefusedRetry. A
// Codino of another protocol version is refused the same way, and stays a
// candidate meanwhile only to report the mismatch (Candidate.Incompatible),
// so the human keeps being told what to update. Use it by pointer: it
// remembers them.
type USBSerial struct {
	// list and now replace the OS's listing and clock, for tests.
	list func() []string
	now  func() time.Time

	mu      sync.Mutex
	refused map[string]refusal // port → when and why it refused
}

type refusal struct {
	at  time.Time
	err error
}

func (s *USBSerial) Candidates() []Candidate {
	list, now := s.list, s.now
	if list == nil {
		list = serialPorts
	}
	if now == nil {
		now = time.Now
	}
	ports := list()
	s.mu.Lock()
	defer s.mu.Unlock()
	plugged := make(map[string]bool, len(ports))
	var candidates []Candidate
	for _, port := range ports {
		plugged[port] = true
		c := Candidate{
			Name:    port,
			Open:    func() (io.ReadWriteCloser, error) { return openSerial(port) },
			Refused: func(err error) { s.refuse(port, refusal{now(), err}) },
		}
		if r, ok := s.refused[port]; ok && now().Sub(r.at) < RefusedRetry {
			var mismatch *VersionMismatchError
			if !errors.As(r.err, &mismatch) {
				continue
			}
			c.Incompatible = r.err
		}
		candidates = append(candidates, c)
	}
	for port := range s.refused {
		if !plugged[port] {
			delete(s.refused, port) // unplugged: a new board may come back there
		}
	}
	return candidates
}

func (s *USBSerial) refuse(port string, r refusal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refused == nil {
		s.refused = map[string]refusal{}
	}
	s.refused[port] = r
}
