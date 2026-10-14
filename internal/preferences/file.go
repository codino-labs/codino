package preferences

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is the config file at Path: canonical for the Preferences. Without
// one every Preference has its default.
type File struct {
	Path string
}

// newFileHeader starts a config file that `codino set` creates.
const newFileHeader = `# Preferences. Every line is optional: a Preference that isn't set here
# keeps its default. "get" on the command line lists them all.
`

// Load reads and validates the Preferences. A missing file gives the
// defaults; an invalid one an error naming the file and the line at fault.
func (f File) Load() (Preferences, error) {
	doc, err := f.read()
	if err != nil {
		return Preferences{}, err
	}
	p, err := fromDocument(doc)
	if err != nil {
		return Preferences{}, fmt.Errorf("%s: %w", f.Path, err)
	}
	return p, nil
}

// read parses the file; an empty document when there is none.
func (f File) read() (document, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return document{}, nil
	}
	if err != nil {
		return document{}, fmt.Errorf("read Preferences: %w", err)
	}
	doc, err := parseDocument(string(data))
	if err != nil {
		return document{}, fmt.Errorf("%s: %w", f.Path, err)
	}
	return doc, nil
}

// List returns every Preference with its current value.
func (f File) List() ([]Setting, error) {
	p, err := f.Load()
	if err != nil {
		return nil, err
	}
	return p.List(), nil
}

// Get returns the current value of the Preference at key.
func (f File) Get(key string) (string, error) {
	if _, err := lookup(splitKey(key)); err != nil {
		return "", err
	}
	p, err := f.Load()
	if err != nil {
		return "", err
	}
	return p.Get(key)
}

// Set sets the Preference at key in the file, creating the file if needed.
// Only that value changes; the rest of the file, comments included, stays as
// it is. It refuses (and leaves the file alone) when the value is invalid or
// the file would still be invalid after the change.
func (f File) Set(key, text string) error {
	s, err := lookup(splitKey(key))
	if err != nil {
		return err
	}
	v, err := parseArgument(s, text)
	if err != nil {
		return err
	}
	// Check the value alone first, for an error about it rather than the file.
	scratch := Defaults()
	if err := s.set(&scratch, v); err != nil {
		return fmt.Errorf("%s: %w", s.key(), err)
	}
	doc, err := f.read()
	if err != nil {
		return err
	}
	if i := doc.find(s.path, samePreference); i >= 0 {
		doc.replaceValue(i, v)
	} else {
		if len(doc.lines) == 0 {
			doc = mustParse(newFileHeader)
		}
		doc.add(s.path, v)
	}
	return f.write(doc)
}

// Unset removes the Preference at key from the file: it returns to its
// default. Nothing changes when the file doesn't set it.
func (f File) Unset(key string) error {
	s, err := lookup(splitKey(key))
	if err != nil {
		return err
	}
	doc, err := f.read()
	if err != nil {
		return err
	}
	i := doc.find(s.path, samePreference)
	if i < 0 {
		return nil
	}
	doc.remove(i)
	return f.write(doc)
}

// samePreference tells whether two key paths name the same Preference:
// pinned Project Colors compare by canonical repo root, so "~/src/app" and
// "/home/me/src/app" are one.
func samePreference(a, b []string) bool {
	if equal(a, b) {
		return true
	}
	if len(a) != 2 || len(b) != 2 || a[0] != projectColorsTable || b[0] != projectColorsTable {
		return false
	}
	ra, errA := canonicalRoot(a[1])
	rb, errB := canonicalRoot(b[1])
	return errA == nil && errB == nil && ra == rb
}

func mustParse(text string) document {
	doc, err := parseDocument(text)
	if err != nil {
		panic(err)
	}
	return doc
}

// write validates the edited file and replaces the old one atomically: a
// temporary file in the same directory renamed over it, so the Host Daemon
// watching it never reads half a file. A symlinked config file (dotfiles)
// stays a symlink: the file it points to is replaced.
func (f File) write(doc document) error {
	if _, err := fromDocument(doc); err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	target := f.Path
	if resolved, err := filepath.EvalSymlinks(f.Path); err == nil {
		target = resolved
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write Preferences: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("write Preferences: %w", err)
	}
	defer os.Remove(tmp.Name()) // after a successful rename there is nothing to remove
	if _, err := tmp.WriteString(doc.text()); err != nil {
		tmp.Close()
		return fmt.Errorf("write Preferences: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("write Preferences: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write Preferences: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("write Preferences: %w", err)
	}
	return nil
}
