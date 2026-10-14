package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// pluginDataEnv is the Claude Code plugin's persistent data directory,
// exported to the plugin's hooks; a Host Daemon a hook spawns inherits it.
const pluginDataEnv = "CLAUDE_PLUGIN_DATA"

// stateDir is where the Host Daemon keeps persistent Host state (the Theme
// copies): the plugin's data directory, which survives plugin updates (the
// plugin root doesn't), when the daemon was started by the plugin's hooks,
// as it is while any Claude Code session is open; else, for a daemon started
// outside Claude Code (another Agent's `codino event`, development),
// <user cache dir>/codino. The Preferences are not Host state: they are the
// user's config file (preferencesFile).
func stateDir() (string, error) {
	if dir := os.Getenv(pluginDataEnv); filepath.IsAbs(dir) {
		return dir, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("Host state directory: %w", err)
	}
	return filepath.Join(cache, name), nil
}
