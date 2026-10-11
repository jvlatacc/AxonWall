package rollback

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls done with a deadline; tests fail if the confirmation does
// not resolve in time.
func waitFor(t *testing.T, c *Confirmation) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation did not resolve in time")
	}
}

// TestRollbackFiresAndRestores: an unconfirmed apply is rolled back when
// the window expires — restore runs exactly once.
func TestRollbackFiresAndRestores(t *testing.T) {
	var runs atomic.Int32
	m := NewManager(time.Second)
	c := m.ArmConfirmTimer(20*time.Millisecond, func(context.Context) error {
		runs.Add(1)
		return nil
	})
	if !m.Pending() {
		t.Fatal("Pending() should be true while the window is open")
	}
	waitFor(t, c)
	if !c.Fired() {
		t.Error("confirmation should report Fired after the window expired")
	}
	if c.Err() != nil {
		t.Errorf("restore error should be nil, got %v", c.Err())
	}
	if runs.Load() != 1 {
		t.Errorf("restore ran %d times, want 1", runs.Load())
	}
	if m.Pending() {
		t.Error("Pending() should be false after the window resolved")
	}
}

// TestConfirmCancelsRollback: a confirmed apply never has its restore run.
func TestConfirmCancelsRollback(t *testing.T) {
	var ran atomic.Bool
	m := NewManager(time.Second)
	c := m.ArmConfirmTimer(30*time.Millisecond, func(context.Context) error {
		ran.Store(true)
		return nil
	})
	if !c.Confirm() {
		t.Fatal("first Confirm should succeed")
	}
	if c.Confirm() {
		t.Error("second Confirm should report already resolved")
	}
	waitFor(t, c)
	if c.Fired() {
		t.Error("confirmed confirmation must not report Fired")
	}
	time.Sleep(60 * time.Millisecond) // let a buggy timer fire if it would
	if ran.Load() {
		t.Error("restore ran after confirm")
	}
}

// TestRearmSupersedesPending: arming a new confirmation cancels the old one
// without firing — a newer apply owns the state machine.
func TestRearmSupersedesPending(t *testing.T) {
	var oldRan atomic.Bool
	m := NewManager(time.Second)
	old := m.ArmConfirmTimer(30*time.Millisecond, func(context.Context) error {
		oldRan.Store(true)
		return nil
	})
	_ = m.ArmConfirmTimer(time.Hour, func(context.Context) error { return nil })
	waitFor(t, old) // resolves as superseded
	if old.Fired() {
		t.Error("superseded confirmation must not report Fired")
	}
	time.Sleep(80 * time.Millisecond)
	if oldRan.Load() {
		t.Error("superseded restore ran")
	}
	if !m.Pending() {
		t.Error("new confirmation should be pending")
	}
}

// TestRestoreErrorRetained: a failed restore surfaces its error through Err.
func TestRestoreErrorRetained(t *testing.T) {
	want := errors.New("nft restore failed")
	m := NewManager(time.Second)
	c := m.ArmConfirmTimer(20*time.Millisecond, func(context.Context) error { return want })
	waitFor(t, c)
	if !errors.Is(c.Err(), want) {
		t.Errorf("Err() = %v, want %v", c.Err(), want)
	}
}

// TestRestoreRunsUnderTimeout: the restore context is capped at the
// manager's restore timeout.
func TestRestoreRunsUnderTimeout(t *testing.T) {
	m := NewManager(50 * time.Millisecond)
	c := m.ArmConfirmTimer(10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done() // restore would block forever on its own
		return ctx.Err()
	})
	waitFor(t, c)
	if !errors.Is(c.Err(), context.DeadlineExceeded) {
		t.Errorf("restore should fail with DeadlineExceeded, got %v", c.Err())
	}
}

// TestNewManagerDefaults: a zero timeout falls back to the default cap.
func TestNewManagerDefaults(t *testing.T) {
	m := NewManager(0)
	if m.restoreTimeout != DefaultRestoreTimeout {
		t.Errorf("restoreTimeout = %v, want %v", m.restoreTimeout, DefaultRestoreTimeout)
	}
}
