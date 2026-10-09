// Package rollback owns the confirm-or-rollback timer: the appliance-code
// piece that makes remote rule changes safe. nftables applies atomically,
// but atomic apply is not automatic rollback — a valid-but-locking ruleset
// stays loaded until someone restores. The manager arms a timer after each
// committed apply; the operator confirms (ax confirm, POST /confirm) before
// it fires, or the restore function runs and the change is undone.
package rollback

import (
	"context"
	"sync"
	"time"
)

// state is the lifecycle of one armed confirmation.
type state int

const (
	stateRunning   state = iota // timer armed, window open
	stateConfirmed              // operator confirmed; timer canceled
	stateFired                  // window expired; restore ran
)

// Manager arms confirmations; at most one is pending at a time. Arming a
// new confirmation supersedes (cancels without firing) any previous one —
// a newer apply owns the state machine from that point.
type Manager struct {
	restoreTimeout time.Duration // cap on a single restore run

	mu      sync.Mutex
	pending *Confirmation
}

// DefaultRestoreTimeout caps a single restore run.
const DefaultRestoreTimeout = 30 * time.Second

// NewManager returns a manager whose restore runs are capped at
// restoreTimeout (0 means 30s).
func NewManager(restoreTimeout time.Duration) *Manager {
	if restoreTimeout <= 0 {
		restoreTimeout = 30 * time.Second
	}
	return &Manager{restoreTimeout: restoreTimeout}
}

// ArmConfirmTimer schedules restore to run after delay. The returned
// Confirmation is the operator's handle: Confirm cancels the rollback;
// Done reports (and blocks until) the confirmation resolved either way.
// Any previously pending confirmation is canceled without firing — the
// caller (the apply pipeline) supersedes it by starting a new apply.
func (m *Manager) ArmConfirmTimer(delay time.Duration, restore func(context.Context) error) *Confirmation {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil {
		m.pending.Confirm() // superseded, not fired
		m.pending = nil
	}
	p := &pending{
		restore:        restore,
		done:           make(chan struct{}),
		restoreTimeout: m.restoreTimeout,
	}
	p.timer = time.AfterFunc(delay, p.fire)
	c := &Confirmation{p: p}
	m.pending = c
	return c
}

// Pending reports whether a confirmation window is open.
func (m *Manager) Pending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending != nil && m.pending.p.state == stateRunning
}

// pending carries one armed timer and its restore function.
type pending struct {
	timer          *time.Timer
	restore        func(context.Context) error
	done           chan struct{}
	restoreTimeout time.Duration

	mu         sync.Mutex
	state      state
	restoreErr error
}

// fire runs the restore (timer-goroutine side) exactly once.
func (p *pending) fire() {
	p.mu.Lock()
	if p.state != stateRunning {
		p.mu.Unlock()
		return
	}
	p.state = stateFired
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), p.restoreTimeout)
	defer cancel()
	p.mu.Lock()
	p.restoreErr = p.restore(ctx)
	p.mu.Unlock()
	close(p.done)
}

// Confirmation is the operator handle for one armed timer.
type Confirmation struct{ p *pending }

// Confirm cancels the pending rollback. It returns false if the
// confirmation already resolved (confirmed or fired).
func (c *Confirmation) Confirm() bool {
	p := c.p
	p.mu.Lock()
	if p.state != stateRunning {
		p.mu.Unlock()
		return false
	}
	p.state = stateConfirmed
	p.timer.Stop()
	p.mu.Unlock()
	close(p.done)
	return true
}

// Done is closed when the confirmation resolves — confirmed by the
// operator or fired (restore completed) by the timer.
func (c *Confirmation) Done() <-chan struct{} { return c.p.done }

// Fired reports whether the rollback fired (as opposed to being confirmed).
func (c *Confirmation) Fired() bool {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	return c.p.state == stateFired
}

// Err returns the restore error if the rollback fired and the restore run
// itself failed. It returns nil while running, after a confirm, or when the
// restore completed successfully.
func (c *Confirmation) Err() error {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	return c.p.restoreErr
}
