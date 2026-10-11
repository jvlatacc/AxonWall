// Package store implements the git-backed configuration store at /config.
//
// The store owns exactly one file, axonwall.yaml, inside a git working tree
// that lives on the persistent /config partition (in tests and development
// the directory is injectable). It is transactional with optimistic
// concurrency: readers load the current revision, writers Begin a
// transaction, and Commit fails if HEAD moved since the transaction began.
//
// The applier is the sole committer: Tx.Commit is only called after the
// rendered configuration has been applied to the system and passed health
// checks, so a git commit always reflects running state.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// FileName is the configuration file the store manages, relative to the
// store directory.
const FileName = "axonwall.yaml"

// DefaultDir is the on-disk location of the config store on the appliance.
const DefaultDir = "/config"

// Store is the git-backed configuration store.
type Store struct {
	dir string

	// commitMu serializes file writes and git commits inside the store
	// directory so working-tree state never disagrees with HEAD.
	commitMu sync.Mutex
}

// Open opens an existing or empty store directory, creating the git dir
// when missing. The directory becomes a git work tree containing
// axonwall.yaml. Use Init to seed a fresh store with an initial
// configuration. A pre-existing axonwall.yaml with no git history is
// adopted into an initial commit — the recovery path for a config
// partition restored without .git — so the working file is never
// uncommitted.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("store: directory must not be empty")
	}
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if err := runGit(dir, "init", "-q", "--initial-branch=main", "."); err != nil {
			return nil, fmt.Errorf("store: init %s: %w", dir, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("store: stat %s: %w", gitDir, err)
	}
	s := &Store{dir: dir}
	if _, err := os.Stat(s.path()); err == nil {
		if _, revErr := s.Rev(); revErr != nil {
			if _, err := s.runGitOut("add", FileName); err != nil {
				return nil, err
			}
			if _, err := s.runGitOut("commit", "-q", "-m", "adopt existing config"); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// Init creates a fresh store at dir and seeds it with cfg as the initial
// commit. It fails if the store already contains a committed axonwall.yaml.
func Init(dir string, cfg *config.Config) (*Store, error) {
	s, err := Open(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, FileName)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("store: %s already exists", path)
	}
	if err := s.writeFile(cfg); err != nil {
		return nil, err
	}
	if _, err := s.runGitOut("add", FileName); err != nil {
		return nil, err
	}
	if _, err := s.runGitOut("commit", "-q", "-m", "Initial configuration"); err != nil {
		return nil, err
	}
	return s, nil
}

// Load returns the current committed configuration and its git revision.
func (s *Store) Load() (*config.Config, string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, FileName))
	if err != nil {
		return nil, "", fmt.Errorf("store: read: %w", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, "", fmt.Errorf("store: %s does not parse: %w", FileName, err)
	}
	rev, err := s.Rev()
	if err != nil {
		return nil, "", err
	}
	return cfg, rev, nil
}

// Rev returns the current HEAD revision of the store.
func (s *Store) Rev() (string, error) {
	out, err := s.runGitOut("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Begin starts a configuration transaction from the current revision. The
// transaction's candidate configuration starts as a copy of the committed
// one; the caller mutates it (or replaces it with Set) and hands it to the
// applier. Only the applier calls Commit.
func (s *Store) Begin() (*Tx, error) {
	cfg, rev, err := s.Load()
	if err != nil {
		return nil, err
	}
	return &Tx{store: s, base: rev, candidate: cfg}, nil
}

// CommitEvent records an operational event in the store's history without
// changing the configuration: it commits the current working tree even
// when it is identical to HEAD, so the event always produces a new
// history node. Config backup restore uses it to mark a restore. Unlike
// Tx.Commit it bypasses the apply-before-commit discipline, so callers
// must only use it for lifecycle events the applier has already made
// true on the system.
func (s *Store) CommitEvent(msg string) (string, error) {
	if msg == "" {
		return "", fmt.Errorf("store: commit message must not be empty")
	}
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	if _, err := s.runGitOut("add", "-A", FileName); err != nil {
		return "", err
	}
	if _, err := s.runGitOut("commit", "--allow-empty", "-q", "-m", msg); err != nil {
		return "", err
	}
	return s.Rev()
}

// writeFile marshals and validates cfg, then writes it into the store
// directory. Callers hold the commit mutex.
func (s *Store) writeFile(cfg *config.Config) error {
	out, err := config.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("store: refusing to write invalid config: %w", err)
	}
	path := filepath.Join(s.dir, FileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("store: rename into place: %w", err)
	}
	return nil
}

// Dir returns the directory backing the store.
func (s *Store) Dir() string { return s.dir }

// Join returns the path of the managed configuration file inside dir.
func Join(dir string) string { return filepath.Join(dir, FileName) }

// path returns the absolute path of the managed configuration file.
func (s *Store) path() string { return Join(s.dir) }
