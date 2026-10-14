package protocol_test

import (
	"bytes"
	"testing"

	"github.com/codino-labs/codino/internal/protocol"
)

// hello is the one message every protocol version keeps as it is, so a
// Device learns from any Host's hello that it speaks another version and
// shows "update me" (protocol/README.md, Handshake).
// hello-newer-protocol.jsonl is what the next version's Host will send:
// this Host's hello with the protocol one up.
func TestTheNextProtocolVersionsHelloIsThisOnesWithItsVersion(t *testing.T) {
	const hostTime = 1760432400000
	this, err := protocol.Encode(protocol.NewHello(hostTime))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.TrimRight(this, "\n"); !bytes.Equal(got, fixture(t, "hello.jsonl")) {
		t.Fatalf("hello = %s, want hello.jsonl", got)
	}
	next, err := protocol.Encode(protocol.Hello{Type: "hello", Protocol: protocol.Version + 1, HostTime: hostTime})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.TrimRight(next, "\n"); !bytes.Equal(got, fixture(t, "hello-newer-protocol.jsonl")) {
		t.Errorf("the next version's hello = %s, want hello-newer-protocol.jsonl", got)
	}
}
