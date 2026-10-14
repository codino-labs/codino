//go:build !linux && !darwin && !windows

package devicelink

import (
	"errors"
	"io"
)

// Other systems have no serial discovery: the Host drives no Device there.
func serialPorts() []string { return nil }

func openSerial(string) (io.ReadWriteCloser, error) {
	return nil, errors.New("serial ports are not supported on this system")
}
