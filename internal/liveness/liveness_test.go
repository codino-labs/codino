package liveness_test

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/codino-labs/codino/internal/liveness"
)

// startAgent starts a stand-in for an Agent that runs until killed.
func startAgent(t *testing.T) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("liveness is not probed on Windows yet")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

func TestAgentThatIsAliveIsNotGone(t *testing.T) {
	cmd := startAgent(t)
	pid := cmd.Process.Pid

	if (liveness.Probe{}).Gone(pid, liveness.Started(pid)) {
		t.Fatal("a live Agent is gone")
	}
	if (liveness.Probe{}).Gone(pid, 0) {
		t.Fatal("a live Agent without a start time is gone")
	}
}

func TestAgentKilledIsGone(t *testing.T) {
	cmd := startAgent(t)
	pid := cmd.Process.Pid
	started := liveness.Started(pid)
	cmd.Process.Kill()
	cmd.Wait()

	if !(liveness.Probe{}).Gone(pid, started) {
		t.Fatal("a killed Agent is not gone")
	}
}

func TestAgentKilledButNotYetWaitedForIsGone(t *testing.T) {
	// kill -9 leaves a zombie until the terminal's shell waits for it.
	if runtime.GOOS != "linux" {
		t.Skip("zombies are told apart through /proc")
	}
	cmd := startAgent(t)
	pid := cmd.Process.Pid
	cmd.Process.Kill()
	for !(liveness.Probe{}).Gone(pid, liveness.Started(pid)) {
		runtime.Gosched() // the kernel turns it into a zombie promptly
	}
}

func TestReusedPIDIsGone(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("start times are read through /proc")
	}
	pid := os.Getpid()
	started := liveness.Started(pid)
	if started == 0 {
		t.Fatal("no start time for this process")
	}

	if !(liveness.Probe{}).Gone(pid, started+1) {
		t.Fatal("a pid now naming a process started at another time is not gone")
	}
}

func TestUnknownPIDIsNeverGone(t *testing.T) {
	if (liveness.Probe{}).Gone(0, 0) {
		t.Fatal("pid 0 is gone; the session can't be probed")
	}
}

func TestParentOfThisProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("parents are read through /proc")
	}
	if got, want := liveness.Parent(os.Getpid()), os.Getppid(); got != want {
		t.Fatalf("Parent = %d, want %d", got, want)
	}
}
