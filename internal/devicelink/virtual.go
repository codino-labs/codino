package devicelink

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// VirtualDevice is a Discoverer whose one candidate is the terminal Virtual
// Device (device/build/virtual-device). Each connection attempt spawns it as
// a subprocess and speaks the protocol on its stdin/stdout; the matrix it
// draws on stderr goes to Display.
type VirtualDevice struct {
	Path    string
	Display io.Writer
	// Args are the Virtual Device's command-line arguments, e.g. a
	// --preferences-file to cache Preferences in, as a Device does in flash.
	Args []string
}

func (v VirtualDevice) Candidates() []Candidate {
	return []Candidate{{Name: "virtual device " + v.Path, Open: v.open}}
}

func (v VirtualDevice) open() (io.ReadWriteCloser, error) {
	cmd := exec.Command(v.Path, v.Args...)
	cmd.Stderr = v.Display
	// os.Pipes rather than cmd.StdinPipe/StdoutPipe: Wait must not close the
	// ends the Device Link uses, and os.File supports write deadlines. The
	// reader sees EOF once the Virtual Device exits, which tells the Device
	// Link it is gone.
	childStdin, stdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("virtual device stdin: %w", err)
	}
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		childStdin.Close()
		stdin.Close()
		return nil, fmt.Errorf("virtual device stdout: %w", err)
	}
	cmd.Stdin = childStdin
	cmd.Stdout = childStdout
	err = cmd.Start()
	childStdin.Close()
	childStdout.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start virtual device: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	return &process{stdin: stdin, stdout: stdout, kill: cmd.Process.Kill, exited: exited}, nil
}

// process is a subprocess seen as a port.
type process struct {
	stdin  *os.File
	stdout io.ReadCloser
	kill   func() error
	exited chan struct{}
}

func (p *process) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *process) Write(b []byte) (int, error) { return p.stdin.Write(b) }

func (p *process) SetWriteDeadline(t time.Time) error { return p.stdin.SetWriteDeadline(t) }

// Close ends the protocol stream, which makes the Virtual Device exit, and
// kills it if it hasn't within a second.
func (p *process) Close() error {
	err := p.stdin.Close()
	select {
	case <-p.exited:
	case <-time.After(time.Second):
		p.kill()
		<-p.exited
	}
	p.stdout.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
