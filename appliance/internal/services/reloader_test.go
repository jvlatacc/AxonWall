package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
)

// fakeRunner records every command and succeeds. Tests assert on the
// command sequence — the reloader's contract is what it runs, when.
type fakeRunner struct {
	cmds []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.cmds = append(f.cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil, nil
}

func (f *fakeRunner) has(substr string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func (f *fakeRunner) count(substr string) int {
	n := 0
	for _, c := range f.cmds {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// testConfig builds a full-services config and renders it.
func testConfig(t *testing.T) (*config.Config, *render.Rendered) {
	t.Helper()
	cfg := config.Default()
	cfg.Services.WireGuard = &config.WireGuard{
		ListenPort: 51820,
		Peers: []config.WGPeer{{
			Name:       "laptop",
			PublicKey:  "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=",
			AllowedIPs: []string{"10.10.0.2/32"},
		}},
	}
	out, err := render.All(cfg, "testrev")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return cfg, out
}

// TestReloaderSyncInstallsAndReloads: a first sync installs every rendered
// file, reloads networkd once, restarts dnsmasq and unbound, and syncs the
// WireGuard runtime config with the private key joined in.
func TestReloaderSyncInstallsAndReloads(t *testing.T) {
	_, out := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{Root: root, Run: run.Run}

	if err := r.Sync(context.Background(), out); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Rendered files landed at their appliance paths.
	for _, p := range []string{
		"etc/systemd/network/10-axonwall-lan0.network",
		"etc/systemd/network/10-axonwall-lan0.link",
		"etc/dnsmasq.conf",
		"etc/unbound/unbound.conf",
		"etc/wireguard/wg0.conf",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("expected %s installed: %v", p, err)
		}
	}
	// The WireGuard private key: 0600, in /etc, joined into the sync file.
	keyPath := filepath.Join(root, "etc/wireguard/wg0.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("private key not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key perms = %o, want 600", perm)
	}
	syncPath := filepath.Join(root, "run/axonwall/wg0.conf")
	synced, err := os.ReadFile(syncPath)
	if err != nil {
		t.Fatalf("sync file not created: %v", err)
	}
	if !strings.Contains(string(synced), "PrivateKey = ") {
		t.Errorf("sync file missing the private key line:\n%s", synced)
	}
	if strings.Contains(string(synced), "SECRET") || strings.Contains(string(synced), "AXONWALL_TEST") {
		t.Errorf("sync file must carry the real key, not the env placeholder:\n%s", synced)
	}
	// The stored rendered artifact never carries a key.
	if strings.Contains(string(out.WireGuard), "PrivateKey") {
		t.Errorf("rendered artifact must not contain a private key:\n%s", out.WireGuard)
	}
	// Reload sequence: networkctl reload, service restarts, wg syncconf.
	if !run.has("networkctl reload") {
		t.Errorf("missing networkctl reload; commands: %v", run.cmds)
	}
	if !run.has("systemctl restart dnsmasq") || !run.has("systemctl restart unbound") {
		t.Errorf("missing service restarts; commands: %v", run.cmds)
	}
	if !run.has("wg syncconf wg0") {
		t.Errorf("missing wg syncconf; commands: %v", run.cmds)
	}
}

// TestReloaderSyncIdempotent: a second sync with unchanged rendered output
// runs no commands at all — an apply loop must not flap the network.
func TestReloaderSyncIdempotent(t *testing.T) {
	_, out := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{Root: root, Run: run.Run}
	if err := r.Sync(context.Background(), out); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	first := len(run.cmds)

	if err := r.Sync(context.Background(), out); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := len(run.cmds); got != first {
		t.Errorf("second sync ran %d additional commands (%v), want 0", got-first, run.cmds[first:])
	}
}

// TestReloaderSyncRemovesStaleUnits: a managed unit no longer in the render
// is deleted, and networkctl reload runs even when every live file matched
// (the delete alone is a change networkd must learn about).
func TestReloaderSyncRemovesStaleUnits(t *testing.T) {
	_, out := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{Root: root, Run: run.Run}

	stale := filepath.Join(root, "etc/systemd/network/10-axonwall-old0.network")
	if err := os.MkdirAll(filepath.Dir(stale), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Sync(context.Background(), out); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale unit still present (err=%v)", err)
	}
}

// TestReloaderTeardownStopsServices: dropping services.dhcp removes
// dnsmasq.conf and STOPS the unit — a restarted daemon on distro defaults
// would open a second DNS listener on :53.
func TestReloaderTeardownStopsServices(t *testing.T) {
	cfgFull, outFull := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{Root: root, Run: run.Run}
	if err := r.Sync(context.Background(), outFull); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	cfgFull.Services.DHCP = nil
	outLess, err := render.All(cfgFull, "testrev")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := r.Sync(context.Background(), outLess); err != nil {
		t.Fatalf("teardown sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/dnsmasq.conf")); !os.IsNotExist(err) {
		t.Errorf("dnsmasq.conf still present (err=%v)", err)
	}
	if !run.has("systemctl stop dnsmasq") {
		t.Errorf("missing systemctl stop dnsmasq; commands: %v", run.cmds)
	}
	if run.count("systemctl restart dnsmasq") != 1 {
		t.Errorf("teardown must not restart dnsmasq; commands: %v", run.cmds)
	}
}

// TestReloaderWireGuardTeardown: dropping services.wireguard removes the
// runtime sync file and deletes the interface.
func TestReloaderWireGuardTeardown(t *testing.T) {
	cfgFull, outFull := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{Root: root, Run: run.Run}
	if err := r.Sync(context.Background(), outFull); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	cfgFull.Services.WireGuard = nil
	outLess, err := render.All(cfgFull, "testrev")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := r.Sync(context.Background(), outLess); err != nil {
		t.Fatalf("teardown sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "run/axonwall/wg0.conf")); !os.IsNotExist(err) {
		t.Errorf("runtime sync file still present (err=%v)", err)
	}
	if !run.has("link delete wg0") {
		t.Errorf("missing ip link delete wg0; commands: %v", run.cmds)
	}
}

// TestReloaderBadPrivateKeySource: a failing private-key source is a sync
// error, never a silently broken WireGuard.
func TestReloaderBadPrivateKeySource(t *testing.T) {
	_, out := testConfig(t)
	root := t.TempDir()
	run := &fakeRunner{}
	r := &Reloader{
		Root:    root,
		Run:     run.Run,
		ReadKey: func(name string) ([]byte, error) { return nil, fmt.Errorf("simulated key read failure for %s", name) },
	}
	if err := r.Sync(context.Background(), out); err == nil {
		t.Fatal("expected sync error when the private key source fails")
	}
}
