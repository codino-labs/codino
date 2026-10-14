package devicelink

import (
	"reflect"
	"testing"
	"testing/fstest"
)

// Which serial ports the Host tries, per OS. A Device is a native USB CDC
// port with Espressif's USB vendor id (0x303a); a port that is something
// else (a UART bridge, Bluetooth, another vendor's board) is never opened,
// so other projects plugged in aren't disturbed. Where the OS doesn't show
// the vendor without cgo (macOS), every USB CDC port is a candidate and the
// handshake decides.

// uevent is a USB interface's uevent in sysfs, for a vendor/product.
func uevent(product string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("DEVTYPE=usb_interface\nDRIVER=cdc_acm\nPRODUCT=" + product + "\nTYPE=239/2/1\nINTERFACE=2/2/0\n")}
}

func TestOnLinuxTheCandidatesAreEspressifUSBCDCPorts(t *testing.T) {
	sys := fstest.MapFS{
		"class/tty/ttyACM0/device/uevent":  uevent("303a/1001/101"),
		"class/tty/ttyACM1/device/uevent":  uevent("2341/43/1"), // an Arduino Uno
		"class/tty/ttyACM2/device/uevent":  uevent("303a/4001/100"),
		"class/tty/ttyACM10/device/uevent": uevent("303A/1001/101"),
		"class/tty/ttyUSB0/device/uevent":  uevent("10c4/ea60/100"), // a CP210x UART bridge
		"class/tty/ttyS0/dev":              &fstest.MapFile{Data: []byte("4:64\n")},
		"class/tty/ttyACM3/dev":            &fstest.MapFile{Data: []byte("166:3\n")}, // no device: gone mid-read
	}
	got := linuxSerialPorts(sys)
	want := []string{"/dev/ttyACM0", "/dev/ttyACM2", "/dev/ttyACM10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("linuxSerialPorts = %q, want %q", got, want)
	}
}

func TestOnLinuxWithoutSysfsThereAreNoCandidates(t *testing.T) {
	if got := linuxSerialPorts(fstest.MapFS{}); len(got) != 0 {
		t.Fatalf("linuxSerialPorts = %q, want none", got)
	}
}

func TestOnMacOSTheCandidatesAreUSBModemCallOutPorts(t *testing.T) {
	dev := fstest.MapFS{
		"cu.usbmodem21201":           &fstest.MapFile{},
		"cu.usbmodem1101":            &fstest.MapFile{},
		"tty.usbmodem1101":           &fstest.MapFile{}, // the dial-in twin: opening it waits for carrier
		"cu.usbserial-0001":          &fstest.MapFile{}, // a UART bridge
		"cu.Bluetooth-Incoming-Port": &fstest.MapFile{},
		"null":                       &fstest.MapFile{},
	}
	got := darwinSerialPorts(dev)
	want := []string{"/dev/cu.usbmodem1101", "/dev/cu.usbmodem21201"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("darwinSerialPorts = %q, want %q", got, want)
	}
}

func TestOnWindowsTheCandidatesArePresentEspressifCOMPorts(t *testing.T) {
	// present: the ports in HARDWARE\DEVICEMAP\SERIALCOMM, which lists
	// only what is plugged in. usb: PortName under each USB device in
	// SYSTEM\CurrentControlSet\Enum\USB, which remembers unplugged ones.
	present := []string{"COM1", "COM12", "COM7", "COM5"}
	usb := []usbPort{
		{HardwareID: `VID_303A&PID_1001&MI_00`, Port: "COM12"},
		{HardwareID: `VID_303A&PID_1001`, Port: "COM5"},
		{HardwareID: `VID_2341&PID_0043`, Port: "COM7"}, // an Arduino Uno
		{HardwareID: `vid_303a&pid_4001`, Port: "COM9"}, // unplugged
	}
	got := windowsSerialPorts(present, usb)
	want := []string{"COM5", "COM12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windowsSerialPorts = %q, want %q", got, want)
	}
}
