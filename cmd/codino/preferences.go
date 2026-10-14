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

// configEnv overrides where the config file is; a Host Daemon spawned by a
// hook inherits it.
const configEnv = "CODINO_CONFIG"

// configFile is the Preferences' config file: $CODINO_CONFIG, else
// $XDG_CONFIG_HOME/codino/config.toml, else ~/.config/codino/config.toml (the
// user's config directory on Windows). It need not exist.
func configFile() (preferences.File, error) {
	if path := os.Getenv(configEnv); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return preferences.File{}, fmt.Errorf("$%s: %w", configEnv, err)
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

// get prints one Preference's value, or every Preference as KEY = VALUE.
func get(args []string, out io.Writer) error {
	fs := newFlags("get")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := configFile()
	if err != nil {
		return err
	}
	switch fs.NArg() {
	case 0:
		all, err := f.List()
		if err != nil {
			return err
		}
		for _, s := range all {
			fmt.Fprintf(out, "%s = %s\n", s.Key, s.Value)
		}
		return nil
	case 1:
		key, err := preferenceKey(fs.Arg(0))
		if err != nil {
			return err
		}
		v, err := f.Get(key)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, v)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "%s get: give at most one KEY\n%s", name, usage)
		return errUsage
	}
}

// set changes one Preference in the config file.
func set(args []string, out io.Writer) error {
	fs := newFlags("set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "%s set: give KEY and VALUE\n%s", name, usage)
		return errUsage
	}
	f, err := configFile()
	if err != nil {
		return err
	}
	key, err := preferenceKey(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := f.Set(key, fs.Arg(1)); err != nil {
		return err
	}
	v, err := f.Get(key)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s = %s (in %s)\n", key, v, f.Path)
	return nil
}

// unset returns one Preference to its default.
func unset(args []string) error {
	fs := newFlags("unset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "%s unset: give one KEY\n%s", name, usage)
		return errUsage
	}
	f, err := configFile()
	if err != nil {
		return err
	}
	key, err := preferenceKey(fs.Arg(0))
	if err != nil {
		return err
	}
	return f.Unset(key)
}
