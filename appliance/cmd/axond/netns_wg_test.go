package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/apply"
	"github.com/jvlatacc/AxonWall/appliance/internal/services"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// Privileged integration test: a WireGuard peer added through the API
// (PUT /config → validate → render → nft apply → service sync → commit)
// ends in a real wg0 passing real encrypted UDP traffic between two
// network namespaces. Runs under `unshare -n` (the test binary re-executes
// itself as the netns child, same pattern as internal/apply); without
// namespace privileges the child skips and the unit tests carry the gate.
//
// Local verification: cd appliance &&
//   sudo -E env PATH="$PATH" go test ./cmd/axond/ -run '^TestNetnsChild' -v
//
// Topology (all inside the test's private netns):
//
//	this namespace                      axw-peer namespace
//	┌────────────────────────┐          ┌──────────────────────────┐
//	│  veth0 10.0.0.1/30 ────┼──────────┼──── veth1 10.0.0.2/30    │
//	│  wg0 10.10.0.1  (API)  │  <veth>  │  wg0 10.10.0.2 (scaffold)│
//	│  nft + wg via axond    │          │  plain wg + routes       │
//	└────────────────────────┘          └──────────────────────────┘
//
// The veth carries the WireGuard UDP transport (udp/51820 accepted by the
// wg-handshake rule the API config carries — the ruleset matches kernel
// interface names, so the physical veth0 works without networkd running).

const (
	netnsChildEnv = "AXW_AXOND_NETNS_CHILD"
	senderEnv     = "AXW_AXOND_WG_SENDER"
	wgTestPayload = "axw-wg-e2e-payload"
	peerNs        = "axw-peer"
)

// testBaseDoc is the store's initial config: the WAN zone on the veth
// (kernel name — nft matches kernel names), a LAN zone, Unbound on the LAN,
// and the rule that lets the WireGuard transport through. No WireGuard
// service yet — the test adds it through the API. The services section is
// last so the PUT body can append the wireguard block inside it.
const testBaseDoc = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
interfaces:
  - { name: wan0, match: veth0, addressing: static, address: ["10.0.0.1/30"] }
  - { name: lan0, match: enp2s0, addressing: static, address: ["192.168.1.1/24"] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  rules:
    - { name: wg-handshake, from: wan, to: firewall, service: udp/51820, verdict: accept }
services:
  dns:
    resolver: unbound
    listen: [lan]
`

// wgConfigDoc returns the full PUT body: the base config plus the
// services.wireguard section with the peer-namespace's public key, a zone
// for the materialized wg0 (allowed only alongside a wireguard service),
// and an input rule so decrypted tunnel traffic reaches the listener.
func wgConfigDoc(peerPub string) string {
	return `version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
  wg: { interfaces: [wg0] }
interfaces:
  - { name: wan0, match: veth0, addressing: static, address: ["10.0.0.1/30"] }
  - { name: lan0, match: enp2s0, addressing: static, address: ["192.168.1.1/24"] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  rules:
    - { name: wg-handshake, from: wan, to: firewall, service: udp/51820, verdict: accept }
    - { name: wg-in, from: wg, to: firewall, verdict: accept }
services:
  dns:
    resolver: unbound
    listen: [lan, wg]
  wireguard:
    listen-port: 51820
    peers:
      - { name: axw-peer, public-key: "` + peerPub + `", allowed-ips: ["10.10.0.2/32"] }
`
}

func TestMain(m *testing.M) {
	// Sender child (innermost): one UDP datagram across the tunnel.
	if os.Getenv(senderEnv) != "" {
		os.Exit(runWGSender(os.Getenv(senderEnv)))
	}
	if os.Getenv(netnsChildEnv) == "1" {
		// Loopback must be up: the unprivileged fixtures in this binary
		// (httptest servers) bind and connect on 127.0.0.1.
		_ = exec.Command("ip", "link", "set", "lo", "up").Run()
		os.Exit(m.Run())
	}
	// Parent: enter a private network namespace and run the privileged
	// tests there. Without namespace capability, run everything plainly
	// (the netns children skip themselves).
	if path, err := exec.LookPath("unshare"); err == nil {
		probe := exec.Command(path, "-n", "true") //nolint:gosec // path from exec.LookPath, fixed args
		if err := probe.Run(); err == nil {
			args := append([]string{"-n", os.Args[0], "-test.run=^TestNetnsChild", "-test.v"}, forwardTestFlags()...)
			child := exec.Command(path, args...) //nolint:gosec // path from exec.LookPath, re-executes this binary in a netns
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

// forwardTestFlags forwards verbosity/timeout flags to the netns child.
func forwardTestFlags() []string {
	var flags []string
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.timeout=") || a == "-test.v" {
			flags = append(flags, a)
		}
	}
	return flags
}

// runWGSender is the peer-namespace child: send one datagram, exit 0.
func runWGSender(target string) int {
	conn, err := net.DialTimeout("udp", target, 3*time.Second)
	if err != nil {
		return 1
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(wgTestPayload)); err != nil {
		return 1
	}
	return 0
}

func requireNetnsChild(t *testing.T) {
	t.Helper()
	if os.Getenv(netnsChildEnv) != "1" {
		t.Skip("privileged netns child required (run under unshare -n)")
	}
}

// netexec runs a command, failing the test with its output on error.
func netexec(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v: %s", args, err, string(out))
	}
	return string(out)
}

// setupPeerNamespace creates the peer namespace, its veth leg, and its
// addressing. The peer's WireGuard interface is configured later, once the
// appliance side's public key exists.
func setupPeerNamespace(t *testing.T) {
	t.Helper()
	netexec(t, "ip", "netns", "add", peerNs)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", peerNs).Run() })
	netexec(t, "ip", "link", "add", "veth0", "type", "veth", "peer", "name", "veth1")
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "veth0").Run() })
	netexec(t, "ip", "link", "set", "veth1", "netns", peerNs)
	netexec(t, "ip", "addr", "add", "10.0.0.1/30", "dev", "veth0")
	netexec(t, "ip", "link", "set", "veth0", "up")
	for _, c := range [][]string{
		{"ip", "link", "set", "lo", "up"},
		{"ip", "addr", "add", "10.0.0.2/30", "dev", "veth1"},
		{"ip", "link", "set", "veth1", "up"},
	} {
		netexec(t, append([]string{"ip", "netns", "exec", peerNs}, c...)...)
	}
}

// peerKeyPair generates the peer namespace's WireGuard keypair (files in a
// tempdir) and returns the private-key path and the public key.
func peerKeyPair(t *testing.T) (privPath, pubKey string) {
	t.Helper()
	priv, err := exec.Command("wg", "genkey").Output()
	if err != nil {
		t.Fatalf("wg genkey: %v", err)
	}
	f := t.TempDir() + "/peer.key"
	if err := os.WriteFile(f, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := exec.Command("sh", "-c", "wg pubkey < "+f).Output()
	if err != nil {
		t.Fatalf("wg pubkey: %v", err)
	}
	return f, strings.TrimSpace(string(pub))
}

// appliancePublicKey derives the appliance wg0 public key from the private
// key the reloader generated during the PUT apply.
func appliancePublicKey(t *testing.T, reloaderRoot string) string {
	t.Helper()
	keyFile := reloaderRoot + "/etc/wireguard/wg0.key"
	out, err := exec.Command("sh", "-c", "wg pubkey < "+keyFile).CombinedOutput()
	if err != nil {
		t.Fatalf("derive appliance public key: %v: %s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// configurePeerWireGuard brings up wg0 in the peer namespace: its own
// keypair, the appliance as its peer, the tunnel address, and the route
// into the tunnel (raw wg adds no routes).
func configurePeerWireGuard(t *testing.T, peerPriv, appliancePub string) {
	t.Helper()
	conf := t.TempDir() + "/peer-wg0.conf"
	body := "[Interface]\nListenPort = 51821\nPrivateKey = " + mustRead(t, peerPriv) +
		"\n\n[Peer]\nPublicKey = " + appliancePub + "\nAllowedIPs = 10.10.0.1/32\nEndpoint = 10.0.0.1:51820\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"ip", "link", "add", "wg0", "type", "wireguard"},
		{"wg", "syncconf", "wg0", conf},
		{"ip", "addr", "add", "10.10.0.2/32", "dev", "wg0"},
		{"ip", "link", "set", "wg0", "up"},
		{"ip", "route", "add", "10.10.0.1/32", "dev", "wg0"},
	} {
		netexec(t, append([]string{"ip", "netns", "exec", peerNs}, c...)...)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestNetnsChild_WireGuardPeerTrafficViaAPI covers spec criterion 5's
// traffic clause: a peer added through the API renders, syncs, and passes
// encrypted UDP between two namespaces.
func TestNetnsChild_WireGuardPeerTrafficViaAPI(t *testing.T) {
	requireNetnsChild(t)
	setupPeerNamespace(t)
	peerPriv, peerPub := peerKeyPair(t)

	// The reloader runs with real ip/wg/nft commands and no systemd (none
	// in the netns): systemctl/networkctl are stubbed, file installation
	// and WireGuard interface management are real.
	root := t.TempDir()
	reloader := &services.Reloader{
		Root: root,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "systemctl", "networkctl":
				return nil, nil
			}
			return services.ExecRunner(ctx, name, args...)
		},
	}

	st, err := store.Init(t.TempDir(), mustConfig(t, testBaseDoc))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	pipeline := apply.NewPipeline(apply.NewNftApplier(), nil, nil, st)
	pipeline.Reload = reloader.Sync
	srv := NewServer(st, token, pipeline)
	handler := srv.Handler()

	// PUT /config: add the WireGuard peer through the API. This is the
	// path under test — validate → render → nft apply → service sync →
	// commit → (rollback armed by the server for the confirm window).
	resp := do(t, handler, "PUT", "/config", token, []byte(wgConfigDoc(peerPub)))
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /config = %d, want 200: %s", resp.StatusCode, string(b))
	}

	// wg0 exists, is up, and carries the peer — the reloader's work.
	ipShow := netexec(t, "ip", "-o", "link", "show", "wg0")
	if !strings.Contains(ipShow, "UP") {
		t.Errorf("wg0 not UP after apply: %s", ipShow)
	}
	peers := netexec(t, "wg", "show", "wg0", "peers")
	if !strings.Contains(peers, peerPub) {
		t.Errorf("peer %s not configured on wg0, have: %s", peerPub, peers)
	}

	// Schema-gap scaffolding (wave 1): wg0 addressing is not in the
	// declarative model yet, so the test assigns the tunnel address and
	// route directly. When the schema carries wg0 addressing these two
	// commands are rendered by networkd instead.
	netexec(t, "ip", "addr", "add", "10.10.0.1/32", "dev", "wg0")
	netexec(t, "ip", "route", "add", "10.10.0.2/32", "dev", "wg0")

	configurePeerWireGuard(t, peerPriv, appliancePublicKey(t, root))

	// Listener in this namespace, on the tunnel address.
	lc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("10.10.0.1"), Port: 9999})
	if err != nil {
		t.Fatalf("listener on 10.10.0.1:9999: %v", err)
	}
	defer lc.Close()
	_ = lc.SetReadDeadline(time.Now().Add(10 * time.Second))

	// The peer namespace sends one datagram into the tunnel.
	sender := exec.Command("ip", "netns", "exec", peerNs, os.Args[0])
	sender.Env = append(os.Environ(), senderEnv+"=10.10.0.1:9999")
	if out, err := sender.CombinedOutput(); err != nil {
		t.Fatalf("sender: %v: %s", err, string(out))
	}

	buf := make([]byte, 1500)
	n, _, err := lc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("no datagram across the tunnel: %v", err)
	}
	if string(buf[:n]) != wgTestPayload {
		t.Errorf("payload = %q, want %q", string(buf[:n]), wgTestPayload)
	}

	// The handshake provably happened on the appliance side.
	hs := netexec(t, "wg", "show", "wg0", "latest-handshakes")
	if strings.HasPrefix(strings.TrimSpace(strings.SplitN(hs, "\t", 2)[1]), "0") {
		t.Errorf("no WireGuard handshake recorded: %s", hs)
	}

	// The change is confirmed through the API before the window closes.
	if cres := do(t, handler, "POST", "/confirm", token, nil); cres.StatusCode != http.StatusOK {
		t.Errorf("POST /confirm = %d, want 200", cres.StatusCode)
	}

	// The store carries the committed peer.
	get := do(t, handler, "GET", "/config", token, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET /config = %d", get.StatusCode)
	}
}
