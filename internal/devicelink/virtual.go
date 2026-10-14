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
	Args    []string
	Display io.Writer
}

func (v VirtualDevice) Candidates() []Candidate {
	return []Candidate{{Name: "virtual device " + v.Path, Open: v.open}}
}

func (v VirtualDevice) open() (io.ReadWriteCloser, error) {
	cmd := exec.Command(v.Path, v.Args...)
	cmd.Stderr = v.Display
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("virtual device stdin: %w", err)
	}
	// An os.Pipe rather than cmd.StdoutPipe: Wait must not close the read
	// end while the Device Link still reads it. The reader sees EOF once the
	// Virtual Device exits, which tells the Device Link it is gone.
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("virtual device stdout: %w", err)
	}
	cmd.Stdout = childStdout
	err = cmd.Start()
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
	stdin  io.WriteCloser
	stdout io.ReadCloser
	kill   func() error
	exited chan struct{}
}

func (p *process) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *process) Write(b []byte) (int, error) { return p.stdin.Write(b) }

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
