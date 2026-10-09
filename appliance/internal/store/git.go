package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitArgs builds the argument prefix for running git against the store
// directory. The pinned identity keeps commits independent of any ambient
// global git config on the appliance or in CI.
func gitArgs(dir string, args ...string) []string {
	return append([]string{
		"-c", "user.name=AxonWall",
		"-c", "user.email=axon@localhost",
		"--git-dir=" + filepath.Join(dir, ".git"),
		"--work-tree=" + dir,
	}, args...)
}

// runGitDir runs a git command against dir's repository and returns
// trimmed stdout.
func runGitDir(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", gitArgs(dir, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs a git command against a directory that is not (yet) a store.
func runGit(dir string, args ...string) error {
	_, err := runGitDir(dir, args...)
	return err
}

// runGitOut runs a git command against the store's repository.
func (s *Store) runGitOut(args ...string) (string, error) {
	return runGitDir(s.dir, args...)
}

// restore writes old content back after a failed commit so the working
// file never disagrees with HEAD.
func restore(path string, old []byte) error {
	return os.WriteFile(path, old, 0o600)
}
