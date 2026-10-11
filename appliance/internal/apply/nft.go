// Package apply owns the only code path that mutates the running nftables
// ruleset. The pipeline is: Check (nft -c -f validation), StageKnownGood
// (capture the current ruleset for restoration), Replace (nft -f atomic
// apply). StageKnownGood hands back a Guard whose Restore re-applies the
// captured ruleset — the rollback half of the confirm-or-rollback loop.
//
// Atomic apply is not automatic rollback: nft -f installs a valid-but-wrong
// ruleset just as happily as a correct one. The caller (axond's apply flow)
// arms the confirm timer and decides when to Restore.
package apply

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// NftApplier drives the nft binary. Where the process lacks CAP_NET_ADMIN
// (CI containers, unit-test sandboxes), the applier degrades gracefully:
// Replace/Restore become no-ops and Check still runs — `nft -c` needs only
// the parse path, not the kernel — so validation is always exercised for
// real while kernel mutation is exercised in privileged netns tests.
type NftApplier struct {
	bin string

	probeOnce sync.Once
	capable   bool
	probeErr  error
}

// NewNftApplier returns an applier bound to the nft binary in PATH.
func NewNftApplier() *NftApplier {
	return &NftApplier{bin: "nft"}
}

// Guard holds a staged known-good ruleset for one apply transaction. It
// carries the staging applier's binary so Restore drives the same nft the
// apply did (same binary in production; the fake in tests).
type Guard struct {
	ruleset []byte
	bin     string
}

// Staged reports whether a ruleset was actually captured. A guard from a
// capability-limited environment is not staged; Restore is a no-op.
func (g *Guard) Staged() bool { return g != nil && g.ruleset != nil }

// Ruleset returns the captured ruleset bytes (nil when nothing was staged).
func (g *Guard) Ruleset() []byte {
	if g == nil {
		return nil
	}
	return g.ruleset
}

// Check validates a ruleset with `nft -c -f`: parse and semantic checking
// against the running kernel without installing anything. An invalid
// ruleset never leaves this function as a silent pass.
func (a *NftApplier) Check(ctx context.Context, ruleset []byte) error {
	if len(strings.TrimSpace(string(ruleset))) == 0 {
		return fmt.Errorf("apply: refusing to validate an empty ruleset")
	}
	path, err := a.resolveBin()
	if err != nil {
		return err
	}
	f, err := tempRuleset(ruleset)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()

	cmd := exec.CommandContext(ctx, path, "-c", "-f", f) //nolint:gosec // path resolved internally via LookPath, args fixed
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply: ruleset rejected by nft -c: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// StageKnownGood captures the current ruleset so the caller can restore it
// if the apply turns out to be unhealthy. The dump is wrapped in
// `flush ruleset` so a Restore reproduces the exact prior state (a bare
// `nft list ruleset` dump re-applied without the flush would merge, not
// replace).
func (a *NftApplier) StageKnownGood(ctx context.Context) (*Guard, error) {
	capable, err := a.hasCap(ctx)
	if err != nil {
		return nil, err
	}
	if !capable {
		return &Guard{}, nil
	}
	path, err := a.resolveBin()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, path, "list", "ruleset").Output() //nolint:gosec // path resolved internally via LookPath, args fixed
	if err != nil {
		return nil, fmt.Errorf("apply: staging known-good ruleset: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		// Nothing loaded yet (first apply after boot): an empty dump is a
		// legitimate known-good — restore re-creates "no rules".
		return &Guard{ruleset: []byte("flush ruleset\n"), bin: a.bin}, nil
	}
	g := &Guard{ruleset: append([]byte("flush ruleset\n"), out...), bin: a.bin}
	return g, nil
}

// Replace atomically installs the rendered ruleset with `nft -f`. The
// kernel applies the file as one transaction: it fully replaces the old
// ruleset or makes no change.
func (a *NftApplier) Replace(ctx context.Context, ruleset []byte) error {
	if len(strings.TrimSpace(string(ruleset))) == 0 {
		return fmt.Errorf("apply: refusing to apply an empty ruleset")
	}
	capable, err := a.hasCap(ctx)
	if err != nil {
		return err
	}
	if !capable {
		return nil
	}
	path, err := a.resolveBin()
	if err != nil {
		return err
	}
	f, err := tempRuleset(ruleset)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()

	cmd := exec.CommandContext(ctx, path, "-f", f) //nolint:gosec // path resolved internally via LookPath, args fixed
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply: nft -f failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ApplyFragment installs a standalone nftables transaction fragment — a
// scoped alias-set update — with `nft -f`. Unlike Replace it stages
// nothing: the transaction touches only the fragment's own set, so a
// failure is atomic and leaves the live set (and the rest of the ruleset)
// untouched — exactly the keep-last-good behavior alias feeds want. No-op
// without kernel capability.
func (a *NftApplier) ApplyFragment(ctx context.Context, fragment []byte) error {
	if len(strings.TrimSpace(string(fragment))) == 0 {
		return fmt.Errorf("apply: refusing to apply an empty fragment")
	}
	capable, err := a.hasCap(ctx)
	if err != nil {
		return err
	}
	if !capable {
		return nil
	}
	path, err := a.resolveBin()
	if err != nil {
		return err
	}
	f, err := tempRuleset(fragment)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()

	cmd := exec.CommandContext(ctx, path, "-f", f) //nolint:gosec // path resolved internally via LookPath, args fixed
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply: fragment rejected by nft -f: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Restore re-applies the staged known-good ruleset.
func (g *Guard) Restore(ctx context.Context) error {
	if !g.Staged() {
		return nil
	}
	applier := &NftApplier{bin: g.bin}
	capable, err := applier.hasCap(ctx)
	if err != nil {
		return err
	}
	if !capable {
		return nil
	}
	path, err := applier.resolveBin()
	if err != nil {
		return err
	}
	f, err := tempRuleset(g.ruleset)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()

	cmd := exec.CommandContext(ctx, path, "-f", f) //nolint:gosec // path resolved internally via LookPath, args fixed
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply: restoring known-good ruleset failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// hasCap probes (once) whether this process may talk to the kernel's
// netlink nftables socket: `nft list ruleset` succeeding means read access,
// which for nft implies the same netlink privilege apply needs. A missing
// binary is an error, not a capability answer.
func (a *NftApplier) hasCap(ctx context.Context) (bool, error) {
	a.probeOnce.Do(func() {
		path, err := exec.LookPath(a.bin)
		if err != nil {
			a.probeErr = fmt.Errorf("apply: %s binary not found in PATH", a.bin)
			return
		}
		a.bin = path
		cmd := exec.CommandContext(ctx, path, "list", "ruleset") //nolint:gosec // path resolved internally via LookPath, args fixed
		if out, err := cmd.CombinedOutput(); err != nil {
			// Permission errors (no CAP_NET_ADMIN) are an expected,
			// downgradeable condition in test environments; anything else
			// is a real probe failure.
			msg := strings.ToLower(string(out))
			if strings.Contains(msg, "operation not permitted") ||
				strings.Contains(msg, "cache initialization failed") {
				a.capable = false
				return
			}
			a.probeErr = fmt.Errorf("apply: probing nft capability: %s", strings.TrimSpace(string(out)))
			return
		}
		a.capable = true
	})
	return a.capable, a.probeErr
}

func (a *NftApplier) resolveBin() (string, error) {
	if filepath.IsAbs(a.bin) {
		return a.bin, nil // already resolved by the capability probe
	}
	path, err := exec.LookPath(a.bin)
	if err != nil {
		return "", fmt.Errorf("apply: %s binary not found in PATH", a.bin)
	}
	a.bin = path
	return path, nil
}

// tempRuleset writes ruleset to a root-only temp file for nft to read.
func tempRuleset(ruleset []byte) (string, error) {
	f, err := os.CreateTemp("", "axonwall-ruleset-*.nft")
	if err != nil {
		return "", fmt.Errorf("apply: temp ruleset: %w", err)
	}
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("apply: temp ruleset: %w", err)
	}
	if _, err := f.Write(ruleset); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("apply: temp ruleset: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("apply: temp ruleset: %w", err)
	}
	return f.Name(), nil
}
