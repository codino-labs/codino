package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The plugin's bin/codino is a POSIX sh launcher: Claude Code runs every hook
// command through sh (Git Bash on Windows), and the launcher runs the binary
// built for the OS and CPU it is on. These tests run it with a fake uname
// and stand-in binaries that print their own name.

const launcher = "../../plugin/bin/codino"

// pluginBin copies the launcher into a fresh bin/ with a stand-in for each
// named binary, and returns the launcher's path.
func pluginBin(t *testing.T, binaries ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the launcher runs under sh; on Windows that is Git Bash, not the test's shell")
	}
	script, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(bin, "codino"), string(script))
	for _, b := range binaries {
		writeScript(t, filepath.Join(bin, b), "#!/bin/sh\necho \""+b+" $*\"\n")
	}
	return filepath.Join(bin, "codino")
}

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runLauncher runs it on a machine whose uname reports system and machine.
func runLauncher(t *testing.T, path, system, machine string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	fake := t.TempDir()
	writeScript(t, filepath.Join(fake, "uname"),
		"#!/bin/sh\ncase \"$1\" in -s) echo '"+system+"';; -m) echo '"+machine+"';; esac\n")
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

var allBinaries = []string{
	"codino-linux-amd64", "codino-linux-arm64",
	"codino-darwin-amd64", "codino-darwin-arm64",
	"codino-windows-amd64.exe", "codino-windows-arm64.exe",
}

func TestTheLauncherRunsTheBinaryForThisOSAndCPU(t *testing.T) {
	path := pluginBin(t, allBinaries...)
	for _, tc := range []struct{ system, machine, want string }{
		{"Linux", "x86_64", "codino-linux-amd64"},
		{"Linux", "aarch64", "codino-linux-arm64"},
		{"Linux", "arm64", "codino-linux-arm64"},
		{"Darwin", "arm64", "codino-darwin-arm64"},
		{"Darwin", "x86_64", "codino-darwin-amd64"},
		{"MINGW64_NT-10.0-26100", "x86_64", "codino-windows-amd64.exe"},
		{"MSYS_NT-10.0-26100", "x86_64", "codino-windows-amd64.exe"},
		{"MINGW64_NT-10.0-26100-ARM64", "aarch64", "codino-windows-arm64.exe"},
	} {
		out, stderr, err := runLauncher(t, path, tc.system, tc.machine, "hook", "--x", "a b")
		if err != nil {
			t.Errorf("%s %s: %v (%s)", tc.system, tc.machine, err, stderr)
			continue
		}
		if want := tc.want + " hook --x a b\n"; out != want {
			t.Errorf("%s %s ran %q, want %q", tc.system, tc.machine, out, want)
		}
	}
}

func TestTheLauncherFollowsASymlinkToTheBinaries(t *testing.T) {
	path := pluginBin(t, allBinaries...)
	link := filepath.Join(t.TempDir(), "codino")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runLauncher(t, link, "Linux", "x86_64", "status")
	if err != nil || out != "codino-linux-amd64 status\n" {
		t.Fatalf("through a symlink ran %q, %v (%s)", out, err, stderr)
	}
}

func TestTheLauncherFailsACommandWithNoBinaryForThisMachine(t *testing.T) {
	path := pluginBin(t, "codino-linux-amd64")
	for _, tc := range []struct{ system, machine string }{
		{"Linux", "riscv64"}, // no build for this CPU
		{"FreeBSD", "amd64"}, // nor for this OS
		{"Darwin", "arm64"},  // a build missing from bin/
	} {
		_, stderr, err := runLauncher(t, path, tc.system, tc.machine, "status")
		if err == nil {
			t.Errorf("%s %s: status succeeded", tc.system, tc.machine)
		}
		if !strings.Contains(stderr, "codino") {
			t.Errorf("%s %s: stderr %q does not explain", tc.system, tc.machine, stderr)
		}
	}
}

func TestTheLauncherNeverFailsAHook(t *testing.T) {
	// A Codino failure must not fail an Agent Session.
	path := pluginBin(t, "codino-linux-amd64")
	_, stderr, err := runLauncher(t, path, "FreeBSD", "amd64", "hook")
	if err != nil {
		t.Fatalf("hook on a machine with no binary: %v", err)
	}
	if !strings.Contains(stderr, "codino") {
		t.Errorf("stderr %q does not explain", stderr)
	}
}
