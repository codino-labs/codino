package devicelink

import (
	"os"

	"golang.org/x/sys/unix"
)

const (
	getTermios = unix.TIOCGETA
	setTermios = unix.TIOCSETA
)

func setSpeed(t *unix.Termios) {
	t.Ispeed = 115200
	t.Ospeed = 115200
}

const permissionHint = "your user may not open this serial port"

func serialPorts() []string { return darwinSerialPorts(os.DirFS("/dev")) }
