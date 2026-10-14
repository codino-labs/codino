// Package gitrepo finds the git repo a directory belongs to by reading the
// files git writes, without running git: the hook binary and the Host Daemon
// stay fast and need nothing installed.
package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
)

// Roots is the Host's ProjectRootProbe: it finds the repo root a project
// directory is colored by.
type Roots struct{}

// Root returns the root of the git repo dir is in, the same for every
// worktree of the repo: the main worktree's directory, or the bare repo for
// worktrees of a bare repo. A submodule is a repo of its own. It returns dir
// itself when dir isn't in a repo.
func (Roots) Root(dir string) string {
	dir = filepath.Clean(dir)
	for d := dir; ; {
		dotGit := filepath.Join(d, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			if info.IsDir() {
				return d
			}
			return worktreeRoot(d, dotGit)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// worktreeRoot reads the .git file of the worktree at dir ("gitdir: <path>")
// and returns the root of the repo it belongs to: the parent of the common
// git dir its commondir file names, or dir itself when it has none (a
// submodule) or the file can't be read.
func worktreeRoot(dir, dotGit string) string {
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return dir
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return dir
	}
	gitDir = resolve(dir, strings.TrimSpace(gitDir))
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return dir
	}
	commonDir := resolve(gitDir, strings.TrimSpace(string(common)))
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir // a bare repo
}

// resolve makes path, relative to base unless absolute, absolute and clean.
func resolve(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
