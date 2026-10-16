// Package devicelink is the Host side of the link to a Device: it finds
// candidate transports, performs the handshake, and claims a Device only after
// a valid hello_reply.
//
// Where candidates come from is the Discoverer's job. USBSerial lists the USB
// serial ports a Device may be on; VirtualDevice spawns the terminal Virtual
// Device.
package devicelink

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/protocol"
)

// HandshakeTimeout bounds how long the Host waits for a hello_reply.
const HandshakeTimeout = 2 * time.Second

// Candidate is a transport that may lead to a Device. Open is called once per
// connection attempt.
type Candidate struct {
	Name string
	Open func() (io.ReadWriteCloser, error)
	// Refused, when set, is told that the candidate opened but answered
	// hello with no valid hello_reply in this Host's protocol version, and
	// why: a *VersionMismatchError for a Device of another version.
	Refused func(err error)
	// Incompatible, when set, is why the candidate was refused a while
	// ago, still to be reported: Claim neither opens it nor sends it hello,
	// and fails with this error.
	Incompatible error
}

// Discoverer lists the candidates to try, in order of preference.
type Discoverer interface {
	Candidates() []Candidate
}

// DiscovererFunc adapts a function to a Discoverer.
type DiscovererFunc func() []Candidate

func (f DiscovererFunc) Candidates() []Candidate { return f() }

// NoDevices offers no candidates: a Host with nothing to drive.
var NoDevices = DiscovererFunc(func() []Candidate { return nil })

// FlasherURL is where the human updates a Device's firmware: the web
// flasher (device/flasher). It has no public address yet, so this points at
// the README's flashing section; set it to the published flasher's URL once
// there is one (docs/hardware/reference-board.md, "Web flasher").
const FlasherURL = "https://github.com/codino-labs/codino#2-flash"

// VersionMismatchError reports a Device speaking another protocol version
// than the Host. The Host refuses to drive it, and the Device shows "update
// me": the human has to update the firmware (a Device older than the Host)
// or Codino on the Host (a newer one).
type VersionMismatchError struct {
	Candidate string
	// The Device's protocol and firmware versions, and the Host's protocol.
	DeviceProtocol int
	Firmware       string
	HostProtocol   int
}

// FirmwareOutdated is true when the Device is the one to update.
func (e *VersionMismatchError) FirmwareOutdated() bool { return e.DeviceProtocol < e.HostProtocol }

func (e *VersionMismatchError) Error() string {
	if e.FirmwareOutdated() {
		return fmt.Sprintf("%s runs firmware %s, which speaks protocol %d; this Host speaks protocol %d: update the firmware with the web flasher, %s",
			e.Candidate, e.Firmware, e.DeviceProtocol, e.HostProtocol, FlasherURL)
	}
	return fmt.Sprintf("%s runs firmware %s, which speaks protocol %d, newer than this Host's protocol %d: update Codino on this Host (the Claude Code plugin)",
		e.Candidate, e.Firmware, e.DeviceProtocol, e.HostProtocol)
}

// Device is a claimed Device: the handshake succeeded and protocol versions match.
type Device struct {
	Name       string
	HelloReply protocol.HelloReply

	port         io.ReadWriteCloser
	gone         chan struct{}
	once         sync.Once
	acknowledged chan struct{}
	orientations chan int
	installed    chan protocol.InstallReply
}

// Claim tries every candidate in order and returns the first one that answers
// hello with a valid hello_reply in this Host's protocol version. now is the
// Host time put in hello. When nothing is claimed, the error explains the last
// failure; a *VersionMismatchError wins over other failures so the user learns
// that a Device needs an update.
func Claim(candidates []Candidate, now time.Time) (*Device, error) {
	if len(candidates) == 0 {
		return nil, errors.New("no Device found")
	}
	var failure error
	for _, c := range candidates {
		var dev *Device
		var err error
		if c.Incompatible != nil {
			err = c.Incompatible
		} else {
			dev, err = open(c, now)
		}
		if err == nil {
			return dev, nil
		}
		var mismatch *VersionMismatchError
		if failure == nil || errors.As(err, &mismatch) {
			failure = err
		}
	}
	return nil, failure
}

// open opens a candidate and makes the handshake, telling it when it
// refused.
func open(c Candidate, now time.Time) (*Device, error) {
	port, err := c.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", c.Name, err)
	}
	dev, err := handshake(c, port, now)
	if err != nil && c.Refused != nil {
		c.Refused(err)
	}
	return dev, err
}

func handshake(c Candidate, port io.ReadWriteCloser, now time.Time) (*Device, error) {
	hello, err := protocol.Encode(protocol.NewHello(now.UnixMilli()))
	if err != nil {
		port.Close()
		return nil, fmt.Errorf("hello to %s: %w", c.Name, err)
	}
	if _, err := port.Write(hello); err != nil {
		port.Close()
		return nil, fmt.Errorf("hello to %s: %w", c.Name, err)
	}

	lines := bufio.NewReader(port)
	type result struct {
		reply protocol.HelloReply
		err   error
	}
	read := make(chan result, 1)
	go func() {
		for {
			line, err := lines.ReadBytes('\n')
			if err != nil {
				read <- result{err: fmt.Errorf("no hello_reply: %w", err)}
				return
			}
			reply, err := protocol.DecodeHelloReply(line)
			if errors.Is(err, protocol.ErrNotHelloReply) {
				continue // boot noise or a message the handshake doesn't need
			}
			read <- result{reply, err}
			return
		}
	}()
	var r result
	select {
	case r = <-read:
	case <-time.After(HandshakeTimeout):
		port.Close() // unblocks the reader
		return nil, fmt.Errorf("%s: no hello_reply within %s", c.Name, HandshakeTimeout)
	}
	if r.err != nil {
		port.Close()
		return nil, fmt.Errorf("%s: %w", c.Name, r.err)
	}
	reply := r.reply
	if reply.Protocol != protocol.Version {
		port.Close()
		return nil, &VersionMismatchError{Candidate: c.Name, DeviceProtocol: reply.Protocol, Firmware: reply.Firmware, HostProtocol: protocol.Version}
	}

	dev := &Device{Name: c.Name, HelloReply: reply, port: port, gone: make(chan struct{}), acknowledged: make(chan struct{}, 1),
		orientations: make(chan int, 1), installed: make(chan protocol.InstallReply, installRepliesQueued)}
	go dev.drain(lines)
	return dev, nil
}

// drain reads what the Device sends after the handshake (an Acknowledge
// from the human, the reply to an install) until the Device goes away.
func (d *Device) drain(lines *bufio.Reader) {
	for {
		line, err := lines.ReadBytes('\n')
		if err != nil {
			d.Close()
			return
		}
		if protocol.IsAcknowledge(line) {
			select {
			case d.acknowledged <- struct{}{}:
			default: // one is pending already: Acknowledging twice is once
			}
		}
		if rotation, ok := protocol.DecodeOrientation(line); ok {
			// Only the latest counts: one not received yet is replaced.
			select {
			case <-d.orientations:
			default:
			}
			d.orientations <- rotation
		}
		if reply, err := protocol.DecodeInstallReply(line); err == nil {
			select {
			case d.installed <- reply:
			default: // nobody is waiting for that many: drop it
			}
		}
	}
}

// Acknowledged receives once the human Acknowledges on the Device (a button
// press or a double tap), for every session.
func (d *Device) Acknowledged() <-chan struct{} { return d.acknowledged }

// Orientations receives the rotation the Device's IMU senses whenever it
// changes (the Device turned another way up): only the latest when several
// came meanwhile.
func (d *Device) Orientations() <-chan int { return d.orientations }

// installRepliesQueued is how many install replies wait to be received:
// the answer to an install, and late answers to earlier ones.
const installRepliesQueued = 4

// InstallReplies receives the Device's answer to each install and
// uninstall, in order; beyond installRepliesQueued unreceived, newer ones
// are dropped.
func (d *Device) InstallReplies() <-chan protocol.InstallReply { return d.installed }

// WriteTimeout bounds one write to a Device that stopped reading, on
// transports that support write deadlines.
const WriteTimeout = time.Second

// Send writes one protocol message. An error means the Device is gone; the
// caller closes it (closing can block on a hung Device, so it is the caller's
// to schedule).
func (d *Device) Send(msg any) error {
	line, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	if p, ok := d.port.(interface{ SetWriteDeadline(time.Time) error }); ok {
		p.SetWriteDeadline(time.Now().Add(WriteTimeout))
	}
	if _, err := d.port.Write(line); err != nil {
		return fmt.Errorf("send to %s: %w", d.Name, err)
	}
	return nil
}

// Gone is closed once the Device is unplugged or closed.
func (d *Device) Gone() <-chan struct{} { return d.gone }

// Close releases the Device.
func (d *Device) Close() error {
	var err error
	d.once.Do(func() {
		close(d.gone)
		err = d.port.Close()
	})
	return err
}
