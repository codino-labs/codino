package devicelink

import (
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
// plugged back in (as reflashing a Device does), or for RefusedRetry. Use
// it by pointer: it remembers them.
type USBSerial struct {
	// list and now replace the OS's listing and clock, for tests.
	list func() []string
	now  func() time.Time

	mu      sync.Mutex
	refused map[string]time.Time // port → when it refused
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
		if at, ok := s.refused[port]; ok && now().Sub(at) < RefusedRetry {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:    port,
			Open:    func() (io.ReadWriteCloser, error) { return openSerial(port) },
			Refused: func() { s.refuse(port, now()) },
		})
	}
	for port := range s.refused {
		if !plugged[port] {
			delete(s.refused, port) // unplugged: a new board may come back there
		}
	}
	return candidates
}

func (s *USBSerial) refuse(port string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refused == nil {
		s.refused = map[string]time.Time{}
	}
	s.refused[port] = at
}
