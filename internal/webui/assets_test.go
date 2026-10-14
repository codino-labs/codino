package webui_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The browser Virtual Device (assets/device.wasm) is built from the Device
// core by `make -C device web` and committed, so building the Host needs no
// C++ toolchain. device.wasm.sources holds the hashes of the sources it was
// built from: when the Device core changes, the preview would no longer
// match the LEDs until it is rebuilt.
func TestTheBrowserDeviceIsBuiltFromTheCurrentDeviceCore(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("assets", "device.wasm"))
	if err != nil || !bytes.HasPrefix(wasm, []byte("\x00asm")) {
		t.Fatalf("assets/device.wasm is no WebAssembly module (%v): run make -C device web", err)
	}
	f, err := os.Open(filepath.Join("assets", "device.wasm.sources"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	device := filepath.Join("..", "..", "device")
	var listed []string
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		sum, path, ok := strings.Cut(lines.Text(), "  ")
		if !ok {
			t.Fatalf("bad line %q", lines.Text())
		}
		listed = append(listed, path)
		data, err := os.ReadFile(filepath.Join(device, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s: %v: run make -C device web", path, err)
			continue
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("device/%s changed since assets/device.wasm was built: run make -C device web", path)
		}
	}
	core, err := filepath.Glob(filepath.Join(device, "core", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range core {
		rel := "core/" + filepath.Base(path)
		if !slices.Contains(listed, rel) {
			t.Errorf("device/%s is not in the build of assets/device.wasm: run make -C device web", rel)
		}
	}
}
