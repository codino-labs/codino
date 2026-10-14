// Command codino is the Host side of Codino: the CLI and the Host Daemon in
// one binary.
//
//	codino event --session ID --state STATE [--project PATH]
//	codino event --session ID --end
//	codino event --session ID --acknowledge [--state STATE]
//	codino show IMAGE
//	codino get [KEY]
//	codino set KEY VALUE
//	codino unset KEY
//	codino status [--json]
//	codino daemon [--virtual-device PATH] [--exit-grace DURATION]
//	codino hook   (internal: run by the Claude Code plugin's hooks)
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

	"github.com/codino-labs/codino/internal/claudecode"
	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/gitrepo"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/liveness"
	"github.com/codino-labs/codino/internal/picture"
)

// name is the product name, kept here so internal packages don't carry it.
const name = "codino"

// virtualDeviceEnv names the Virtual Device binary the Host Daemon drives. It
// is an environment variable so a daemon spawned by `codino event` inherits it.
const virtualDeviceEnv = "CODINO_VIRTUAL_DEVICE"

// exitGraceEnv sets how long the Host Daemon stays up with no sessions, for a
// daemon spawned by `codino event` too. Preferences replace it later.
const exitGraceEnv = "CODINO_EXIT_GRACE"

const usage = `usage:
  codino event --session ID --state STATE [--project PATH]
  codino event --session ID --end
  codino event --session ID --acknowledge [--state STATE]
  codino show IMAGE
  codino get [KEY]
  codino set KEY VALUE
  codino unset KEY
  codino status [--json]
  codino daemon [--virtual-device PATH] [--exit-grace DURATION]
  codino hook   (internal: Claude Code hooks pipe their JSON to it)

STATE is one of: idle, working, awaiting_input, done, error.
--acknowledge: the human has seen the session (a new prompt): it clears Done
and restarts the Escalation of Awaiting Input.
show: puts a PNG, JPEG or GIF on the Device in Raw Mode, scaled to its matrix,
an animated GIF looping, until Ctrl-C; Awaiting Input always shows over it.
get, set, unset: read and change the Preferences in the config file
($CODINO_CONFIG, else $XDG_CONFIG_HOME/codino/config.toml, else
~/.config/codino/config.toml). The Host Daemon applies a change within a
second. get with no KEY lists every Preference. Keys:
  theme                 default
  brightness            1-100, percent of the Device's brightness cap
  layout                adaptive or aggregate
  rotation              0, 90, 180 or 270 (degrees clockwise)
  quiet_mode            true or false: show nothing but Awaiting Input
  done_decay            e.g. 10m: how long Done shows before it decays to Idle
  disconnected_timeout  e.g. 5s (2s-1h): Host silence before Disconnected
  escalation.faster     e.g. 30s: Awaiting Input pulses faster and brighter
  escalation.takeover   e.g. 2m: the waiting session takes the matrix over
  escalation.steady     e.g. 10m: Escalation settles into a steady glow
  project_colors.PATH   #rrggbb pinned to the git repo PATH is in (PATH may be .)
unset returns a Preference to its default.
`

func main() {
	// A hook's events are ordered by when its process started.
	started := time.Now()
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	paths := daemon.DefaultPaths(name)
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "hook":
		hook(paths, os.Stdin, os.Stderr, started) // never fails the hook
	case "event":
		err = event(paths, args)
	case "show":
		err = show(paths, args, os.Stdout)
	case "get":
		err = get(args, os.Stdout)
	case "set":
		err = set(args, os.Stdout)
	case "unset":
		err = unset(args)
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
	acknowledge := fs.Bool("acknowledge", false, "the human has seen the session: clear Done, restart Escalation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Either the session ended, or it changed (a state, an Acknowledge or both).
	if changed := *state != "" || *acknowledge; fs.NArg() > 0 || changed == *end {
		fmt.Fprintf(os.Stderr, "%s event: give --state, --acknowledge or --end\n%s", name, usage)
		return errUsage
	}
	e := host.Event{SessionID: *id, State: host.SessionState(*state), End: *end, Acknowledge: *acknowledge}
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

	client, err := spawningClient(paths)
	if err != nil {
		return err
	}
	return client.Deliver(context.Background(), e)
}

// spawningClient talks to the Host Daemon, spawning one when none runs.
func spawningClient(paths daemon.Paths) (daemon.Client, error) {
	self, err := os.Executable()
	if err != nil {
		return daemon.Client{}, fmt.Errorf("find own binary: %w", err)
	}
	return daemon.Client{Paths: paths, Spawn: daemon.SpawnCommand(paths, self, "daemon")}, nil
}

// show puts a picture on the Device in Raw Mode until Ctrl-C (or SIGTERM),
// then returns the Device to its own rendering. It fails when the Host Daemon
// ends the show first: a newer `codino show` replaced it, or the daemon
// exited.
func show(paths daemon.Paths, args []string, out io.Writer) error {
	fs := newFlags("show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "%s show: give one image file\n%s", name, usage)
		return errUsage
	}
	file := fs.Arg(0)
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("show: %w", err)
	}
	// Fail on a file that is no picture before spawning a daemon for it.
	if _, err := picture.Decode(data); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	client, err := spawningClient(paths)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := client.Show(ctx, data)
	if err != nil {
		return fmt.Errorf("show %s: %w", file, err)
	}
	defer s.Stop()
	if s.Status.Device.Claimed {
		fmt.Fprintf(out, "Showing %s on %s. Ctrl-C to stop.\n", file, s.Status.Device.Name)
	} else {
		fmt.Fprintf(out, "Showing %s once a Device is connected. Ctrl-C to stop.\n", file)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-s.Ended():
		return errors.New("the Host Daemon ended the show: another codino show replaced it, or the daemon exited")
	}
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
	if st.PreferencesProblem != "" {
		fmt.Fprintf(out, "Preferences: the config file is ignored: %s\n", st.PreferencesProblem)
	}
	switch d := st.Device; {
	case d.Claimed:
		fmt.Fprintf(out, "Device: %s (firmware %s, protocol %d)\n", d.Name, d.Firmware, d.Protocol)
	case d.Problem != "":
		fmt.Fprintf(out, "Device: not connected: %s\n", d.Problem)
	default:
		fmt.Fprintln(out, "Device: not connected")
	}
	if st.Showing {
		fmt.Fprintln(out, "Raw Mode: showing a picture (codino show)")
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
	exitGrace := host.DefaultExitGrace
	if env := os.Getenv(exitGraceEnv); env != "" {
		d, err := time.ParseDuration(env)
		if err != nil || d < 0 {
			return fmt.Errorf("$%s: want a duration such as 5m, got %q", exitGraceEnv, env)
		}
		exitGrace = d
	}
	fs.DurationVar(&exitGrace, "exit-grace", exitGrace,
		"exit after this long with no sessions (default $"+exitGraceEnv+" or "+host.DefaultExitGrace.String()+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// A daemon spawned while the previous one is still exiting waits for it,
	// longer than a client waits, so the next event finds it even if this
	// one timed out.
	ln, err := daemon.ListenWithin(paths, takeoverWait)
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
		// Its Preferences cache lives as long as the runtime directory, as a
		// Device's lives in flash.
		devices = devicelink.VirtualDevice{Path: path, Display: os.Stderr,
			Args: []string{"--preferences-file", filepath.Join(paths.Dir, "virtual-device-preferences.jsonl")}}
	}
	prefsFile, err := preferencesFile()
	if err != nil {
		return err
	}
	h := host.New(systemClock{}, devices, host.WithExitGrace(exitGrace), host.WithPreferences(prefsFile),
		host.WithTranscriptProbe(claudecode.Transcripts{}), host.WithLivenessProbe(liveness.Probe{}),
		host.WithProjectRoots(gitrepo.Roots{}))
	defer h.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("Host Daemon listening", "socket", paths.Socket, "virtual_device", *virtual, "exit_grace", exitGrace,
		"preferences", prefsFile.Path)
	if err := daemon.Run(ctx, ln, h); err != nil {
		return err
	}
	select {
	case <-h.Retired():
		slog.Info("no Agent Sessions for the exit grace; exiting", "exit_grace", exitGrace)
	default:
	}
	return nil
}

// takeoverWait bounds how long a new Host Daemon waits for the previous one
// to release its Device and lock: draining clients (up to 2 s) plus closing a
// hung Device (up to 1 s), with margin.
const takeoverWait = 5 * time.Second

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
