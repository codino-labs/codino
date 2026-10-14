// Command codino is the Host side of Codino: the CLI and the Host Daemon in
// one binary.
//
//	codino event --session ID --state STATE [--project PATH]
//	codino event --session ID --end
//	codino status [--json]
//	codino daemon [--virtual-device PATH]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
)

// name is the product name, kept here so internal packages don't carry it.
const name = "codino"

// virtualDeviceEnv names the Virtual Device binary the Host Daemon drives. It
// is an environment variable so a daemon spawned by `codino event` inherits it.
const virtualDeviceEnv = "CODINO_VIRTUAL_DEVICE"

const usage = `usage:
  codino event --session ID --state STATE [--project PATH]
  codino event --session ID --end
  codino status [--json]
  codino daemon [--virtual-device PATH]

STATE is one of: idle, working, awaiting_input, done, error.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	paths := daemon.DefaultPaths(name)
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "event":
		err = event(paths, args)
	case "status":
		err = status(paths, args, os.Stdout)
	case "daemon":
		err = runDaemon(paths, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n%s", name, cmd, usage)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

var errUsage = errors.New("usage")

func newFlags(cmd string) *flag.FlagSet {
	fs := flag.NewFlagSet(name+" "+cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), usage) }
	return fs
}

func event(paths daemon.Paths, args []string) error {
	fs := newFlags("event")
	id := fs.String("session", "", "Agent Session id")
	state := fs.String("state", "", "Session State")
	project := fs.String("project", "", "project directory of the session")
	end := fs.Bool("end", false, "the session ended: remove it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || (*state == "") == !*end {
		fmt.Fprintf(os.Stderr, "%s event: give --state or --end\n%s", name, usage)
		return errUsage
	}
	e := host.Event{SessionID: *id, State: host.SessionState(*state), End: *end}
	if *project != "" {
		abs, err := filepath.Abs(*project)
		if err != nil {
			return fmt.Errorf("project: %w", err)
		}
		e.Project = abs
	}
	if err := e.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "%s event: %v\n%s", name, err, usage)
		return errUsage
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find own binary: %w", err)
	}
	client := daemon.Client{Paths: paths, Spawn: daemon.SpawnCommand(paths, self, "daemon")}
	return client.Deliver(context.Background(), e)
}

func status(paths daemon.Paths, args []string, out io.Writer) error {
	fs := newFlags("status")
	asJSON := fs.Bool("json", false, "print the status as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := daemon.Client{Paths: paths}.Status(context.Background())
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Fprintln(out, "Host Daemon: not running")
		return nil
	}
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	printStatus(out, st, time.Now())
	return nil
}

func printStatus(out io.Writer, st host.Status, now time.Time) {
	switch d := st.Device; {
	case d.Claimed:
		fmt.Fprintf(out, "Device: %s (firmware %s, protocol %d)\n", d.Name, d.Firmware, d.Protocol)
	case d.Problem != "":
		fmt.Fprintf(out, "Device: not connected: %s\n", d.Problem)
	default:
		fmt.Fprintln(out, "Device: not connected")
	}
	fmt.Fprintf(out, "Aggregate State: %s\n", st.AggregateState)
	if len(st.Sessions) == 0 {
		fmt.Fprintln(out, "No Agent Sessions.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nTILE\tSESSION\tSTATE\tFOR\tPROJECT")
	for _, s := range st.Sessions {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", s.Tile, s.ID, s.State, now.Sub(s.StateEntered).Truncate(time.Second), s.Project)
	}
	tw.Flush()
}

func runDaemon(paths daemon.Paths, args []string) error {
	fs := newFlags("daemon")
	virtual := fs.String("virtual-device", os.Getenv(virtualDeviceEnv),
		"drive the Virtual Device binary at this path (default $"+virtualDeviceEnv+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ln, err := daemon.Listen(paths)
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		slog.Info("another Host Daemon is running; exiting")
		return nil
	}
	if err != nil {
		return err
	}
	defer ln.Unlock() // after h.Close below: release the Device, then the lock

	// Real USB serial discovery is not built yet: without a Virtual Device the
	// daemon tracks sessions but drives no Device.
	var devices devicelink.Discoverer = devicelink.NoDevices
	if *virtual != "" {
		path, err := filepath.Abs(*virtual)
		if err != nil {
			return fmt.Errorf("virtual device: %w", err)
		}
		devices = devicelink.VirtualDevice{Path: path, Display: os.Stderr}
	}
	h := host.New(systemClock{}, devices)
	defer h.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("Host Daemon listening", "socket", paths.Socket, "virtual_device", *virtual)
	return daemon.Run(ctx, ln, h)
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
