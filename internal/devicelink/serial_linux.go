package devicelink

import (
	"os"

	"golang.org/x/sys/unix"
)

const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)

func setSpeed(t *unix.Termios) {
	t.Cflag &^= unix.CBAUD
	t.Cflag |= unix.B115200
	t.Ispeed = unix.B115200
	t.Ospeed = unix.B115200
}

// permissionHint is what a user who may not open a port needs to know.
const permissionHint = "your user may not open serial ports: install the udev rule or join the dialout group (uucp on Arch), see the README"

func serialPorts() []string { return linuxSerialPorts(os.DirFS("/sys")) }
