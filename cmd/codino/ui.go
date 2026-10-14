package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/codino-labs/codino/internal/daemon"
	"github.com/codino-labs/codino/internal/protocol"
)

// uiTimeout bounds `codino ui`: spawning a Host Daemon and starting its web
// UI.
const uiTimeout = 5 * time.Second

// ui opens the web UI in the browser, starting the Host Daemon if needed.
// The daemon serves the page on 127.0.0.1 while it is open and stays up
// meanwhile; the command returns at once.
func ui(paths daemon.Paths, args []string, out io.Writer) error {
	fs := newFlags("ui")
	noBrowser := fs.Bool("no-browser", false, "print the URL rather than opening the browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s ui: takes no arguments\n%s", name, usage)
		return errUsage
	}
	client, err := spawningClient(paths)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiTimeout)
	defer cancel()
	url, err := client.OpenUI(ctx)
	if err != nil {
		return fmt.Errorf("ui: %w", err)
	}
	if !*noBrowser {
		if err := openBrowser(url); err == nil {
			fmt.Fprintln(out, "Opened the Codino web UI in your browser. It closes when you close the page.")
			return nil
		} else {
			fmt.Fprintf(out, "Could not open a browser (%v).\n", err)
		}
	}
	fmt.Fprintf(out, "Open this link once, within 2 minutes (it works only once):\n%s\n", url)
	return nil
}

// openBrowser opens url in the user's browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it; the browser outlives this command
	return nil
}

// themeCopies keeps a copy of each Theme and Sprite Pack the Host Daemon
// installed on the Device, as its install line, in
// <user cache dir>/codino/themes/<kind>/<name>.jsonl: the Device's flash is
// canonical and can't be read back, so the web UI's preview renders these.
// A lost copy only means the preview shows "default" for that Theme.
type themeCopies struct{ dir string }

func newThemeCopies() (themeCopies, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return themeCopies{}, fmt.Errorf("theme copies: %w", err)
	}
	return themeCopies{dir: filepath.Join(cache, name, "themes")}, nil
}

func (c themeCopies) path(kind protocol.Kind, name string) string {
	return filepath.Join(c.dir, string(kind), name+".jsonl")
}

func (c themeCopies) Load() []protocol.Install {
	var out []protocol.Install
	for _, kind := range []protocol.Kind{protocol.KindSpritePack, protocol.KindTheme} {
		entries, err := os.ReadDir(filepath.Join(c.dir, string(kind)))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("theme copies", "err", err)
			}
			continue
		}
		for _, e := range entries {
			n, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if !ok || !protocol.ValidName(n) {
				continue
			}
			data, err := os.ReadFile(c.path(kind, n))
			if err != nil {
				slog.Warn("theme copies", "err", err)
				continue
			}
			install, err := protocol.NewInstall(data)
			if err != nil || install.Kind != kind || install.Name != n {
				slog.Warn("theme copies: ignoring an invalid copy", "file", c.path(kind, n), "err", err)
				continue
			}
			out = append(out, install)
		}
	}
	return out
}

func (c themeCopies) Save(install protocol.Install) error {
	line, err := protocol.Encode(install)
	if err != nil {
		return err
	}
	path := c.path(install.Kind, install.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("theme copies: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("theme copies: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(line); err != nil {
		tmp.Close()
		return fmt.Errorf("theme copies: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("theme copies: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

func (c themeCopies) Remove(kind protocol.Kind, name string) error {
	if err := os.Remove(c.path(kind, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("theme copies: %w", err)
	}
	return nil
}
