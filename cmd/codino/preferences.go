package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/codino-labs/codino/internal/gitrepo"
	"github.com/codino-labs/codino/internal/preferences"
)

// preferencesFileEnv overrides where the config file is; a Host Daemon spawned by a
// hook inherits it.
const preferencesFileEnv = "CODINO_CONFIG"

// preferencesFile is the Preferences' config file: $CODINO_CONFIG, else
// $XDG_CONFIG_HOME/codino/config.toml, else ~/.config/codino/config.toml (the
// user's config directory on Windows). It need not exist.
func preferencesFile() (preferences.File, error) {
	if path := os.Getenv(preferencesFileEnv); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return preferences.File{}, fmt.Errorf("$%s: %w", preferencesFileEnv, err)
		}
		return preferences.File{Path: abs}, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return preferences.File{Path: filepath.Join(xdg, name, "config.toml")}, nil
	}
	var dir string
	if runtime.GOOS == "windows" {
		d, err := os.UserConfigDir()
		if err != nil {
			return preferences.File{}, fmt.Errorf("config file: %w", err)
		}
		dir = d
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return preferences.File{}, fmt.Errorf("config file: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return preferences.File{Path: filepath.Join(dir, name, "config.toml")}, nil
}

// projectColorsKey is the prefix of the keys pinning a Project Color.
const projectColorsKey = "project_colors."

// preferenceKey resolves the project of a project_colors.PATH key to the
// repo root it is pinned by, as the Host colors sessions: PATH may be
// relative (".") or anywhere in the repo. A PATH starting with ~ is kept for
// the config file to read.
func preferenceKey(key string) (string, error) {
	project, ok := strings.CutPrefix(key, projectColorsKey)
	if !ok || strings.HasPrefix(project, "~") {
		return key, nil
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return projectColorsKey + gitrepo.Roots{}.Root(abs), nil
}

// preferencesCommand parses the arguments of get, set or unset: between min
// and max of them, the first a KEY (resolved by preferenceKey). It returns
// the config file and the arguments.
func preferencesCommand(cmd string, args []string, min, max int, want string) (preferences.File, []string, error) {
	fs := newFlags(cmd)
	if err := fs.Parse(args); err != nil {
		return preferences.File{}, nil, err
	}
	if fs.NArg() < min || fs.NArg() > max {
		fmt.Fprintf(os.Stderr, "%s %s: give %s\n%s", name, cmd, want, usage)
		return preferences.File{}, nil, errUsage
	}
	f, err := preferencesFile()
	if err != nil {
		return preferences.File{}, nil, err
	}
	args = fs.Args()
	if len(args) > 0 {
		if args[0], err = preferenceKey(args[0]); err != nil {
			return preferences.File{}, nil, err
		}
	}
	return f, args, nil
}

// get prints one Preference's value, or every Preference as KEY = VALUE.
func get(args []string, out io.Writer) error {
	f, args, err := preferencesCommand("get", args, 0, 1, "at most one KEY")
	if err != nil {
		return err
	}
	if len(args) == 0 {
		all, err := f.List()
		if err != nil {
			return err
		}
		for _, e := range all {
			fmt.Fprintf(out, "%s = %s\n", e.Key, e.Value)
		}
		return nil
	}
	v, err := f.Get(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(out, v)
	return nil
}

// set changes one Preference in the config file.
func set(args []string, out io.Writer) error {
	f, args, err := preferencesCommand("set", args, 2, 2, "KEY and VALUE")
	if err != nil {
		return err
	}
	if err := f.Set(args[0], args[1]); err != nil {
		return err
	}
	v, err := f.Get(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s = %s (in %s)\n", args[0], v, f.Path)
	return nil
}

// unset returns one Preference to its default.
func unset(args []string) error {
	f, args, err := preferencesCommand("unset", args, 1, 1, "one KEY")
	if err != nil {
		return err
	}
	return f.Unset(args[0])
}
