package devicelink

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func serialPorts() []string { return windowsSerialPorts(presentCOMPorts(), usbCOMPorts()) }

// presentCOMPorts lists the COM ports plugged in now.
func presentCOMPorts() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil // no serial port at all: the key appears with the first
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil
	}
	var ports []string
	for _, n := range names {
		if port, _, err := k.GetStringValue(n); err == nil {
			ports = append(ports, port)
		}
	}
	return ports
}

// usbCOMPorts lists the COM ports recorded under USB devices of a Device
// vendor, plugged in or not.
func usbCOMPorts() []usbPort {
	usb, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer usb.Close()
	ids, err := usb.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var ports []usbPort
	for _, id := range ids {
		if !isDeviceVendor(hardwareVendor(id)) {
			continue
		}
		device, err := registry.OpenKey(usb, id, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		instances, _ := device.ReadSubKeyNames(-1)
		for _, instance := range instances {
			params, err := registry.OpenKey(device, instance+`\Device Parameters`, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			if port, _, err := params.GetStringValue("PortName"); err == nil {
				ports = append(ports, usbPort{HardwareID: id, Port: port})
			}
			params.Close()
		}
		device.Close()
	}
	return ports
}

// openSerial opens a COM port (exclusively: Windows shares none) for
// overlapped I/O under the Go runtime's poller, so Close ends a pending
// Read and writes take deadlines; 8N1 with DTR and RTS on, as a terminal
// opens a USB CDC port (a CDC device may not send until DTR is up). The
// baud rate means nothing to USB CDC; it is 115200, never 1200.
func openSerial(name string) (io.ReadWriteCloser, error) {
	path, err := windows.UTF16PtrFromString(`\\.\` + name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("open %s: in use by another program: %w", name, err)
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	if err := configure(h); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("configure %s: %w", name, err)
	}
	return os.NewFile(uintptr(h), name), nil
}

func configure(h windows.Handle) error {
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(h, &dcb); err != nil {
		return err
	}
	const fBinary = 0x1
	dcb.Flags = fBinary | windows.DTR_CONTROL_ENABLE | windows.RTS_CONTROL_ENABLE
	dcb.BaudRate = 115200
	dcb.ByteSize = 8
	dcb.Parity = windows.NOPARITY
	dcb.StopBits = windows.ONESTOPBIT
	if err := windows.SetCommState(h, &dcb); err != nil {
		return err
	}
	// A read returns as soon as a byte is there, and otherwise waits for
	// one (MAXDWORD, MAXDWORD, constant < MAXDWORD); a read that returned
	// nothing would look like the end of the port. Writes don't time out
	// here: Send's deadline bounds them.
	const maxDword = ^uint32(0)
	return windows.SetCommTimeouts(h, &windows.CommTimeouts{
		ReadIntervalTimeout:        maxDword,
		ReadTotalTimeoutMultiplier: maxDword,
		ReadTotalTimeoutConstant:   maxDword - 1,
	})
}
