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

func themeList(client daemon.Client, args []string, out io.Writer) error {
	fs := newFlags("theme list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s theme list: takes no arguments\n%s", name, usage)
		return errUsage
	}
	deadline := time.Now().Add(deviceWait)
	for {
		st, err := client.Wake(context.Background())
		if err != nil {
			return err
		}
		if st.Device.Claimed {
			printThemes(out, st)
			return nil
		}
		if time.Now().After(deadline) {
			return noDevice(st)
		}
		time.Sleep(200 * time.Millisecond)
	}
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
	if !slices.Contains(st.Device.Themes, st.Theme) {
		fmt.Fprintf(out, "theme = %s is not on the Device: it shows %q instead.\n", st.Theme, "default")
	}
	fmt.Fprintf(out, "Sprite Packs: %s\n", strings.Join(st.Device.SpritePacks, ", "))
}

func noDevice(st host.Status) error {
	if st.Device.Problem != "" {
		return fmt.Errorf("no Device connected: %s", st.Device.Problem)
	}
	return errors.New("no Device connected")
}

// untilClaimed retries an install while the Host Daemon has no Device yet.
func untilClaimed(client daemon.Client, do func() (protocol.InstallReply, error)) (protocol.InstallReply, error) {
	deadline := time.Now().Add(deviceWait)
	for {
		reply, err := do()
		if !errors.Is(err, daemon.ErrNoDevice) || time.Now().After(deadline) {
			if errors.Is(err, daemon.ErrNoDevice) {
				if st, serr := client.Status(context.Background()); serr == nil {
					return reply, noDevice(st)
				}
			}
			return reply, err
		}
		time.Sleep(200 * time.Millisecond)
	}
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
	install, err := protocol.NewInstall(data)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	reply, err := untilClaimed(client, func() (protocol.InstallReply, error) {
		return client.Install(context.Background(), data)
	})
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("the Device refused %s %s: %s", kindName(install.Kind), install.Name, reply.Error)
	}
	fmt.Fprintf(out, "Installed %s %s.\n", kindName(install.Kind), install.Name)
	if install.Kind == protocol.KindTheme {
		fmt.Fprintf(out, "Show it with: codino set theme %s\n", install.Name)
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
	reply, err := untilClaimed(client, func() (protocol.InstallReply, error) {
		return client.Uninstall(context.Background(), kind, fs.Arg(0))
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
