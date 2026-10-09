package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
)

// fakeNft builds a stand-in nft binary that records every invocation and
// is steered by AXW_NFT_* environment variables:
//
//	AXW_NFT_DUMP       file whose contents `nft list ruleset` prints
//	AXW_NFT_APPLIED    file each `nft -f FILE` copies FILE onto
//	AXW_NFT_LOG        file every invocation is appended to
//	AXW_NFT_CHECK_FAIL set → `nft -c` fails
//	AXW_NFT_APPLY_FAIL set → `nft -f` fails
//	AXW_NFT_LIST_FAIL  set → `nft list ruleset` fails with a permission error
//
// It returns an applier bound to the fake and a reader for the applied file.
func fakeNft(t *testing.T) (*NftApplier, func() []byte) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "nft")
	dump := filepath.Join(dir, "dump")
	applied := filepath.Join(dir, "applied")
	log := filepath.Join(dir, "log")
	for _, f := range []string{dump, applied, log} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	const body = `#!/bin/sh
echo "$@" >> "$AXW_NFT_LOG"
if [ "$1" = "list" ]; then
  if [ -n "$AXW_NFT_LIST_FAIL" ]; then
    echo "Operation not permitted" >&2
    exit 1
  fi
  cat "$AXW_NFT_DUMP"
  exit 0
fi
if [ "$1" = "-c" ]; then
  if [ -n "$AXW_NFT_CHECK_FAIL" ]; then
    echo "syntax error, line 1" >&2
    exit 1
  fi
  exit 0
fi
if [ "$1" = "-f" ]; then
  if [ -n "$AXW_NFT_APPLY_FAIL" ]; then
    echo "apply boom" >&2
    exit 1
  fi
  # invoked as: nft -f FILE  -- FILE is $2 (no $3 in that form)
  cp "${3:-$2}" "$AXW_NFT_APPLIED" || exit 1
  exit 0
fi
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { // #nosec G306 -- fake nft script must be executable
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("AXW_NFT_DUMP", dump)
	t.Setenv("AXW_NFT_APPLIED", applied)
	t.Setenv("AXW_NFT_LOG", log)

	readApplied := func() []byte {
		b, err := os.ReadFile(applied)
		if err != nil {
			t.Fatalf("read applied: %v", err)
		}
		return b
	}
	return &NftApplier{bin: script}, readApplied
}

// setDump points AXW_NFT_DUMP at a temp file holding the given ruleset
// (the fake cats the file the env var names).
func setDump(t *testing.T, content string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "dump")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatalf("write dump: %v", err)
	}
	t.Setenv("AXW_NFT_DUMP", f)
}

func defaultRuleset(t *testing.T) []byte {
	t.Helper()
	out, err := render.All(config.Default(), "")
	if err != nil {
		t.Fatalf("render default: %v", err)
	}
	return out.Nft
}

func TestCheck_Valid(t *testing.T) {
	a, _ := fakeNft(t)
	if err := a.Check(context.Background(), defaultRuleset(t)); err != nil {
		t.Fatalf("Check(valid ruleset) = %v, want nil", err)
	}
}

func TestCheck_Invalid(t *testing.T) {
	a, _ := fakeNft(t)
	t.Setenv("AXW_NFT_CHECK_FAIL", "1")
	err := a.Check(context.Background(), []byte("table inet x {}"))
	if err == nil {
		t.Fatal("Check(invalid ruleset) should fail")
	}
	if !strings.Contains(err.Error(), "rejected by nft -c") {
		t.Errorf("error should name the validator, got %v", err)
	}
}

func TestCheck_EmptyRulesetRefused(t *testing.T) {
	a, _ := fakeNft(t)
	if err := a.Check(context.Background(), []byte("   \n")); err == nil {
		t.Fatal("Check should refuse an empty ruleset")
	}
}

// TestStageKnownGood_CapturesDump: staging returns a guard holding the
// current ruleset wrapped in flush (restore replaces, never merges).
func TestStageKnownGood_CapturesDump(t *testing.T) {
	a, _ := fakeNft(t)
	setDump(t, "table inet axonwall {\n}\n")
	guard, err := a.StageKnownGood(context.Background())
	if err != nil {
		t.Fatalf("StageKnownGood: %v", err)
	}
	if !guard.Staged() {
		t.Fatal("guard should be staged")
	}
	rs := string(guard.Ruleset())
	if !strings.HasPrefix(rs, "flush ruleset\n") {
		t.Errorf("staged ruleset should start with flush, got %q", rs)
	}
	if !strings.Contains(rs, "table inet axonwall") {
		t.Errorf("staged ruleset should contain the dump, got %q", rs)
	}
}

// TestStageKnownGood_EmptyKernel: an empty dump (fresh boot) is a
// legitimate known-good — restoring re-creates "no rules".
func TestStageKnownGood_EmptyKernel(t *testing.T) {
	a, _ := fakeNft(t)
	guard, err := a.StageKnownGood(context.Background())
	if err != nil {
		t.Fatalf("StageKnownGood: %v", err)
	}
	if !guard.Staged() {
		t.Fatal("empty kernel is still a staged known-good")
	}
	if string(guard.Ruleset()) != "flush ruleset\n" {
		t.Errorf("guard = %q, want flush-only", string(guard.Ruleset()))
	}
}

// TestStageKnownGood_NoCapability: without netlink access the guard is
// unstaged and Restore is a no-op — validation still works, mutation is
// exercised in the privileged netns tests.
func TestStageKnownGood_NoCapability(t *testing.T) {
	a, _ := fakeNft(t)
	t.Setenv("AXW_NFT_LIST_FAIL", "1")
	guard, err := a.StageKnownGood(context.Background())
	if err != nil {
		t.Fatalf("StageKnownGood under no-cap should downgrade, got %v", err)
	}
	if guard.Staged() {
		t.Fatal("guard should be unstaged without capability")
	}
	if err := guard.Restore(context.Background()); err != nil {
		t.Errorf("Restore on unstaged guard should be a no-op, got %v", err)
	}
}

// TestReplaceAndRestore: Replace installs the candidate; Restore puts the
// known-good dump back.
func TestReplaceAndRestore(t *testing.T) {
	a, readApplied := fakeNft(t)
	setDump(t, "table inet old {}\n")
	guard, err := a.StageKnownGood(context.Background())
	if err != nil {
		t.Fatalf("StageKnownGood: %v", err)
	}
	candidate := defaultRuleset(t)
	if err := a.Replace(context.Background(), candidate); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if string(readApplied()) != string(candidate) {
		t.Error("Replace did not install the candidate")
	}
	if err := guard.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if string(readApplied()) != "flush ruleset\ntable inet old {}\n" {
		t.Errorf("Restore did not re-apply the known-good, got %q", string(readApplied()))
	}
}

func TestReplace_Failure(t *testing.T) {
	a, _ := fakeNft(t)
	t.Setenv("AXW_NFT_APPLY_FAIL", "1")
	if err := a.Replace(context.Background(), defaultRuleset(t)); err == nil {
		t.Fatal("Replace should fail when nft -f fails")
	}
}

func TestReplace_EmptyRulesetRefused(t *testing.T) {
	a, _ := fakeNft(t)
	if err := a.Replace(context.Background(), nil); err == nil {
		t.Fatal("Replace should refuse an empty ruleset")
	}
}

// TestMissingBinary: a missing nft binary is an error, not a capability
// downgrade — validation must never silently pass.
func TestMissingBinary(t *testing.T) {
	a := &NftApplier{bin: "nft-definitely-not-on-path-xyz"}
	err := a.Check(context.Background(), []byte("table inet x {}"))
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("Check with missing binary should error, got %v", err)
	}
}

// TestProbeUnexpectedFailure: a kernel error that is not a permission
// downgrade is a real probe failure, surfaced rather than swallowed.
func TestProbeUnexpectedFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "nft")
	body := `#!/bin/sh
echo "unexpected netlink doom" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { // #nosec G306 -- fake nft script must be executable
		t.Fatal(err)
	}
	a := &NftApplier{bin: script}
	if _, err := a.StageKnownGood(context.Background()); err == nil {
		t.Fatal("expected probe error for unexpected nft failure")
	}
}
