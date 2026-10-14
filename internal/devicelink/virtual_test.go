package devicelink_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/protocol"
)

// The terminal Virtual Device is the real Device core: claiming it checks the
// Host's handshake against the C++ side. Build it with `make -C device`.
func TestTheVirtualDeviceIsClaimedAndRendersSessions(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "device", "build", "virtual-device"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("Virtual Device not built (make -C device): %v", err)
	}
	var display syncBuffer
	vd := devicelink.VirtualDevice{Path: path, Display: &display}

	dev, err := devicelink.Claim(vd.Candidates(), time.UnixMilli(1760432400000))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	want, err := protocol.DecodeHelloReply(fixtureBytes(t, "hello-reply.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dev.HelloReply, want) {
		t.Errorf("HelloReply = %+v, want the hello-reply fixture %+v", dev.HelloReply, want)
	}
	if err := dev.Send(protocol.NewSessionUpsert("a", "awaiting_input", "#3a86ff", 1760432400000, 0)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(display.String(), `"state":"awaiting_input"`) {
		t.Errorf("the Virtual Device did not render the upsert; display:\n%s", display.String())
	}
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(data, "\r\n")
}
