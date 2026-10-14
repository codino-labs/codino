package daemon

import (
	"strings"
	"testing"
)

// On Windows the local socket is a named pipe (spec #1). These pure parts
// run on every OS; the pipe itself only on Windows.

func TestThePipeIsOwnedByAndOpenToTheUserAlone(t *testing.T) {
	const user = "S-1-5-21-3623811015-3361044348-30300820-1013"
	got := pipeSecurityDescriptor(user)
	// Owner: the user. DACL: protected (no inherited entries), one entry
	// allowing the user generic all; nobody else is listed, so nobody else
	// gets in, not Everyone, Administrators or SYSTEM.
	want := "O:" + user + "D:P(A;;GA;;;" + user + ")"
	if got != want {
		t.Fatalf("pipeSecurityDescriptor = %q, want %q", got, want)
	}
}

func TestEachRuntimeDirectoryHasItsOwnPipe(t *testing.T) {
	a := pipeName(`C:\Users\ada\AppData\Local\codino\run`)
	b := pipeName(`C:\Users\bob\AppData\Local\codino\run`)
	if a == b {
		t.Fatalf("two users' runtime directories share the pipe %q", a)
	}
	for _, name := range []string{a, b} {
		rest, ok := strings.CutPrefix(name, `\\.\pipe\`)
		if !ok || rest == "" || strings.ContainsAny(rest, `\/`) || len(name) > 256 {
			t.Errorf("%q is not a local pipe name", name)
		}
	}
}

func TestThePipeNameIgnoresHowTheDirectoryIsSpelled(t *testing.T) {
	// Windows paths are case-insensitive: one directory, one pipe.
	a := pipeName(`C:\Users\Ada\AppData\Local\codino\run`)
	b := pipeName(`c:\users\ada\appdata\local\codino\run\`)
	if a != b {
		t.Fatalf("one directory gives two pipes: %q and %q", a, b)
	}
}
