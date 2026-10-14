package devicelink

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/codino-labs/codino/internal/protocol"
)

// A pty pair stands in for a USB serial port: the Host opens the pty's
// terminal end as it would /dev/ttyACM0, and the test plays the Device on
// the other end. A pty is a terminal like a CDC ACM port, so the raw mode the
// Host needs (no echo, no newline translation, no signals) is tested too.

// ptyPair returns the Device's end and the path of the port the Host opens.
func ptyPair(t *testing.T) (*os.File, string) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	device := os.NewFile(uintptr(fd), "ptmx") // non-blocking: it takes deadlines
	device.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { device.Close() })
	return device, fmt.Sprintf("/dev/pts/%d", n)
}

func fixtureLine(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "protocol", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestADeviceOnASerialPortIsClaimed(t *testing.T) {
	device, port := ptyPair(t)
	go func() {
		// The Device: wait for hello, then reply.
		line, err := bufio.NewReader(device).ReadBytes('\n')
		if err == nil && bytes.Contains(line, []byte(`"hello"`)) {
			device.Write(fixtureLine(t, "hello-reply.jsonl"))
		}
	}()
	dev, err := Claim(USBSerial{list: func() []string { return []string{port} }}.Candidates(), time.UnixMilli(1760432400000))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	defer dev.Close()
	if dev.Name != port || dev.HelloReply.Protocol != protocol.Version {
		t.Fatalf("claimed %q speaking protocol %d", dev.Name, dev.HelloReply.Protocol)
	}
}

func TestASerialPortCarriesBytesUnchanged(t *testing.T) {
	device, port := ptyPair(t)
	p, err := openSerial(port)
	if err != nil {
		t.Fatalf("openSerial: %v", err)
	}
	defer p.Close()
	// A cooked port would keep a read waiting: fail rather than hang.
	p.(*os.File).SetDeadline(time.Now().Add(5 * time.Second))

	// Device → Host: a cooked terminal would turn \r into \n, eat ^C and
	// ^?, and echo it all back.
	sent := []byte("a\rb\x03c\x7fd\xff\n")
	if _, err := device.Write(sent); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(sent))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sent) {
		t.Errorf("the Host read %q, want %q", got, sent)
	}

	// Host → Device: no \n → \r\n, and nothing echoed back.
	if _, err := p.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, err := device.Read(buf)
	if err != nil || string(buf[:n]) != "x\n" {
		t.Errorf("the Device read %q, %v, want %q", buf[:n], err, "x\n")
	}
}

func TestClosingASerialPortEndsAPendingRead(t *testing.T) {
	// The handshake closes the port to give up on a silent candidate.
	_, port := ptyPair(t)
	p, err := openSerial(port)
	if err != nil {
		t.Fatalf("openSerial: %v", err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := p.Read(make([]byte, 1))
		read <- err
	}()
	p.Close()
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("a read on a closed port succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the port left a read blocked")
	}
}

func TestAPortInUseIsNotOpenedTwice(t *testing.T) {
	// The Host holds its Device's port exclusively, so another program
	// can't interleave with the protocol; and it can't take a port another
	// program holds that way.
	_, port := ptyPair(t)
	p, err := openSerial(port)
	if err != nil {
		t.Fatalf("openSerial: %v", err)
	}
	defer p.Close()
	if q, err := openSerial(port); err == nil {
		q.Close()
		t.Fatal("a port the Host holds opened again")
	}
}
