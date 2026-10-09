package apply

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
	"gopkg.in/yaml.v3"
)

// Privileged integration tests for the apply pipeline, run against the real
// nft binary inside a private network namespace (`unshare -n`). The test
// binary re-executes itself as the netns child; when namespace creation is
// unavailable (CI containers, non-root), the child tests skip and the
// unprivileged unit tests plus `nft -c` fixture validation carry the gate.
//
// Local verification: go test -c -o /tmp/apply.test ./internal/apply/ &&
//   sudo /tmp/apply.test -test.run '^TestNetnsChild' -test.v

const netnsChildEnv = "AXW_NETNS_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(netnsChildEnv) == "1" {
		// Child: run only the privileged tests.
		os.Exit(m.Run())
	}
	// Parent: try to enter a private network namespace and run the child
	// tests there. No namespace capability → plain run (children skip).
	if path, err := exec.LookPath("unshare"); err == nil {
		probe := exec.Command(path, "-n", "true") //nolint:gosec // path from exec.LookPath, fixed args
		if err := probe.Run(); err == nil {
			args := append([]string{"-n", os.Args[0], "-test.run=^TestNetnsChild", "-test.v"}, extraTestFlags()...)
			child := exec.Command(path, args...) //nolint:gosec // path from exec.LookPath, re-executes this test binary in a netns
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			child.Env = append(os.Environ(), netnsChildEnv+"=1")
			if err := child.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					os.Exit(ee.ExitCode())
				}
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// extraTestFlags forwards verbosity/timeout flags to the child run.
func extraTestFlags() []string {
	var flags []string
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.timeout=") || a == "-test.v" {
			flags = append(flags, a)
		}
	}
	return flags
}

// kernelRuleset lists the live ruleset (privileged child only).
func kernelRuleset(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v: %s", err, string(out))
	}
	return string(out)
}

func requireNetnsChild(t *testing.T) {
	t.Helper()
	if os.Getenv(netnsChildEnv) != "1" {
		t.Skip("privileged netns child required (run under unshare -n)")
	}
}

func TestNetnsChild_InvalidConfigRejectedBeforeKernel(t *testing.T) {
	requireNetnsChild(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(NewNftApplier(), nil, &nopHealth{}, st)

	cand := config.Default()
	cand.Firewall.Rules = []config.Rule{{Name: "ghost", From: "lan", To: "nowhere", Verdict: "accept"}}
	if err := p.Apply(cand); err == nil {
		t.Fatal("Apply(unknown zone) should fail")
	}
	if rs := kernelRuleset(t); strings.Contains(rs, "table inet axonwall") {
		t.Fatalf("kernel must be untouched on render failure, ruleset = %s", rs)
	}
}

func TestNetnsChild_ValidApplyLoads(t *testing.T) {
	requireNetnsChild(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(NewNftApplier(), nil, &nopHealth{}, st)

	if err := p.Apply(candidateWithRule("lan-to-wan")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rs := kernelRuleset(t); !strings.Contains(rs, "table inet axonwall") {
		t.Fatalf("candidate ruleset not loaded, ruleset = %s", rs)
	}
}

// TestNetnsChild_ForcedRollbackRestoresKnownGood: the forced-rollback test
// against the real kernel — an unconfirmed change has its ruleset replaced
// by the pre-apply known-good and its store commit reverted.
func TestNetnsChild_ForcedRollbackRestoresKnownGood(t *testing.T) {
	requireNetnsChild(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(NewNftApplier(), nil, &nopHealth{}, st)
	p.ConfirmWindow = 300 * time.Millisecond

	cand := candidateWithRule("rollback-test") // must differ from the committed config
	if err := p.Apply(cand); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rs := kernelRuleset(t); !strings.Contains(rs, "rollback-test") {
		t.Fatalf("candidate rule not loaded before rollback, ruleset = %s", rs)
	}

	applyRev := commitCfg(t, st, cand, "apply: netns rollback test")
	p.ArmRollback(prevCfg, prevRev)

	waitFor(t, func() bool {
		_, rev, err := st.Load()
		return err == nil && rev != applyRev
	}, "rollback commit did not land")

	cfg, _, err := st.Load()
	if err != nil {
		t.Fatalf("load after rollback: %v", err)
	}
	got, _ := yaml.Marshal(cfg)
	want, _ := yaml.Marshal(prevCfg)
	if string(got) != string(want) {
		t.Error("store should hold the pre-apply config after rollback")
	}
	if rs := kernelRuleset(t); strings.Contains(rs, "rollback-test") {
		t.Fatalf("rolled-back rule still present in the kernel, ruleset = %s", rs)
	}
}

func TestNetnsChild_HealthyApplyCommitsStore(t *testing.T) {
	requireNetnsChild(t)
	st, prevCfg, prevRev := newTestStore(t)
	p := NewPipeline(NewNftApplier(), nil, &nopHealth{}, st)
	p.ConfirmWindow = 5 * time.Second

	cand := candidateWithRule("healthy-apply")
	if err := p.Apply(cand); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	applyRev := commitCfg(t, st, cand, "apply: netns healthy test")
	p.ArmRollback(prevCfg, prevRev)
	if !p.Confirm() {
		t.Fatal("Confirm should succeed while the window is open")
	}
	time.Sleep(100 * time.Millisecond)

	_, rev, err := st.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rev != applyRev {
		t.Errorf("healthy apply must keep its store commit (rev %s, want %s)", rev, applyRev)
	}
	if rs := kernelRuleset(t); !strings.Contains(rs, "healthy-apply") {
		t.Fatalf("healthy candidate should remain applied, ruleset = %s", rs)
	}
}

// TestNetnsChild_AliasFragmentUpdatesSet covers the real-kernel fragment
// path the alias refresher uses: the config apply seeds the url-table set
// from the store entries, then a scoped set-update fragment atomically
// replaces its elements without touching the rest of the ruleset.
func TestNetnsChild_AliasFragmentUpdatesSet(t *testing.T) {
	requireNetnsChild(t)
	st, _, _ := newTestStore(t)
	p := NewPipeline(NewNftApplier(), nil, &nopHealth{}, st)

	cand := config.Default()
	cand.Firewall.Aliases = map[string]config.Alias{
		"ads": {Type: "url-table", URL: "https://feeds.example/ads.txt", Entries: []string{"192.0.2.1"}},
	}
	if err := p.Apply(cand); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	fragment, err := render.SetUpdateFragment(cand, "ads", []string{"198.51.100.7", "203.0.113.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewNftApplier().ApplyFragment(context.Background(), fragment); err != nil {
		t.Fatalf("ApplyFragment: %v", err)
	}
	rs := kernelRuleset(t)
	if !strings.Contains(rs, "203.0.113.0/24") || strings.Contains(rs, "192.0.2.1") {
		t.Fatalf("fragment should have replaced the set elements, ruleset = %s", rs)
	}
}
