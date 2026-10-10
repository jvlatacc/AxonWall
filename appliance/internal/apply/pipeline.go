package apply

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
	"github.com/jvlatacc/AxonWall/appliance/internal/rollback"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// DefaultConfirmWindow is how long a committed change may run unconfirmed
// before the confirm-or-rollback timer undoes it.
const DefaultConfirmWindow = 5 * time.Minute

// Store is what the pipeline needs of the config store: opening rollback
// transactions that revert an unconfirmed change.
type Store interface {
	Begin() (*store.Tx, error)
}

// Health verifies the appliance is still operable after an apply. The
// health.Checker implements it; a function adapts any other implementation.
type Health interface {
	SelfCheck(ctx context.Context) error
}

// Pipeline is the full apply flow axond consults between validation and
// commit: render → nft -c check → stage known-good → atomic replace →
// service reload → health check. Any failure restores the known-good
// ruleset before returning. After the caller commits the store change,
// ArmRollback arms the confirm-or-rollback timer; until the operator
// confirms, a fired timer restores the previous ruleset and appends a
// rollback commit to the store.
type Pipeline struct {
	// ConfirmWindow is the confirmation window for committed applies.
	ConfirmWindow time.Duration
	// Reload re-materializes service configs (systemd-networkd units,
	// dnsmasq, unbound, wireguard) from the rendered artifacts; nil when the
	// pipeline runs without a service reloader (kernel-only tests).
	Reload func(ctx context.Context, rendered *render.Rendered) error

	mu      sync.Mutex
	nft     *NftApplier
	timers  *rollback.Manager
	health  Health
	store   Store
	guard   *Guard // staged ruleset of the last successful apply
	pending *rollback.Confirmation
}

// NewPipeline wires the pipeline. store may be nil (rollback then restores
// the kernel ruleset only, and the caller's environment owns store state).
func NewPipeline(nftApplier *NftApplier, timers *rollback.Manager, checker Health, st Store) *Pipeline {
	if timers == nil {
		timers = rollback.NewManager(rollback.DefaultRestoreTimeout)
	}
	return &Pipeline{
		ConfirmWindow: DefaultConfirmWindow,
		nft:           nftApplier,
		timers:        timers,
		health:        checker,
		store:         st,
	}
}

// Apply materializes candidate on the system. It is the axond Applier
// contract's pre-commit phase: on success the candidate is running and the
// caller may commit the store; on failure the running state is the
// known-good ruleset and the candidate never became live.
func (p *Pipeline) Apply(cfg *config.Config) error {
	return p.ApplyContext(context.Background(), cfg)
}

// ApplyContext is Apply with explicit context control (tests, shutdown).
func (p *Pipeline) ApplyContext(ctx context.Context, cfg *config.Config) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// A new apply supersedes any pending confirmation: the operator is
	// actively working, and a stale timer must not fire mid-apply.
	p.cancelPendingLocked()

	// Bad config never reaches the kernel: validation ran at the API
	// boundary; the render step is the last semantic gate.
	rendered, err := render.All(cfg, "")
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if err := p.nft.Check(ctx, rendered.Nft); err != nil {
		return err
	}
	guard, err := p.nft.StageKnownGood(ctx)
	if err != nil {
		return err
	}
	if err := p.nft.Replace(ctx, rendered.Nft); err != nil {
		return errors.Join(err, guard.Restore(ctx))
	}
	if p.Reload != nil {
		if err := p.Reload(ctx, rendered); err != nil {
			return errors.Join(err, guard.Restore(ctx))
		}
	}
	if p.health != nil {
		if err := p.health.SelfCheck(ctx); err != nil {
			return errors.Join(err, guard.Restore(ctx))
		}
	}
	p.guard = guard
	return nil
}

// ArmRollback arms the confirm-or-rollback timer for a just-committed
// change. The timer restores the previous ruleset and appends a store
// rollback commit unless Confirm lands first. The server calls it only
// after a successful store commit.
func (p *Pipeline) ArmRollback(prevCfg *config.Config, prevRev string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	guard := p.guard
	p.guard = nil
	if guard == nil || !guard.Staged() {
		// Nothing was captured (capability-limited environment): the timer
		// can still revert the store, which is the source of truth.
		guard = &Guard{}
	}
	restore := func(ctx context.Context) error {
		err := guard.Restore(ctx)
		if p.store != nil {
			if storeErr := p.revertStore(prevCfg, prevRev); storeErr != nil {
				err = errors.Join(err, storeErr)
			}
		}
		log.Printf("axond: apply not confirmed within %s; rolled back to revision %s (restore: %v)",
			p.ConfirmWindow, shortRev(prevRev), err)
		return err
	}
	p.pending = p.timers.ArmConfirmTimer(p.ConfirmWindow, restore)
}

// Confirm confirms the pending change, canceling its rollback timer. It
// returns false when no confirmation window is open.
func (p *Pipeline) Confirm() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancelPendingLocked()
}

// cancelPendingLocked confirms any pending rollback; mu must be held.
func (p *Pipeline) cancelPendingLocked() bool {
	if p.pending == nil {
		return false
	}
	confirmed := p.pending.Confirm()
	p.pending = nil
	return confirmed
}

// revertStore appends a rollback commit restoring cfg (the pre-apply
// revision's content). Rollbacks are history, not rewrites: the store
// records that the change happened and was undone.
func (p *Pipeline) revertStore(cfg *config.Config, prevRev string) error {
	tx, err := p.store.Begin()
	if err != nil {
		return fmt.Errorf("apply: rollback store tx: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			tx.Abort()
		}
	}()
	if err := tx.Set(cfg); err != nil {
		return fmt.Errorf("apply: rollback store tx: %w", err)
	}
	msg := "rollback: apply not confirmed within confirm window"
	if prevRev != "" {
		msg = fmt.Sprintf("rollback: apply not confirmed; restored revision %s", shortRev(prevRev))
	}
	if _, err := tx.Commit(msg); err != nil {
		return fmt.Errorf("apply: rollback store tx: %w", err)
	}
	ok = true
	return nil
}

func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
