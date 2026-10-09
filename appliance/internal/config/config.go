// Package config defines the AxonWall wave-1 configuration schema and its
// validation rules.
//
// The config store is the appliance's single source of truth: every visible
// change flows through it, and daemons are render targets that are never
// edited by hand (AxonWall spec, management plane). Parsing is strict —
// unknown fields are rejected so typos fail at the API boundary instead of
// silently disappearing — and semantic validation checks references, subnet
// overlap, and value domains before anything reaches the applier.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version is the only schema version this build understands.
const Version = 1

// WireGuardInterfaceName is the interface name materialized by the WireGuard
// service. Wave 1 supports a single WireGuard interface; a zone that lists
// it does not need a separate `interfaces:` entry for it.
const WireGuardInterfaceName = "wg0"

// FirewallZone is the reserved pseudo-zone name for traffic to and from the
// appliance itself.
const FirewallZone = "firewall"

// Config is the root of the wave-1 configuration document.
type Config struct {
	Version    int             `yaml:"version"`
	Zones      map[string]Zone `yaml:"zones"`
	Interfaces []Interface     `yaml:"interfaces"`
	Services   Services        `yaml:"services"`
	Firewall   Firewall        `yaml:"firewall"`
}

// InterfaceByName returns the interface with the given logical name.
func (c *Config) InterfaceByName(name string) (Interface, bool) {
	for i := range c.Interfaces {
		if c.Interfaces[i].Name == name {
			return c.Interfaces[i], true
		}
	}
	return Interface{}, false
}

// Default returns the first-boot configuration: a safe default-drop
// gateway with one WAN (DHCP) and one LAN (static) interface. First-boot
// setup refines it; it exists so a fresh install boots with the firewall
// up and the API reachable on the LAN.
func Default() *Config {
	return &Config{
		Version: Version,
		Zones: map[string]Zone{
			"wan": {Interfaces: []string{"wan0"}},
			"lan": {Interfaces: []string{"lan0"}},
		},
		Interfaces: []Interface{
			{Name: "wan0", Match: "enp1s0", Addressing: "dhcp"},
			{Name: "lan0", Match: "enp2s0", Addressing: "static",
				Address: []string{"192.168.1.1/24"}},
		},
		Services: Services{
			DNS: &DNS{
				Resolver: "unbound",
				Listen:   []string{"lan"},
			},
			DHCP: &DHCP{
				Pools: []DHCPPool{{
					Zone:    "lan",
					Range:   [2]string{"192.168.1.100", "192.168.1.199"},
					Gateway: "192.168.1.1",
					DNS:     "192.168.1.1",
				}},
			},
		},
		Firewall: Firewall{
			Default: Defaults{Input: "drop", Forward: "drop", Output: "accept"},
		},
	}
}

// Zone groups interfaces for rule addressing. Interface membership is
// exclusive: an interface belongs to at most one zone.
type Zone struct {
	Interfaces []string `yaml:"interfaces"`
}

// Interface declares a logical interface. Name is the appliance-logical name
// (wan0, lan0); Match is the kernel device it binds to (enp1s0, eth0.100).
type Interface struct {
	Name       string   `yaml:"name"`
	Match      string   `yaml:"match"`
	Addressing string   `yaml:"addressing"` // dhcp | static
	Address    []string `yaml:"address"`    // CIDRs; static only
}

// Services holds the optional service sections. Section presence matters
// (e.g. an absent WireGuard section means no wg interface exists), so each
// is a pointer.
type Services struct {
	DNS       *DNS       `yaml:"dns"`
	DHCP      *DHCP      `yaml:"dhcp"`
	WireGuard *WireGuard `yaml:"wireguard"`
}

// DNS configures the resolver (Unbound in wave 1) and the zones it listens on.
type DNS struct {
	Resolver string   `yaml:"resolver"`
	Listen   []string `yaml:"listen"`
}

// DHCP configures DHCP server pools.
type DHCP struct {
	Pools []DHCPPool `yaml:"pools"`
}

// DHCPPool hands out addresses in [Range[0], Range[1]] for a zone's subnet.
type DHCPPool struct {
	Zone    string    `yaml:"zone"`
	Range   [2]string `yaml:"range"`
	Gateway string    `yaml:"gateway"`
	DNS     string    `yaml:"dns"`
}

// WireGuard configures the single wave-1 WireGuard interface.
type WireGuard struct {
	ListenPort int      `yaml:"listen-port"`
	Peers      []WGPeer `yaml:"peers"`
}

// WGPeer is one WireGuard peer.
type WGPeer struct {
	Name       string   `yaml:"name"`
	PublicKey  string   `yaml:"public-key"`
	AllowedIPs []string `yaml:"allowed-ips"`
}

// Firewall holds the packet-filtering policy.
type Firewall struct {
	Default Defaults         `yaml:"default"`
	Aliases map[string]Alias `yaml:"aliases"`
	NAT     []NATRule        `yaml:"nat"`
	Rules   []Rule           `yaml:"rules"`
}

// Defaults are the base chain policies. nftables chain policies are accept
// or drop only (reject is a rule verdict, not a policy).
type Defaults struct {
	Input   string `yaml:"input"`
	Forward string `yaml:"forward"`
	Output  string `yaml:"output"`
}

// Alias is a named set of addresses (nftables named set in the renderer).
//
// Types ipv4 and ipv6 hold static entries. Type url-table names a source
// URL whose content (one address or CIDR per line) populates the set: the
// config carries the seed entries (possibly none) and the URL, while axond
// refreshes the runtime set from the URL without a config transaction.
// URL-table sets are IPv4 — the wave-1 feed format and the common blocklist
// case; IPv6 feeds are later work.
type Alias struct {
	Type    string   `yaml:"type"` // ipv4 | ipv6 | url-table
	URL     string   `yaml:"url"`  // url-table only: feed source
	Entries []string `yaml:"entries"`
}

// NATRule is a NAT rule. Mode masquerade source-NATs a zone's traffic as it
// leaves an interface. Mode port-forward DNATs a public port on an ingress
// interface to an internal host (IPv4, optionally with a rewritten port);
// the renderer also emits the matching forward-chain allowance, the
// equivalent of OPNsense's filter rule association.
type NATRule struct {
	Name   string `yaml:"name"`
	Out    string `yaml:"out"`    // masquerade: egress interface (logical)
	Source string `yaml:"source"` // masquerade: source zone
	Mode   string `yaml:"mode"`

	// Port-forward fields (mode: port-forward). Out and Source must be
	// empty in that mode; the fields above belong to masquerade.
	In      string `yaml:"in"`       // ingress interface (logical)
	Proto   string `yaml:"proto"`    // tcp | udp
	DstPort int    `yaml:"dst-port"` // public port
	To      string `yaml:"to"`       // internal ip or ip:port (IPv4)
}

// Rule is a firewall rule between zones (or FirewallZone for the appliance
// itself).
type Rule struct {
	Name        string `yaml:"name"`
	From        string `yaml:"from"`
	To          string `yaml:"to"`
	Service     string `yaml:"service"`      // named service or proto/port; empty = any
	SourceAlias string `yaml:"source-alias"` // optional alias refining From
	Verdict     string `yaml:"verdict"`
}

// ValidationError reports every problem found with a configuration document.
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 1 {
		return "invalid config: " + e.Issues[0]
	}
	return fmt.Sprintf("invalid config: %d issues:\n  - %s", len(e.Issues), strings.Join(e.Issues, "\n  - "))
}

// Parse decodes a YAML configuration document with strict field checking and
// then applies semantic validation. It never returns both a usable *Config
// and a nil error.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &ValidationError{Issues: []string{"document is empty"}}
		}
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return nil, &ValidationError{Issues: typeErr.Errors}
		}
		return nil, &ValidationError{Issues: []string{err.Error()}}
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Marshal renders a config to canonical YAML for storage.
func Marshal(c *Config) ([]byte, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return data, nil
}
