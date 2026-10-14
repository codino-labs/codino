package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/protocol"
)

// deviceWait is how long a theme command waits for the Host Daemon to claim
// a Device: one it spawned claims within a second or two.
const deviceWait = 6 * time.Second

// theme runs `codino theme list|install|uninstall`.
func theme(paths daemon.Paths, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "%s theme: give list, install or uninstall\n%s", name, usage)
		return errUsage
	}
	client, err := spawningClient(paths)
	if err != nil {
		return err
	}
	switch sub, args := args[0], args[1:]; sub {
	case "list":
		return themeList(client, args, out)
	case "install":
		return themeInstall(client, args, out)
	case "uninstall":
		return themeUninstall(client, args, out)
	default:
		fmt.Fprintf(os.Stderr, "%s theme: unknown subcommand %q\n%s", name, sub, usage)
		return errUsage
	}
}

// withDevice calls do until it no longer fails with daemon.ErrNoDevice, for
// up to deviceWait: a Host Daemon just spawned hasn't claimed the Device yet.
// It then explains why no Device is connected, if none is.
func withDevice(client daemon.Client, do func() error) error {
	deadline := time.Now().Add(deviceWait)
	for {
		err := do()
		if !errors.Is(err, daemon.ErrNoDevice) {
			return err
		}
		if time.Now().After(deadline) {
			if st, serr := client.Status(context.Background()); serr == nil && st.Device.Problem != "" {
				return fmt.Errorf("%w: %s", err, st.Device.Problem)
			}
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func themeList(client daemon.Client, args []string, out io.Writer) error {
	fs := newFlags("theme list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s theme list: takes no arguments\n%s", name, usage)
		return errUsage
	}
	return withDevice(client, func() error {
		st, err := client.StatusSpawning(context.Background())
		if err != nil {
			return err
		}
		if !st.Device.Claimed {
			return daemon.ErrNoDevice
		}
		printThemes(out, st)
		return nil
	})
}

func printThemes(out io.Writer, st host.Status) {
	fmt.Fprintf(out, "Themes on %s (* is the theme Preference):\n", st.Device.Name)
	for _, t := range st.Device.Themes {
		mark := " "
		if t == st.Theme {
			mark = "*"
		}
		fmt.Fprintf(out, "%s %s\n", mark, t)
	}
	if missing := themeMissing(st); missing != "" {
		fmt.Fprintln(out, missing)
	}
	fmt.Fprintf(out, "Sprite Packs: %s\n", strings.Join(st.Device.SpritePacks, ", "))
}

// themeMissing warns when the theme Preference names a Theme the claimed
// Device doesn't have; empty otherwise.
func themeMissing(st host.Status) string {
	d := st.Device
	if !d.Claimed || len(d.Themes) == 0 || slices.Contains(d.Themes, st.Theme) {
		return ""
	}
	return fmt.Sprintf("Theme: %s is not on the Device, which shows \"default\" (codino theme list)", st.Theme)
}

func themeInstall(client daemon.Client, args []string, out io.Writer) error {
	fs := newFlags("theme install")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "%s theme install: give one Theme or Sprite Pack file\n%s", name, usage)
		return errUsage
	}
	file := fs.Arg(0)
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("theme install: %w", err)
	}
	var reply protocol.InstallReply
	err = withDevice(client, func() (err error) {
		reply, err = client.Install(context.Background(), data)
		return err
	})
	var invalid *protocol.InvalidInstallError
	if errors.As(err, &invalid) {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("the Device refused %s %s: %s", kindName(reply.Kind), reply.Name, reply.Error)
	}
	fmt.Fprintf(out, "Installed %s %s.\n", kindName(reply.Kind), reply.Name)
	if reply.Kind == protocol.KindTheme {
		fmt.Fprintf(out, "Show it with: codino set theme %s\n", reply.Name)
	}
	return nil
}

func themeUninstall(client daemon.Client, args []string, out io.Writer) error {
	fs := newFlags("theme uninstall")
	spritePack := fs.Bool("sprite-pack", false, "uninstall a Sprite Pack rather than a Theme")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !protocol.ValidName(fs.Arg(0)) {
		fmt.Fprintf(os.Stderr, "%s theme uninstall: give one Theme (or --sprite-pack) name\n%s", name, usage)
		return errUsage
	}
	kind := protocol.KindTheme
	if *spritePack {
		kind = protocol.KindSpritePack
	}
	var reply protocol.InstallReply
	err := withDevice(client, func() (err error) {
		reply, err = client.Uninstall(context.Background(), kind, fs.Arg(0))
		return err
	})
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("the Device refused to uninstall %s %s: %s", kindName(kind), fs.Arg(0), reply.Error)
	}
	fmt.Fprintf(out, "Uninstalled %s %s.\n", kindName(kind), fs.Arg(0))
	return nil
}

func kindName(k protocol.Kind) string {
	if k == protocol.KindSpritePack {
		return "Sprite Pack"
	}
	return "Theme"
}
