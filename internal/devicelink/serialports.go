package devicelink

import (
	"bufio"
	"bytes"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Which serial ports may lead to a Device, per OS. The listings are worked
// out here from what the OS shows (sysfs, /dev, the registry), on every OS,
// so they are tested everywhere; serial_<os>.go reads the real sources.
//
// A Device is a native USB CDC port. Where the OS shows the USB vendor
// without cgo (Linux, Windows), only usbVendors' ports are candidates, so
// other boards plugged in are never opened; on macOS every USB CDC port is,
// and the handshake decides.

// usbVendors are the USB vendor ids of the boards a Device runs on:
// Espressif's, for the ESP32-S3 and its siblings with native USB.
var usbVendors = []uint64{0x303a}

func isDeviceVendor(vid uint64) bool { return slices.Contains(usbVendors, vid) }

// linuxSerialPorts lists the USB CDC ACM ports (/dev/ttyACM*) of a Device
// vendor, from sysfs (mounted at /sys): each port's USB interface uevent
// carries PRODUCT=vid/pid/bcd in hex.
func linuxSerialPorts(sys fs.FS) []string {
	entries, err := fs.ReadDir(sys, "class/tty")
	if err != nil {
		return nil
	}
	var ports []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "ttyACM") {
			continue
		}
		uevent, err := fs.ReadFile(sys, path.Join("class/tty", name, "device/uevent"))
		if err != nil {
			continue // unplugged meanwhile
		}
		if vid, ok := ueventVendor(uevent); ok && isDeviceVendor(vid) {
			ports = append(ports, "/dev/"+name)
		}
	}
	sortPorts(ports)
	return ports
}

// ueventVendor reads the vendor id from a USB uevent's PRODUCT=vid/pid/bcd.
func ueventVendor(uevent []byte) (uint64, bool) {
	lines := bufio.NewScanner(bytes.NewReader(uevent))
	for lines.Scan() {
		product, ok := strings.CutPrefix(lines.Text(), "PRODUCT=")
		if !ok {
			continue
		}
		vid, _, _ := strings.Cut(product, "/")
		v, err := strconv.ParseUint(vid, 16, 16)
		return v, err == nil
	}
	return 0, false
}

// darwinSerialPorts lists the USB CDC call-out ports (/dev/cu.usbmodem*),
// from /dev. The tty.* twins wait for carrier when opened; UART bridges are
// cu.usbserial*.
func darwinSerialPorts(dev fs.FS) []string {
	names, err := fs.Glob(dev, "cu.usbmodem*")
	if err != nil {
		return nil
	}
	ports := make([]string, 0, len(names))
	for _, n := range names {
		ports = append(ports, "/dev/"+n)
	}
	sortPorts(ports)
	return ports
}

// usbPort is a serial port the Windows registry records under a USB device
// (SYSTEM\CurrentControlSet\Enum\USB\<HardwareID>\<instance>\Device
// Parameters\PortName), plugged in or not.
type usbPort struct {
	HardwareID string // e.g. VID_303A&PID_1001&MI_00
	Port       string // e.g. COM5
}

// windowsSerialPorts lists the COM ports of a Device vendor that are
// plugged in: present (from HARDWARE\DEVICEMAP\SERIALCOMM) and recorded
// under a USB device of that vendor.
func windowsSerialPorts(present []string, usb []usbPort) []string {
	var ports []string
	for _, p := range present {
		for _, u := range usb {
			if strings.EqualFold(u.Port, p) && isDeviceVendor(hardwareVendor(u.HardwareID)) {
				ports = append(ports, p)
				break
			}
		}
	}
	sortPorts(ports)
	return ports
}

// hardwareVendor reads the vendor id from a hardware id such as
// VID_303A&PID_1001&MI_00; 0 when there is none.
func hardwareVendor(id string) uint64 {
	for part := range strings.SplitSeq(id, "&") {
		if vid, ok := strings.CutPrefix(strings.ToUpper(part), "VID_"); ok {
			v, err := strconv.ParseUint(vid, 16, 16)
			if err == nil {
				return v
			}
		}
	}
	return 0
}

// sortPorts orders ports by name, numbers by value: ttyACM2 before
// ttyACM10, COM5 before COM12.
func sortPorts(ports []string) {
	slices.SortFunc(ports, func(a, b string) int {
		pa, na := splitNumber(a)
		pb, nb := splitNumber(b)
		if c := strings.Compare(pa, pb); c != 0 {
			return c
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
}

// splitNumber splits a trailing decimal number off name.
func splitNumber(name string) (string, uint64) {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	n, _ := strconv.ParseUint(name[i:], 10, 64)
	return name[:i], n
}
