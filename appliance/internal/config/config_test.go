package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
)

// Valid documents for the valid matrix.
const minimalYAML = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const dhcpWANYAML = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.50.1/24] }
services:
  dhcp:
    pools:
      - { zone: lan, range: [192.168.50.100, 192.168.50.150], gateway: 192.168.50.1, dns: 192.168.50.1 }
firewall:
  default: { input: drop, forward: drop, output: accept }
  aliases:
    net-blocks: { type: ipv4, entries: [10.8.0.0/24] }
  nat:
    - { name: lan-masq, out: wan0, source: lan, mode: masquerade }
  rules:
    - { name: lan-to-wan, from: lan, to: wan, verdict: accept }
`

// wgKeyA and wgKeyB are well-formed base64 32-byte keys for tests.
const (
	wgKeyA = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	wgKeyB = "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI="
)

const wireguardYAML = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  wg: { interfaces: [wg0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.7.1/24] }
services:
  wireguard:
    listen-port: 51820
    peers:
      - { name: laptop, public-key: QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=, allowed-ips: [10.10.0.2/32, fd00::2/128] }
      - { name: phone, public-key: QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=, allowed-ips: [10.10.0.3/32] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  rules:
    - { name: wg-handshake, from: wan, to: firewall, service: udp/51820, verdict: accept }
`

// invalidCase is one entry of the invalid matrix.
type invalidCase struct {
	name   string
	yaml   string
	issues []string // substrings expected among the reported issues
}

const baseInvalid = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const noInterfacesYAML = `
version: 1
zones:
  lan: { interfaces: [] }
interfaces: []
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const missingPolicyYAML = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { forward: drop, output: accept }
`

func TestParseSpecSample(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.yaml")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("spec sample must parse: %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("version = %d, want 1", cfg.Version)
	}
	if len(cfg.Zones) != 3 || len(cfg.Interfaces) != 2 {
		t.Fatalf("sample shape drifted: %d zones, %d interfaces", len(cfg.Zones), len(cfg.Interfaces))
	}
	if len(cfg.Firewall.Rules) != 3 || len(cfg.Firewall.NAT) != 1 || len(cfg.Firewall.Aliases) != 1 {
		t.Fatalf("sample firewall shape drifted")
	}
	if cfg.Services.DNS == nil || cfg.Services.DNS.Resolver != "unbound" {
		t.Fatalf("dns section not parsed as expected")
	}
	wg := cfg.Services.WireGuard
	if wg == nil || wg.ListenPort != 51820 || len(wg.Peers) != 1 {
		t.Fatalf("wireguard section not parsed as expected")
	}
}

func TestParseValidMatrix(t *testing.T) {
	valid := map[string]string{
		"minimal":   minimalYAML,
		"dhcp-wan":  dhcpWANYAML,
		"wireguard": wireguardYAML,
	}
	for name, doc := range valid {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(doc)); err != nil {
				t.Fatalf("expected valid, got: %v", err)
			}
		})
	}
}

func TestParseInvalidMatrix(t *testing.T) {
	cases := []invalidCase{
		{
			name:   "unknown field",
			yaml:   baseInvalid + "\nbogus: true\n",
			issues: []string{"bogus"},
		},
		{
			name:   "unsupported version",
			yaml:   strings.Replace(baseInvalid, "version: 1", "version: 2", 1),
			issues: []string{"unsupported schema version 2"},
		},
		{
			name:   "zero version",
			yaml:   strings.Replace(baseInvalid, "version: 1", "version: 0", 1),
			issues: []string{"unsupported schema version 0"},
		},
		{
			name:   "no interfaces",
			yaml:   noInterfacesYAML,
			issues: []string{"at least one interface"},
		},
		{
			name: "zone references unknown interface",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0, nope0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"undefined interface"},
		},
		{
			name: "wg zone without wireguard service",
			yaml: `
version: 1
zones:
  wg: { interfaces: [wg0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"no wireguard service"},
		},
		{
			name: "declared wg interface",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
  - { name: wg0, match: eth1, addressing: dhcp }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"materialized by the wireguard service"},
		},
		{
			name: "interface in two zones",
			yaml: `
version: 1
zones:
  wan: { interfaces: [lan0] }
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"already assigned to zone"},
		},
		{
			name: "bad interface name",
			yaml: `
version: 1
zones:
  lan: { interfaces: [LAN_0] }
interfaces:
  - { name: LAN_0, match: eth0, addressing: static, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"invalid interface name"},
		},
		{
			name:   "bad match pattern",
			yaml:   strings.Replace(baseInvalid, "match: eth0", "match: $$$", 1),
			issues: []string{"invalid match pattern"},
		},
		{
			name:   "bad addressing",
			yaml:   strings.Replace(baseInvalid, "addressing: static", "addressing: auto", 1),
			issues: []string{"invalid value", "one of"},
		},
		{
			name: "static without address",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"requires at least one address"},
		},
		{
			name: "dhcp with static addresses",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: dhcp, address: [10.0.0.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"must not list static addresses"},
		},
		{
			name:   "invalid CIDR",
			yaml:   strings.Replace(baseInvalid, "10.0.0.1/24", "10.0.0.1", 1),
			issues: []string{"invalid CIDR"},
		},
		{
			name: "overlapping subnets",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0] }
  dmz: { interfaces: [dmz0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
  - { name: dmz0, match: eth1, addressing: static, address: [10.0.0.200/25] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"overlapping subnets"},
		},
		{
			name: "duplicate interface name",
			yaml: `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [10.0.0.1/24] }
  - { name: lan0, match: eth1, addressing: static, address: [10.0.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`,
			issues: []string{"duplicate interface name"},
		},
		{
			name:   "dns resolver not unbound",
			yaml:   minimalYAML + "services:\n  dns:\n    resolver: dnsmasq\n    listen: [lan]\n",
			issues: []string{`must be "unbound"`},
		},
		{
			name:   "dns listen unknown zone",
			yaml:   minimalYAML + "services:\n  dns:\n    resolver: unbound\n    listen: [nosuch]\n",
			issues: []string{"unknown zone"},
		},
		{
			name:   "dns empty listen",
			yaml:   minimalYAML + "services:\n  dns:\n    resolver: unbound\n    listen: []\n",
			issues: []string{"must list at least one zone"},
		},
		{
			name: "dhcp pool unknown zone",
			yaml: minimalYAML + `services:
  dhcp:
    pools:
      - { zone: nosuch, range: [10.0.0.100, 10.0.0.200], gateway: 10.0.0.1, dns: 10.0.0.1 }
`,
			issues: []string{"unknown zone"},
		},
		{
			name: "dhcp pool bad range",
			yaml: minimalYAML + `services:
  dhcp:
    pools:
      - { zone: lan, range: [bogus, 10.0.0.200], gateway: 10.0.0.1, dns: 10.0.0.1 }
`,
			issues: []string{"invalid IP"},
		},
		{
			name: "dhcp pool reversed range",
			yaml: minimalYAML + `services:
  dhcp:
    pools:
      - { zone: lan, range: [10.0.0.200, 10.0.0.100], gateway: 10.0.0.1, dns: 10.0.0.1 }
`,
			issues: []string{"must be below"},
		},
		{
			name: "dhcp pool outside subnet",
			yaml: strings.Replace(minimalYAML, "10.0.0.1/24", "192.168.1.1/24", 1) + `services:
  dhcp:
    pools:
      - { zone: lan, range: [10.0.0.100, 10.0.0.200], gateway: 10.0.0.1, dns: 10.0.0.1 }
`,
			issues: []string{"outside the subnets of zone"},
		},
		{
			name: "dhcp overlapping pools",
			yaml: minimalYAML + `services:
  dhcp:
    pools:
      - { zone: lan, range: [10.0.0.100, 10.0.0.150], gateway: 10.0.0.1, dns: 10.0.0.1 }
      - { zone: lan, range: [10.0.0.120, 10.0.0.200], gateway: 10.0.0.1, dns: 10.0.0.1 }
`,
			issues: []string{"overlapping ranges"},
		},
		{
			name: "dhcp gateway outside subnet",
			yaml: minimalYAML + `services:
  dhcp:
    pools:
      - { zone: lan, range: [10.0.0.100, 10.0.0.200], gateway: 10.0.5.1, dns: 10.0.0.1 }
`,
			issues: []string{"outside the subnets of zone"},
		},
		{
			name:   "wireguard bad port",
			yaml:   strings.Replace(wireguardYAML, "listen-port: 51820", "listen-port: 70000", 1),
			issues: []string{"between 1 and 65535"},
		},
		{
			name:   "wireguard bad key",
			yaml:   strings.Replace(wireguardYAML, wgKeyA, "not-base64!!", 1),
			issues: []string{"invalid base64"},
		},
		{
			name:   "wireguard short key",
			yaml:   strings.Replace(wireguardYAML, wgKeyA, base64.StdEncoding.EncodeToString([]byte("too short")), 1),
			issues: []string{"32 bytes"},
		},
		{
			name:   "wireguard duplicate peer name",
			yaml:   strings.Replace(wireguardYAML, "name: phone", "name: laptop", 1),
			issues: []string{"duplicate peer name"},
		},
		{
			name:   "wireguard bad allowed-ips",
			yaml:   strings.Replace(wireguardYAML, "10.10.0.2/32, fd00::2/128", "10.10.0.2/99", 1),
			issues: []string{"invalid CIDR"},
		},
		{
			name:   "bad chain policy",
			yaml:   strings.Replace(baseInvalid, "input: drop", "input: reject", 1),
			issues: []string{"firewall.default.input", "invalid value"},
		},
		{
			name:   "missing chain policy",
			yaml:   missingPolicyYAML,
			issues: []string{"firewall.default.input", "must be set"},
		},
		{
			name:   "unknown alias type",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  aliases:\n    macs: { type: mac, entries: [aa:bb:cc:dd:ee:ff] }\n  default", 1),
			issues: []string{"invalid value", "one of"},
		},
		{
			name:   "alias bad entry",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  aliases:\n    hosts: { type: ipv4, entries: [banana] }\n  default", 1),
			issues: []string{"invalid ipv4 address or CIDR"},
		},
		{
			name:   "alias family mismatch",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  aliases:\n    v6s: { type: ipv4, entries: [\"::1\"] }\n  default", 1),
			issues: []string{"not an IPv4"},
		},
		{
			name:   "nat unknown interface",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  nat:\n    - { name: masq, out: nope0, source: lan, mode: masquerade }\n  default", 1),
			issues: []string{"undefined interface"},
		},
		{
			name:   "nat unknown zone",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  nat:\n    - { name: masq, out: lan0, source: nosuch, mode: masquerade }\n  default", 1),
			issues: []string{"unknown zone"},
		},
		{
			name:   "nat bad mode",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  nat:\n    - { name: masq, out: lan0, source: lan, mode: snat }\n  default", 1),
			issues: []string{"invalid value", "one of"},
		},
		{
			name:   "rule unknown zone",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: nosuch, to: lan, verdict: accept }\n  default", 1),
			issues: []string{"unknown zone"},
		},
		{
			name:   "rule unknown alias",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: lan, to: lan, source-alias: ghost, verdict: accept }\n  default", 1),
			issues: []string{"source-alias", "not defined"},
		},
		{
			name:   "rule bad verdict",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: lan, to: lan, verdict: forward }\n  default", 1),
			issues: []string{"invalid value", "one of"},
		},
		{
			name:   "rule bad service port",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: lan, to: lan, service: tcp/99999, verdict: accept }\n  default", 1),
			issues: []string{"invalid port"},
		},
		{
			name:   "rule unknown service protocol",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: lan, to: lan, service: sctp/5, verdict: accept }\n  default", 1),
			issues: []string{"unknown service"},
		},
		{
			name:   "duplicate rule name",
			yaml:   strings.Replace(minimalYAML, "firewall:\n  default", "firewall:\n  rules:\n    - { name: r1, from: lan, to: lan, verdict: accept }\n    - { name: r1, from: lan, to: lan, verdict: drop }\n  default", 1),
			issues: []string{"duplicate name"},
		},
		{
			name:   "reserved zone name",
			yaml:   strings.Replace(minimalYAML, "lan: { interfaces: [lan0] }", "firewall: { interfaces: [lan0] }", 1),
			issues: []string{"reserved pseudo-zone"},
		},
		{
			name:   "empty document",
			yaml:   "# nothing here\n",
			issues: []string{"document is empty"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("expected invalid config to be rejected")
			}
			var valErr *ValidationError
			if !errors.As(err, &valErr) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			for _, want := range tc.issues {
				found := false
				for _, issue := range valErr.Issues {
					if strings.Contains(issue, want) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("issues %v do not contain %q", valErr.Issues, want)
				}
			}
		})
	}
}

func TestParseAggregatesIssues(t *testing.T) {
	doc := `
version: 7
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static }
firewall:
  default: { input: drop, forward: drop, output: accept }
`
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("expected error")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(valErr.Issues) < 2 {
		t.Fatalf("expected aggregated issues, got %v", valErr.Issues)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.yaml")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cfg2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse marshaled config: %v", err)
	}
	out2, err := Marshal(cfg2)
	if err != nil {
		t.Fatalf("marshal again: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("round-trip changed the config:\n%s\n---\n%s", out, out2)
	}
}

func TestValidateService(t *testing.T) {
	valid := []string{"ssh", "http", "https", "dns", "tcp/443", "udp/51820", "tcp/1"}
	for _, s := range valid {
		if err := ValidateService(s); err != nil {
			t.Errorf("ValidateService(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{"", "sctp/5", "tcp/0", "tcp/70000", "tcp/x", "tcp", "TCP/443", "http/80"}
	for _, s := range invalid {
		if err := ValidateService(s); err == nil {
			t.Errorf("ValidateService(%q) = nil, want error", s)
		}
	}
}

func TestParseWGKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := ParseWGKey(good); err != nil {
		t.Errorf("ParseWGKey(valid) = %v, want nil", err)
	}
	if _, err := ParseWGKey("short"); err == nil {
		t.Errorf("ParseWGKey(short) = nil, want error")
	}
}
