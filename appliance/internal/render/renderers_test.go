package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// TestRenderServiceGoldens pins every service renderer's output
// byte-for-byte against testdata/<case>.<renderer>.golden, using the same
// configs as the ruleset goldens. A diff here is meaningful: it is what the
// daemons will run. Artifacts a config does not produce (nil) have no
// golden file — absence is asserted by TestRenderServiceNilWhenAbsent.
func TestRenderServiceGoldens(t *testing.T) {
	cases := map[string]*config.Config{
		"full-wave1":     mustParse(t, "testdata/full-wave1.yaml"),
		"empty":          mustParse(t, "testdata/empty.yaml"),
		"default-config": config.Default(),
	}
	renderers := []struct {
		name     string
		artifact func(r *Rendered) []byte
		units    bool
	}{
		{name: "networkd", artifact: func(r *Rendered) []byte { return concatUnits(r.Networkd) }, units: true},
		{name: "dnsmasq", artifact: func(r *Rendered) []byte { return r.Dnsmasq }},
		{name: "unbound", artifact: func(r *Rendered) []byte { return r.Unbound }},
		{name: "wireguard", artifact: func(r *Rendered) []byte { return r.WireGuard }},
	}
	for caseName, cfg := range cases {
		got, err := All(cfg, "a1b2c3d4e5f6a7b8")
		if err != nil {
			t.Fatalf("render %s: %v", caseName, err)
		}
		for _, rend := range renderers {
			artifact := rend.artifact(got)
			golden := filepath.Join("testdata", fmt.Sprintf("%s.%s.golden", caseName, rend.name))
			if len(artifact) == 0 {
				// Nothing rendered for this renderer: the golden must not
				// exist (a stale golden means the renderer started emitting
				// for a config it should not).
				if _, err := os.Stat(golden); err == nil {
					t.Errorf("%s/%s: rendered nothing but golden %s exists", caseName, rend.name, golden)
				}
				continue
			}
			if *update {
				if err := os.WriteFile(golden, artifact, 0o600); err != nil {
					t.Fatalf("write %s: %v", golden, err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read %s (run with -update to create): %v", golden, err)
			}
			if string(want) != string(artifact) {
				t.Errorf("%s/%s differs from %s — inspect the diff, -update only if intended", caseName, rend.name, golden)
			}
		}
	}
}

// concatUnits renders a map of units into a deterministic single artifact
// for golden comparison: sorted by file name, separated by headers.
func concatUnits(units map[string][]byte) []byte {
	if units == nil {
		return nil
	}
	names := make([]string, 0, len(units))
	for n := range units {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, n := range names {
		fmt.Fprintf(&b, "===== %s =====\n", n)
		b.Write(units[n])
	}
	return b.Bytes()
}

// TestRenderServiceNilWhenAbsent: service artifacts are nil exactly when
// their config section is absent — the reloader's contract for "nothing to
// install / tear down what was there".
func TestRenderServiceNilWhenAbsent(t *testing.T) {
	out, err := All(mustParse(t, "testdata/empty.yaml"), "rev")
	if err != nil {
		t.Fatal(err)
	}
	if out.Dnsmasq != nil {
		t.Errorf("dnsmasq artifact for a config without services.dhcp:\n%s", out.Dnsmasq)
	}
	if out.Unbound != nil {
		t.Errorf("unbound artifact for a config without services.dns:\n%s", out.Unbound)
	}
	if out.WireGuard != nil {
		t.Errorf("wireguard artifact for a config without services.wireguard:\n%s", out.WireGuard)
	}
	if len(out.Networkd) == 0 {
		t.Error("networkd units missing for the default config (it declares interfaces)")
	}
}

// TestRenderNetworkdLinkPolicy pins the .link contract: every physical
// interface gets a .network plus a .link unit; wg0 gets only a .network
// (its link is created at runtime, not claimed by udev).
func TestRenderNetworkdLinkPolicy(t *testing.T) {
	cfg := mustParse(t, "testdata/full-wave1.yaml")
	out, err := All(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"10-axonwall-wan0.network", "10-axonwall-lan0.network", "10-axonwall-wg0.network"} {
		if _, ok := out.Networkd[name]; !ok {
			t.Errorf("missing networkd unit %s (have %v)", name, keysOf(out.Networkd))
		}
	}
	for _, physical := range []string{"10-axonwall-wan0.link", "10-axonwall-lan0.link"} {
		if _, ok := out.Networkd[physical]; !ok {
			t.Errorf("missing .link unit %s (have %v)", physical, keysOf(out.Networkd))
		}
	}
	if _, ok := out.Networkd["10-axonwall-wg0.link"]; ok {
		t.Errorf("wg0 must not get a .link unit (have %v)", keysOf(out.Networkd))
	}
	link := string(out.Networkd["10-axonwall-lan0.link"])
	for _, want := range []string{"OriginalName=enp2s0", "NamePolicy=kernel database onboard slot path", "MACAddressPolicy=persistent"} {
		if !strings.Contains(link, want) {
			t.Errorf(".link unit missing %q:\n%s", want, link)
		}
	}
}

// TestRenderDnsmasqPoolConflicts: two pools in one zone must agree on the
// tagged router/DNS options — a conflict is a render error, never a config
// that hands clients two gateways.
func TestRenderDnsmasqPoolConflicts(t *testing.T) {
	cfg := config.Default()
	cfg.Services.DHCP = &config.DHCP{Pools: []config.DHCPPool{
		{Zone: "lan", Range: [2]string{"192.168.1.100", "192.168.1.150"}, Gateway: "192.168.1.1", DNS: "192.168.1.1"},
		{Zone: "lan", Range: [2]string{"192.168.1.151", "192.168.1.199"}, Gateway: "192.168.1.2", DNS: "192.168.1.1"},
	}}
	if _, err := renderDnsmasq(cfg, "rev"); err == nil {
		t.Fatal("expected render error for conflicting pool gateways in one zone")
	}
}

// TestRenderUnboundForwardMode pins the forward-mode contract: forwarders
// render a root forward-zone; recursive mode renders none.
func TestRenderUnboundForwardMode(t *testing.T) {
	cfg := config.Default()
	cfg.Services.DNS = &config.DNS{Resolver: "unbound", Listen: []string{"lan"}, Mode: "forward", Forwarders: []string{"1.1.1.1", "9.9.9.9"}}
	fwd, err := renderUnbound(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"forward-zone:", "forward-addr: 1.1.1.1", "forward-addr: 9.9.9.9"} {
		if !strings.Contains(string(fwd), want) {
			t.Errorf("forward-mode config missing %q:\n%s", want, fwd)
		}
	}
	cfg.Services.DNS.Mode = ""
	rec, err := renderUnbound(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rec), "forward-zone") {
		t.Errorf("recursive mode rendered a forward-zone:\n%s", rec)
	}
	if !strings.Contains(string(rec), "access-control: 192.168.1.0/24 allow") {
		t.Errorf("recursive config missing the zone-subnet ACL:\n%s", rec)
	}
}

// TestRenderWireGuardKeyValidation: a malformed peer key is a render error
// and the rendered file carries the base64 form the wg tool accepts.
func TestRenderWireGuardKeyValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Services.WireGuard = &config.WireGuard{
		ListenPort: 51820,
		Peers:      []config.WGPeer{{Name: "bad", PublicKey: "not-base64!", AllowedIPs: []string{"10.10.0.2/32"}}},
	}
	if _, err := renderWireGuard(cfg, "rev"); err == nil {
		t.Fatal("expected render error for malformed peer key")
	}
	cfg.Services.WireGuard.Peers[0].PublicKey = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	out, err := renderWireGuard(cfg, "rev")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "PublicKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=") {
		t.Errorf("rendered key must be the base64 form:\n%s", s)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
