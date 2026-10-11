package store

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Replace swaps the store's managed contents — axonwall.yaml and the git
// history — with those of from, a directory laid out like a store (a git
// work tree plus the config file), typically a staged import prepared by
// the backup package. Files outside the store's ownership are left
// untouched.
//
// It holds the commit lock, so a Replace cannot interleave with a
// configuration commit; transactions begun before the swap fail their
// optimistic-concurrency check against the new history and must be
// retried. The daemon additionally serializes Replace against the apply
// pipeline at the API layer.
func (s *Store) Replace(from string) error {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	for _, path := range []string{s.path(), s.path() + ".tmp", filepath.Join(s.dir, ".git")} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("store: wipe %s: %w", path, err)
		}
	}
	if err := copyTree(from, s.dir); err != nil {
		return fmt.Errorf("store: replace from %s: %w", from, err)
	}
	return nil
}

// copyTree copies the file tree at src into dst, preserving directory
// and file modes. Symlinks and other special files are refused: store
// trees contain none, and silently restoring one would be a foothold.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("store: staged %s: not a regular file", rel)
		}
	})
}

// copyFile copies one file, creating dst with the source's mode.
func copyFile(src, dst string, mode fs.FileMode) (err error) {
	in, err := os.Open(src) //nolint:gosec // path is the staged store tree built by the backup package
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
