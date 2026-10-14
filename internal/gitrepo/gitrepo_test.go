package gitrepo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codino-labs/codino/internal/gitrepo"
)

// write creates a file with its parent directories.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// The layouts below are what git writes; the tests build them by hand so they
// don't need git installed.

func TestRootOfADirectoryInARepoIsTheRepoRoot(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "app", ".git", "HEAD"), "ref: refs/heads/main\n")
	mkdir(t, filepath.Join(src, "app", "cmd", "server"))

	for _, dir := range []string{filepath.Join(src, "app"), filepath.Join(src, "app", "cmd", "server")} {
		if got, want := (gitrepo.Roots{}).Root(dir), filepath.Join(src, "app"); got != want {
			t.Errorf("Root(%s) = %s, want %s", dir, got, want)
		}
	}
}

func TestRootOfADirectoryOutsideAnyRepoIsTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	mkdir(t, dir)

	if got := (gitrepo.Roots{}).Root(dir); got != dir {
		t.Errorf("Root(%s) = %s, want the directory itself", dir, got)
	}
}

func TestEveryWorktreeOfARepoHasTheMainRepoRoot(t *testing.T) {
	src := t.TempDir()
	main := filepath.Join(src, "app")
	write(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	// git worktree add ../app-feature-x
	write(t, filepath.Join(main, ".git", "worktrees", "app-feature-x", "commondir"), "../..\n")
	write(t, filepath.Join(src, "app-feature-x", ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "app-feature-x")+"\n")
	// with worktree.useRelativePaths
	write(t, filepath.Join(main, ".git", "worktrees", "relative", "commondir"), "../..\n")
	write(t, filepath.Join(src, "wt", "relative", ".git"), "gitdir: ../../app/.git/worktrees/relative\n")
	mkdir(t, filepath.Join(src, "wt", "relative", "internal"))

	for _, dir := range []string{filepath.Join(src, "app-feature-x"), filepath.Join(src, "wt", "relative", "internal")} {
		if got := (gitrepo.Roots{}).Root(dir); got != main {
			t.Errorf("Root(%s) = %s, want the main repo %s", dir, got, main)
		}
	}
}

func TestAWorktreeOfABareRepoHasTheBareRepoAsRoot(t *testing.T) {
	src := t.TempDir()
	bare := filepath.Join(src, "app.git")
	write(t, filepath.Join(bare, "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(bare, "worktrees", "main", "commondir"), "../..\n")
	write(t, filepath.Join(src, "main", ".git"), "gitdir: "+filepath.Join(bare, "worktrees", "main")+"\n")

	if got := (gitrepo.Roots{}).Root(filepath.Join(src, "main")); got != bare {
		t.Errorf("Root = %s, want %s", got, bare)
	}
}

func TestASubmoduleIsARepoOfItsOwn(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "app", ".git", "modules", "lib", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(src, "app", "lib", ".git"), "gitdir: ../.git/modules/lib\n")

	if got, want := (gitrepo.Roots{}).Root(filepath.Join(src, "app", "lib")), filepath.Join(src, "app", "lib"); got != want {
		t.Errorf("Root = %s, want %s", got, want)
	}
}

func TestAnUnreadableGitFileMakesItsDirectoryTheRoot(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "odd", ".git"), "not a gitdir line\n")
	mkdir(t, filepath.Join(src, "odd", "sub"))

	if got, want := (gitrepo.Roots{}).Root(filepath.Join(src, "odd", "sub")), filepath.Join(src, "odd"); got != want {
		t.Errorf("Root = %s, want %s", got, want)
	}
}
