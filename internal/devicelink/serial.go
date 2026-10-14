package devicelink

import "io"

// USBSerial is the Discoverer of real Devices: the USB CDC serial ports
// plugged into the Host that may be a Device (serialports.go), in a stable
// order. Each is opened in raw mode for the handshake; Claim keeps only one
// that answers it.
type USBSerial struct {
	// list replaces the OS's listing, for tests.
	list func() []string
}

func (s USBSerial) Candidates() []Candidate {
	list := s.list
	if list == nil {
		list = serialPorts
	}
	var candidates []Candidate
	for _, port := range list() {
		candidates = append(candidates, Candidate{Name: port, Open: func() (io.ReadWriteCloser, error) {
			return openSerial(port)
		}})
	}
	return candidates
}
