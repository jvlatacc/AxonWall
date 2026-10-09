package apply

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
	"gopkg.in/yaml.v3"
)

// nopHealth is a configurable Health implementation.
type nopHealth struct{ err error }

func (h *nopHealth) SelfCheck(context.Context) error { return h.err }

// newTestStore opens a git-backed store seeded with the default config.
func newTestStore(t *testing.T) (*store.Store, *config.Config, string) {
	t.Helper()
	s, err := store.Init(t.TempDir(), config.Default())
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	cfg, rev, err := s.Load()
	if err != nil {
		t.Fatalf("store load: %v", err)
	}
	return s, cfg, rev
}

// commitCfg emulates the server's post-apply store commit.
func commitCfg(t *testing.T, s *store.Store, cfg *config.Config, msg string) string {
	t.Helper()
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := tx.Set(cfg); err != nil {
		tx.Abort()
		t.Fatalf("tx set: %v", err)
	}
	rev, err := tx.Commit(msg)
	if err != nil {
		tx.Abort()
		t.Fatalf("tx commit: %v", err)
	}
	return rev
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// candidateWithRule adds one forwarding rule to the default config so an
// apply has visibly different ruleset output from the bare default.
func candidateWithRule(name string) *config.Config {
	cfg := config.Default()
	cfg.Firewall.Rules = []config.Rule{{Name: name, From: "lan", To: "wan", Verdict: "accept"}}
	return cfg
}

func marshalCfg(t *testing.T, c *config.Config) string {
	t.Helper()
	b, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestPipeline_ApplySuccess(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)

	if err := p.Apply(candidateWithRule("lan-to-wan")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(readApplied()) == 0 {
		t.Fatal("candidate was not applied to the kernel")
	}
	if p.guard == nil || !p.guard.Staged() {
		t.Fatal("successful apply should stage a known-good guard")
	}
}

// TestPipeline_InvalidConfigRejectedBeforeKernel: a config that cannot
// render fails before the kernel is touched — the fake's applied file
// stays empty.
func TestPipeline_InvalidConfigRejectedBeforeKernel(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)

	cand := config.Default()
	cand.Firewall.Rules = []config.Rule{{Name: "ghost", From: "lan", To: "nowhere", Verdict: "accept"}}
	if err := p.Apply(cand); err == nil {
		t.Fatal("Apply(unknown zone) should fail")
	}
	if len(readApplied()) != 0 {
		t.Fatalf("kernel must be untouched on render failure, got %q", string(readApplied()))
	}
}

// TestPipeline_CheckFailureKeepsKernel: a ruleset rejected by nft -c never
// reaches nft -f.
func TestPipeline_CheckFailureKeepsKernel(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)

	t.Setenv("AXW_NFT_CHECK_FAIL", "1")
	if err := p.Apply(config.Default()); err == nil {
		t.Fatal("Apply should fail when nft -c rejects the ruleset")
	}
	if len(readApplied()) != 0 {
		t.Fatalf("kernel must be untouched on check failure, got %q", string(readApplied()))
	}
}

// TestPipeline_RestoreOnReloadFailure: a failed post-apply reload restores
// the known-good ruleset, and the error reports both failures.
func TestPipeline_RestoreOnReloadFailure(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)
	p.Reload = func(_ context.Context, _ *render.Rendered) error { return errors.New("reload boom") }

	setDump(t, "table inet known-good {}\n")
	err := p.Apply(config.Default())
	if err == nil {
		t.Fatal("Apply should fail when reload fails")
	}
	if !strings.Contains(err.Error(), "reload boom") {
		t.Errorf("error should carry the reload failure, got %v", err)
	}
	if string(readApplied()) != "flush ruleset\ntable inet known-good {}\n" {
		t.Errorf("known-good ruleset not restored, applied = %q", string(readApplied()))
	}
}

// TestPipeline_RestoreOnHealthFailure: a failed self-check restores the
// known-good ruleset.
func TestPipeline_RestoreOnHealthFailure(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{err: errors.New("selfcheck down")}, st)

	setDump(t, "table inet known-good {}\n")
	err := p.Apply(config.Default())
	if err == nil {
		t.Fatal("Apply should fail when the self-check fails")
	}
	if !strings.Contains(err.Error(), "selfcheck down") {
		t.Errorf("error should carry the health failure, got %v", err)
	}
	if string(readApplied()) != "flush ruleset\ntable inet known-good {}\n" {
		t.Errorf("known-good ruleset not restored, applied = %q", string(readApplied()))
	}
}

// TestPipeline_RollbackOnUnconfirmed: the forced-rollback test — a change
// that is not confirmed within the window has its ruleset restored AND its
// store change reverted by a rollback commit.
func TestPipeline_RollbackOnUnconfirmed(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)
	p.ConfirmWindow = 150 * time.Millisecond

	setDump(t, "table inet known-good {}\n")
	cand := candidateWithRule("rollback-test") // must differ from the committed config: a rollback of a no-change apply has nothing to revert
	if err := p.Apply(cand); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	applyRev := commitCfg(t, st, cand, "apply: test change")
	p.ArmRollback(prevCfg, prevRev)

	waitFor(t, func() bool {
		_, rev, err := st.Load()
		return err == nil && rev != applyRev
	}, "rollback commit did not land")

	cfg, _, err := st.Load()
	if err != nil {
		t.Fatalf("load after rollback: %v", err)
	}
	if marshalCfg(t, cfg) != marshalCfg(t, prevCfg) {
		t.Error("store should hold the pre-apply config after rollback")
	}
	if string(readApplied()) != "flush ruleset\ntable inet known-good {}\n" {
		t.Errorf("kernel should hold the known-good ruleset after rollback, applied = %q", string(readApplied()))
	}
}

// TestPipeline_ConfirmCancelsRollback: a confirmed change is never rolled
// back.
func TestPipeline_ConfirmCancelsRollback(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)
	p.ConfirmWindow = 150 * time.Millisecond

	setDump(t, "table inet known-good {}\n")
	if err := p.Apply(config.Default()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	applyRev := commitCfg(t, st, config.Default(), "apply: test change")
	p.ArmRollback(prevCfg, prevRev)
	if !p.Confirm() {
		t.Fatal("Confirm should succeed while the window is open")
	}
	if p.Confirm() {
		t.Error("second Confirm should report nothing pending")
	}
	time.Sleep(400 * time.Millisecond)

	_, rev, err := st.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rev != applyRev {
		t.Errorf("confirmed change must not be rolled back (rev %s, want %s)", rev, applyRev)
	}
	applied := string(readApplied())
	if !strings.Contains(applied, "AxonWall managed ruleset") || strings.Contains(applied, "table inet known-good") {
		t.Errorf("confirmed candidate should still be applied, applied = %q", applied)
	}
}

// TestPipeline_NewApplySupersedesPending: a newer apply cancels the old
// confirmation window — the operator is actively working, and a stale
// rollback must not fire mid-apply.
func TestPipeline_NewApplySupersedesPending(t *testing.T) {
	a, readApplied := fakeNft(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)
	p.ConfirmWindow = 150 * time.Millisecond

	setDump(t, "table inet known-good {}\n")
	if err := p.Apply(config.Default()); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	firstRev := commitCfg(t, st, config.Default(), "apply: first change")
	p.ArmRollback(prevCfg, prevRev)

	if err := p.Apply(candidateWithRule("lan-to-wan")); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if p.Confirm() {
		t.Error("superseded window should already be closed")
	}
	time.Sleep(400 * time.Millisecond)

	_, rev, err := st.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rev != firstRev {
		t.Errorf("superseded rollback must not fire (rev %s, want %s)", rev, firstRev)
	}
	if !strings.Contains(string(readApplied()), "AxonWall managed ruleset") {
		t.Errorf("second candidate should still be applied, applied = %q", string(readApplied()))
	}
}

// TestPipeline_RollbackWithoutGuard: even with no staged ruleset
// (capability-limited environment), a fired rollback still reverts the
// store — the source of truth is recovered even when the kernel cannot be.
func TestPipeline_RollbackWithoutGuard(t *testing.T) {
	a, _ := fakeNft(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(a, nil, &nopHealth{}, st)
	p.ConfirmWindow = 120 * time.Millisecond
	p.guard = &Guard{} // unstaged: nothing was captured

	p.ArmRollback(prevCfg, prevRev)
	waitFor(t, func() bool {
		cfg, _, err := st.Load()
		return err == nil && marshalCfg(t, cfg) == marshalCfg(t, prevCfg)
	}, "store rollback did not land without a guard")
}
