package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// ErrTxDone is returned when a finished transaction is reused.
var ErrTxDone = errors.New("store: transaction already finished")

// ConflictError reports an optimistic-concurrency failure: the store moved
// to a different revision while this transaction was open.
type ConflictError struct {
	Base    string
	Current string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("store: config changed since transaction began (base %s, current %s)", e.Base, e.Current)
}

// Tx is an in-flight configuration transaction. Its candidate starts as a
// copy of the committed configuration; the API layer mutates it and hands
// it to the applier. The applier is the sole committer: Commit is called
// only after the rendered configuration has been applied to the system and
// passed health checks, so the store never claims state the kernel never
// ran. Abandoning a transaction (Abort) leaves the store untouched.
type Tx struct {
	store     *Store
	base      string
	candidate *config.Config
	done      bool
}

// Config returns the transaction's candidate configuration. It is a copy
// from Begin time; callers mutate it and then call Set to validate.
func (tx *Tx) Config() *config.Config { return tx.candidate }

// Set replaces the candidate configuration, validating it immediately so
// invalid intent fails before any rendering or apply work happens.
func (tx *Tx) Set(c *config.Config) error {
	if tx.done {
		return ErrTxDone
	}
	if c == nil {
		return fmt.Errorf("store: candidate config must not be nil")
	}
	if err := config.Validate(c); err != nil {
		return err
	}
	tx.candidate = c
	return nil
}

// BaseRev returns the revision the transaction started from.
func (tx *Tx) BaseRev() string { return tx.base }

// Commit writes the candidate configuration and commits it to the store's
// git history. The applier calls this only after a successful apply; see
// the type doc. Commit fails with *ConflictError if the store changed since
// Begin — the candidate must then be re-based and re-applied.
func (tx *Tx) Commit(msg string) (string, error) {
	if tx.done {
		return "", ErrTxDone
	}
	if msg == "" {
		return "", fmt.Errorf("store: commit message must not be empty")
	}
	s := tx.store
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	current, err := s.Rev()
	if err != nil {
		return "", err
	}
	if current != tx.base {
		return "", &ConflictError{Base: tx.base, Current: current}
	}

	old, err := os.ReadFile(s.path())
	if err != nil {
		return "", fmt.Errorf("store: read current: %w", err)
	}
	if err := s.writeFile(tx.candidate); err != nil {
		return "", err
	}
	if _, err := s.runGitOut("add", FileName); err != nil {
		_ = restore(s.path(), old)
		return "", err
	}
	staged, err := s.runGitOut("diff", "--cached", "--name-only")
	if err != nil {
		_ = restore(s.path(), old)
		return "", err
	}
	if staged == "" {
		// Nothing staged: the candidate is identical to the committed config.
		tx.done = true
		return tx.base, nil
	}
	if _, err := s.runGitOut("commit", "-q", "-m", msg); err != nil {
		_ = restore(s.path(), old)
		return "", err
	}
	tx.done = true
	newRev, err := s.Rev()
	if err != nil {
		return "", err
	}
	return newRev, nil
}

// Abort abandons the transaction without writing anything. Aborting a
// finished transaction is a no-op.
func (tx *Tx) Abort() {
	tx.done = true
}
