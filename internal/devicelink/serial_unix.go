//go:build linux || darwin

package devicelink

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// openSerial opens a serial port exclusively (TIOCEXCL: no other program
// can open it meanwhile, and one that holds it so makes this fail) and in raw
// mode: 8N1, no echo, no newline or signal translation, no flow control.
// The baud rate means nothing to USB CDC; it is set to 115200, never 1200,
// which some boards take as "reboot into the bootloader".
//
// The port is non-blocking under the Go runtime's poller, so Close ends a
// pending Read and writes take deadlines.
func openSerial(path string) (io.ReadWriteCloser, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		err = &os.PathError{Op: "open", Path: path, Err: err}
		if errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("%w (%s)", err, permissionHint)
		}
		return nil, err
	}
	if err := configure(fd); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("configure %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func configure(fd int) error {
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		return fmt.Errorf("exclusive: %w", err)
	}
	t, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return err
	}
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF | unix.IXANY
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	setSpeed(t)
	return unix.IoctlSetTermios(fd, setTermios, t)
}
