package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// update regenerates golden files when -update is passed:
//
//	go test ./internal/render/ -update
var update = flag.Bool("update", false, "update golden files")

func mustParse(t *testing.T, file string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	return cfg
}

// TestRenderGolden pins the ruleset byte-for-byte against testdata/*.golden.
// A diff here is meaningful: the ruleset is what the kernel runs.
func TestRenderGolden(t *testing.T) {
	cases := map[string]*config.Config{
		"full-wave1":     mustParse(t, "testdata/full-wave1.yaml"),
		"empty":          mustParse(t, "testdata/empty.yaml"),
		"urltable-only":  mustParse(t, "testdata/urltable-only.yaml"),
		"default-config": config.Default(),
	}
	for name, cfg := range cases {
		got, err := All(cfg, "a1b2c3d4e5f6a7b8")
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		golden := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.WriteFile(golden, got.Nft, 0o600); err != nil {
				t.Fatalf("write %s: %v", golden, err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read %s (run with -update to create): %v", golden, err)
		}
		if string(want) != string(got.Nft) {
			t.Errorf("ruleset %s differs from %s — inspect the diff, -update only if intended", name, golden)
		}
	}
}

// TestRenderDeterministic pins determinism explicitly: map iteration order
// must never leak into the emitted ruleset.
func TestRenderDeterministic(t *testing.T) {
	cfg := mustParse(t, "testdata/full-wave1.yaml")
	first, err := All(cfg, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := All(cfg, "rev-1")
		if err != nil {
			t.Fatal(err)
		}
		if string(next.Nft) != string(first.Nft) {
			t.Fatalf("render not deterministic at iteration %d", i)
		}
	}
}

// TestRenderRejectsUnknownZone: a rule naming a missing zone is a render
// error, never a broken ruleset.
func TestRenderRejectsUnknownZone(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.Rules = []config.Rule{{Name: "ghost", From: "lan", To: "nowhere", Verdict: "accept"}}
	if _, err := All(cfg, "rev"); err == nil {
		t.Fatal("expected render error for unknown zone")
	}
}

// TestRenderRejectsUnknownAlias covers source-alias references to aliases
// that do not exist.
func TestRenderRejectsUnknownAlias(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.Rules = []config.Rule{{Name: "ghost-alias", From: "lan", To: "wan", SourceAlias: "nope", Verdict: "accept"}}
	if _, err := All(cfg, "rev"); err == nil {
		t.Fatal("expected render error for unknown source alias")
	}
}

// TestRenderRejectsUnknownNATZone covers masquerade referencing a zone
// that does not exist.
func TestRenderRejectsUnknownNATZone(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.NAT = []config.NATRule{{Name: "lan-masq", Out: "wan0", Source: "nolane", Mode: "masquerade"}}
	if _, err := All(cfg, "rev"); err == nil {
		t.Fatal("expected render error for unknown NAT source zone")
	}
}

// TestRenderEmptyURLTableSet pins the empty url-table case: the set is
// emitted (so rule references resolve) with no elements, plus the feed URL
// as a comment — the runtime refresher fills it via `nft add element`.
func TestRenderEmptyURLTableSet(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.Aliases = map[string]config.Alias{
		"ads": {Type: "url-table", URL: "https://example.com/ads.txt"},
	}
	out, err := All(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Nft)
	if !strings.Contains(s, "set alias-ads { type ipv4_addr; flags interval; }") {
		t.Fatalf("empty url-table set not emitted as empty set:\n%s", s)
	}
	if !strings.Contains(s, "url-table: runtime set refreshed from https://example.com/ads.txt") {
		t.Fatalf("feed URL comment missing:\n%s", s)
	}
}

// TestRenderPortForwardAssociation pins the pf "filter sees the translated
// address" behavior: every port-forward emits a forward-chain allowance
// matching the post-DNAT destination.
func TestRenderPortForwardAssociation(t *testing.T) {
	cfg := config.Default()
	cfg.Firewall.NAT = []config.NATRule{{
		Name: "web", Mode: "port-forward", In: "wan0", Proto: "tcp",
		DstPort: 8443, To: "192.168.1.50:443",
	}}
	out, err := All(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Nft)
	if !strings.Contains(s, `dnat ip to 192.168.1.50:443`) {
		t.Fatalf("dnat line missing:\n%s", s)
	}
	if !strings.Contains(s, `ip daddr 192.168.1.50 tcp dport 443 accept`) {
		t.Fatalf("forward association (translated destination) missing:\n%s", s)
	}
	if strings.Contains(s, "dport 8443 accept") {
		t.Fatalf("association must match the TRANSLATED port, not the public one:\n%s", s)
	}
}
