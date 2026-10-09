package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// mustParse parses and validates a YAML document, failing the test on error.
func mustParse(t *testing.T, doc string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return cfg
}

const initialYAML = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [192.168.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const modifiedYAML = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  nat:
    - { name: lan-masq, out: wan0, source: lan, mode: masquerade }
  rules:
    - { name: lan-to-wan, from: lan, to: wan, verdict: accept }
`

func newTestStore(t *testing.T) (*Store, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := mustParse(t, initialYAML)
	s, err := Init(dir, cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s, cfg
}

func TestInitAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := mustParse(t, initialYAML)

	s, err := Init(dir, cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, rev, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := got.Zones["lan"]; !ok {
		t.Fatal("loaded config lost the lan zone")
	}
	if rev == "" {
		t.Fatal("expected a non-empty initial revision")
	}

	// Git history must exist with exactly one commit.
	logs, err := s.runGitOut("log", "--oneline")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(logs), "\n"); len(lines) != 1 {
		t.Fatalf("expected 1 initial commit, got %d: %q", len(lines), logs)
	}

	// The committed file must be byte-identical to a fresh marshal.
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read store file: %v", err)
	}
	out, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != string(out) {
		t.Fatalf("stored file is not the canonical marshal:\n%s\n---\n%s", data, out)
	}
}

func TestTransactionCommitRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)

	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Set(mustParse(t, modifiedYAML)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	newRev, err := tx.Commit("add wan and masquerade")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg, rev, err := s.Load()
	if err != nil {
		t.Fatalf("Load after commit: %v", err)
	}
	if _, ok := cfg.Zones["wan"]; !ok {
		t.Fatal("committed config lost the wan zone")
	}
	if rev != newRev {
		t.Fatalf("rev = %s, want %s", rev, newRev)
	}
	if rev == tx.BaseRev() {
		t.Fatal("revision did not advance after commit")
	}

	// The transaction is finished; reuse is rejected.
	if err := tx.Set(mustParse(t, initialYAML)); !errors.Is(err, ErrTxDone) {
		t.Fatalf("Set after commit = %v, want ErrTxDone", err)
	}
	if _, err := tx.Commit("again"); !errors.Is(err, ErrTxDone) {
		t.Fatalf("Commit after commit = %v, want ErrTxDone", err)
	}
}

func TestCommitConflict(t *testing.T) {
	s, _ := newTestStore(t)

	tx1, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx1: %v", err)
	}
	tx2, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}

	// tx2 commits first and wins.
	if err := tx2.Set(mustParse(t, modifiedYAML)); err != nil {
		t.Fatalf("Set tx2: %v", err)
	}
	if _, err := tx2.Commit("tx2 wins"); err != nil {
		t.Fatalf("Commit tx2: %v", err)
	}

	// tx1 commits on a stale base and must be rejected.
	if err := tx1.Set(mustParse(t, initialYAML)); err != nil {
		t.Fatalf("Set tx1: %v", err)
	}
	_, err = tx1.Commit("tx1 stale")
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Commit tx1 = %v, want *ConflictError", err)
	}
	if conflict.Current == "" {
		t.Fatal("conflict must report the current revision")
	}

	// The store still reflects tx2's win, and the failed commit left the
	// working file consistent with HEAD.
	cfg, _, err := s.Load()
	if err != nil {
		t.Fatalf("Load after conflict: %v", err)
	}
	if _, ok := cfg.Zones["wan"]; !ok {
		t.Fatal("conflicting commit clobbered the winning transaction")
	}
}

func TestCommitNoOpIsClean(t *testing.T) {
	s, _ := newTestStore(t)

	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// Commit with an unchanged candidate: nothing to commit.
	rev, err := tx.Commit("no change")
	if err != nil {
		t.Fatalf("Commit no-op: %v", err)
	}
	if rev != tx.BaseRev() {
		t.Fatalf("no-op commit returned rev %s, want base %s", rev, tx.BaseRev())
	}
}

func TestSetRejectsInvalidCandidate(t *testing.T) {
	s, _ := newTestStore(t)

	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	bad := mustParse(t, initialYAML)
	bad.Firewall.Default.Input = "reject" // invalid policy
	if err := tx.Set(bad); err == nil {
		t.Fatal("Set accepted an invalid candidate")
	}
	// The candidate must remain the valid one from Begin.
	if tx.Config().Firewall.Default.Input != "drop" {
		t.Fatal("failed Set replaced the candidate")
	}
	tx.Abort()
}

func TestInitFailsWhenStoreExists(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := Init(s.Dir(), mustParse(t, initialYAML)); err == nil {
		t.Fatal("Init succeeded on an existing store")
	}
}

func TestOpenInjectsGitDir(t *testing.T) {
	// Open on an empty directory must create the git dir; the store is
	// usable for dev/test flows that inject their own location.
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("git dir not created: %v", err)
	}
	if _, _, err := s.Load(); err == nil {
		t.Fatal("Load on an empty store should fail: no axonwall.yaml yet")
	}
}
