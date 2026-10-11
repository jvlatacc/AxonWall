// Package render turns a validated configuration document into the
// concrete artifacts the appliance runs: the complete nftables ruleset and
// the service daemon configs (systemd-networkd units, dnsmasq, unbound,
// wireguard). Rendering is a pure function of the configuration — the same
// config always renders byte-identical output, which is what makes the
// golden-file tests and the apply pipeline's determinism guarantees hold.
//
// The nftables ruleset models OPNsense's pf semantics; docs/rules-semantics.md
// records the translation decisions (one base chain per hook, first-match
// evaluation, NAT placement). docs/service-renderers.md records the service
// renderers' decisions.
package render

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// Table is the single nftables table AxonWall owns. The appliance runs a
// single-owner ruleset (spec control-plane decision): the rendered ruleset
// starts with `flush ruleset` and the alias refresher only touches sets in
// this table.
const Table = "axonwall"

// Rendered holds every artifact a config renders into: the nftables
// ruleset plus one artifact per service renderer. A nil service artifact
// (Dnsmasq, Unbound, WireGuard) means the corresponding config section is
// absent — the service reloader removes previously rendered files for it.
type Rendered struct {
	Nft []byte
	// Networkd holds systemd-networkd units by file name (the networkd
	// renderer emits .network and .link units for every interface).
	Networkd map[string][]byte
	// Dnsmasq is the complete dnsmasq.conf, or nil when services.dhcp is
	// absent.
	Dnsmasq []byte
	// Unbound is the complete unbound.conf, or nil when services.dns is
	// absent.
	Unbound []byte
	// WireGuard is the wg0 syncconf config, or nil when services.wireguard
	// is absent.
	WireGuard []byte
}

// All renders every render target for cfg. sourceRev annotates the
// rendered headers with the store revision the config came from ("" for
// candidates).
func All(cfg *config.Config, sourceRev string) (*Rendered, error) {
	// The store and API validate before rendering; re-checking here keeps
	// the renderer honest as a standalone entry point (bad input is a
	// renderer error, never a malformed artifact).
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	nftR := &renderer{cfg: cfg, rev: sourceRev}
	if err := nftR.render(); err != nil {
		return nil, err
	}
	networkd, err := renderNetworkd(cfg, sourceRev)
	if err != nil {
		return nil, err
	}
	dnsmasq, err := renderDnsmasq(cfg, sourceRev)
	if err != nil {
		return nil, err
	}
	unbound, err := renderUnbound(cfg, sourceRev)
	if err != nil {
		return nil, err
	}
	wireguard, err := renderWireGuard(cfg, sourceRev)
	if err != nil {
		return nil, err
	}
	return &Rendered{
		Nft:       []byte(nftR.out.String()),
		Networkd:  networkd,
		Dnsmasq:   dnsmasq,
		Unbound:   unbound,
		WireGuard: wireguard,
	}, nil
}

type renderer struct {
	cfg *config.Config
	rev string
	out strings.Builder
}

func (r *renderer) pf(format string, args ...any) {
	fmt.Fprintf(&r.out, format+"\n", args...)
}

func (r *renderer) render() error {
	r.pf("#!/usr/sbin/nft -f")
	r.pf("# AxonWall managed ruleset — rendered from the declarative config store.")
	if r.rev == "" {
		r.pf("# Source revision: uncommitted candidate")
	} else {
		r.pf("# Source revision: %s", r.rev)
	}
	r.pf("# DO NOT EDIT — regenerated on every apply; hand edits are out of model.")
	r.pf("")
	r.pf("flush ruleset")
	r.pf("")
	r.pf("table inet %s {", Table)

	if err := r.renderZoneSets(); err != nil {
		return err
	}
	if err := r.renderAliasSets(); err != nil {
		return err
	}
	dstnat, srcnat, fwdAssoc, err := r.renderNAT()
	if err != nil {
		return err
	}
	if len(dstnat) > 0 {
		r.pf("")
		r.pf("    # ----- NAT: destination (port-forward) -----")
		r.renderChain("dstnat", "nat hook prerouting priority dstnat", "accept", dstnat)
	}
	if len(srcnat) > 0 {
		r.pf("")
		r.pf("    # ----- NAT: source (masquerade) -----")
		r.renderChain("srcnat", "nat hook postrouting priority srcnat", "accept", srcnat)
	}
	if err := r.renderFilterChains(fwdAssoc); err != nil {
		return err
	}

	r.pf("}")
	return nil
}

// kernelName maps a logical interface name (wan0) to the kernel device it
// binds to (enp1s0) — iifname/oifname match kernel names.
func (r *renderer) kernelName(logical string) (string, error) {
	return kernelNameOf(r.cfg, logical)
}

// renderZoneSets emits one ifname set per zone with the kernel device names
// of its interfaces.
func (r *renderer) renderZoneSets() error {
	first := true
	for _, name := range sortedZoneNames(r.cfg) {
		if first {
			r.pf("")
			r.pf("    # ----- zones: interface membership (kernel device names) -----")
			first = false
		}
		z := r.cfg.Zones[name]
		members := make([]string, 0, len(z.Interfaces))
		for _, in := range z.Interfaces {
			k, err := r.kernelName(in)
			if err != nil {
				return fmt.Errorf("render: zone %q: %w", name, err)
			}
			members = append(members, fmt.Sprintf("%q", k))
		}
		sort.Strings(members)
		r.pf("    set zone-%s { type ifname; elements = { %s } }", name, strings.Join(members, ", "))
	}
	return nil
}

// renderAliasSets emits one address set per alias. Sets carrying CIDR
// prefixes need the interval flag; url-table sets are always interval-flagged
// (feeds legitimately mix bare addresses and prefixes) with the feed URL
// recorded as a comment (entries are the last-known seed or refresh).
func (r *renderer) renderAliasSets() error {
	first := true
	for _, name := range sortedAliasNames(r.cfg) {
		a := r.cfg.Firewall.Aliases[name]
		if first {
			r.pf("")
			r.pf("    # ----- aliases: named sets -----")
			first = false
		}
		family := "ipv4_addr"
		switch a.Type {
		case "ipv4", "url-table":
			// already the default family
		case "ipv6":
			family = "ipv6_addr"
		default:
			return fmt.Errorf("render: alias %q: unsupported type %q", name, a.Type)
		}
		if a.Type == "url-table" {
			r.pf("    # url-table: runtime set refreshed from %s", a.URL)
			if len(a.Entries) == 0 {
				r.pf("    set alias-%s { type %s; flags interval; }", name, family)
				continue
			}
			r.pf("    set alias-%s { type %s; flags interval; elements = { %s } }",
				name, family, strings.Join(sortedUnique(a.Entries), ", "))
			continue
		}
		if hasCIDREntry(a.Entries) {
			r.pf("    set alias-%s { type %s; flags interval; elements = { %s } }",
				name, family, strings.Join(sortedUnique(a.Entries), ", "))
			continue
		}
		if len(a.Entries) == 0 {
			r.pf("    set alias-%s { type %s; }", name, family)
			continue
		}
		r.pf("    set alias-%s { type %s; elements = { %s } }",
			name, family, strings.Join(sortedUnique(a.Entries), ", "))
	}
	return nil
}

// renderNAT emits the destination-NAT (port-forward, prerouting) and
// source-NAT (masquerade, postrouting) lines, plus the forward-chain
// allowances each port-forward needs to actually pass traffic — the
// equivalent of OPNsense's filter rule association. Empty chains are
// omitted entirely.
func (r *renderer) renderNAT() (dstnat, srcnat, fwdAssoc []string, err error) {
	for i := range r.cfg.Firewall.NAT {
		n := &r.cfg.Firewall.NAT[i]
		switch n.Mode {
		case "masquerade":
			kOut, kErr := r.kernelName(n.Out)
			if kErr != nil {
				return nil, nil, nil, fmt.Errorf("render: nat %q: %w", n.Name, kErr)
			}
			if _, ok := r.cfg.Zones[n.Source]; !ok {
				return nil, nil, nil, fmt.Errorf("render: nat %q: unknown zone %q", n.Name, n.Source)
			}
			srcnat = append(srcnat, fmt.Sprintf("        iifname @zone-%s oifname %q masquerade comment %q",
				n.Source, kOut, n.Name))
		case "port-forward":
			d, a, pErr := r.portForwardLines(n)
			if pErr != nil {
				return nil, nil, nil, pErr
			}
			dstnat = append(dstnat, d)
			fwdAssoc = append(fwdAssoc, a)
		default:
			return nil, nil, nil, fmt.Errorf("render: nat %q: unsupported mode %q", n.Name, n.Mode)
		}
	}
	return dstnat, srcnat, fwdAssoc, nil
}

// portForwardLines renders one port-forward as a dstnat line plus its
// forward-chain association. The association matches the TRANSLATED
// destination — netfilter performs DNAT in prerouting, before the forward
// hook, which is exactly pf's "filter rules see the translated address"
// behavior for inbound redirections (docs/rules-semantics.md).
func (r *renderer) portForwardLines(n *config.NATRule) (dstnat, assoc string, err error) {
	kIn, err := r.kernelName(n.In)
	if err != nil {
		return "", "", fmt.Errorf("render: nat %q: %w", n.Name, err)
	}
	target, err := config.ParsePortForwardTarget(n.To)
	if err != nil {
		return "", "", fmt.Errorf("render: nat %q: to: %w", n.Name, err)
	}
	innerPort := target.Port
	if innerPort == 0 {
		innerPort = n.DstPort
	}
	dstnat = fmt.Sprintf("        iifname %q %s dport %d dnat ip to %s:%d comment %q",
		kIn, n.Proto, n.DstPort, target.IP, innerPort, n.Name)
	assoc = fmt.Sprintf("        iifname %q ip daddr %s %s dport %d accept comment %q",
		kIn, target.IP, n.Proto, innerPort, n.Name+" (port-forward association)")
	return dstnat, assoc, nil
}

// renderFilterChains emits the three filter chains. One base chain per hook
// is the load-bearing decision: within a chain the first matching rule with
// an explicit verdict terminates evaluation (pf first-match), while a
// second chain at the same hook could override an earlier accept.
func (r *renderer) renderFilterChains(fwdAssoc []string) error {
	pol := r.cfg.Firewall.Default
	input, forward, output, err := r.userRules()
	if err != nil {
		return err
	}
	r.pf("")
	r.pf("    # ----- filter: one base chain per hook; first match wins -----")
	r.renderChain("input", "filter hook input priority filter", pol.Input,
		withUserRules(baseRules("input"), input))
	r.renderChain("forward", "filter hook forward priority filter", pol.Forward,
		withUserRules(withAssociations(baseRules("forward"), fwdAssoc), forward))
	r.renderChain("output", "filter hook output priority filter", pol.Output,
		withUserRules(baseRules("output"), output))
	return nil
}

// withUserRules appends the user-rule block (with its section comment) when
// the chain carries any.
func withUserRules(lines, user []string) []string {
	if len(user) == 0 {
		return lines
	}
	out := append(lines, "        # --- user rules: first match wins (config order) ---")
	return append(out, user...)
}

// withAssociations appends the port-forward association block, which sits
// after the base rules and before user rules: it is the NAT rule's implied
// allowance and must not be reordered by user-rule edits.
func withAssociations(lines, assoc []string) []string {
	if len(assoc) == 0 {
		return lines
	}
	out := append(lines, "        # --- port-forward associations (translated destination) ---")
	return append(out, assoc...)
}

// baseRules are the posture rules every filter chain carries before user
// rules: loopback, invalid-state teardown, established/related accept
// (stateful by default, like pf), and ICMP/ICMPv6.
func baseRules(chain string) []string {
	var out []string
	switch chain {
	case "input":
		out = append(out, `        iifname "lo" accept`)
	case "output":
		out = append(out, `        oifname "lo" accept`)
	}
	out = append(out,
		"        ct state invalid drop",
		"        ct state established,related accept",
		"        meta l4proto ipv6-icmp accept",
		"        ip protocol icmp accept",
	)
	return out
}

// renderChain prints one chain: declaration, base/user lines, closing brace.
func (r *renderer) renderChain(name, hookType, policy string, lines []string) {
	r.pf("    chain %s {", name)
	r.pf("        type %s; policy %s;", hookType, policy)
	for _, l := range lines {
		r.pf("%s", l)
	}
	r.pf("    }")
}

// userRules renders the config's firewall rules into their chains, in
// config order (config order is the documented priority; first match wins).
func (r *renderer) userRules() (input, forward, output []string, err error) {
	for i := range r.cfg.Firewall.Rules {
		ru := &r.cfg.Firewall.Rules[i]
		lines, lErr := r.filterRuleLines(ru)
		if lErr != nil {
			return nil, nil, nil, lErr
		}
		for chain, line := range lines {
			switch chain {
			case "input":
				input = append(input, line)
			case "forward":
				forward = append(forward, line)
			case "output":
				output = append(output, line)
			}
		}
	}
	return input, forward, output, nil
}

// filterRuleLines maps one config rule to the chains it belongs in and the
// nftables line for each: zone→firewall lives in input, zone→zone in
// forward, firewall→zone in output. firewall→firewall restricts both to lo.
func (r *renderer) filterRuleLines(ru *config.Rule) (map[string]string, error) {
	base, err := r.ruleMatches(ru)
	if err != nil {
		return nil, err
	}
	type edge struct{ chain, devExpr string }
	edges := []edge{}
	fromFW := ru.From == config.FirewallZone
	toFW := ru.To == config.FirewallZone
	switch {
	case fromFW && toFW:
		edges = append(edges, edge{"input", `iifname "lo"`}, edge{"output", `oifname "lo"`})
	case fromFW:
		edges = append(edges, edge{"output", ""})
	case toFW:
		edges = append(edges, edge{"input", ""})
	default:
		edges = append(edges, edge{"forward", ""})
	}
	out := make(map[string]string, len(edges))
	for _, e := range edges {
		parts := []string{}
		if e.devExpr != "" {
			parts = append(parts, e.devExpr)
		}
		parts = append(parts, base...)
		out[e.chain] = "        " + strings.Join(parts, " ") +
			" " + ru.Verdict + " comment \"" + ru.Name + "\""
	}
	return out, nil
}

// ruleMatches builds the match expressions for a rule, sans verdict.
func (r *renderer) ruleMatches(ru *config.Rule) ([]string, error) {
	var parts []string
	switch {
	// Zone membership is an interface match against the zone's ifname set;
	// each side contributes its own direction expression.
	case ru.From != config.FirewallZone && ru.To != config.FirewallZone:
		if _, ok := r.cfg.Zones[ru.From]; !ok {
			return nil, fmt.Errorf("render: rule %q: unknown zone %q", ru.Name, ru.From)
		}
		if _, ok := r.cfg.Zones[ru.To]; !ok {
			return nil, fmt.Errorf("render: rule %q: unknown zone %q", ru.Name, ru.To)
		}
		parts = append(parts, "iifname @zone-"+ru.From, "oifname @zone-"+ru.To)
	case ru.From != config.FirewallZone:
		if _, ok := r.cfg.Zones[ru.From]; !ok {
			return nil, fmt.Errorf("render: rule %q: unknown zone %q", ru.Name, ru.From)
		}
		parts = append(parts, "iifname @zone-"+ru.From)
	case ru.To != config.FirewallZone:
		if _, ok := r.cfg.Zones[ru.To]; !ok {
			return nil, fmt.Errorf("render: rule %q: unknown zone %q", ru.Name, ru.To)
		}
		parts = append(parts, "oifname @zone-"+ru.To)
	}
	if ru.SourceAlias != "" {
		a, ok := r.cfg.Firewall.Aliases[ru.SourceAlias]
		if !ok {
			return nil, fmt.Errorf("render: rule %q: source-alias %q is not defined", ru.Name, ru.SourceAlias)
		}
		fam := "ip"
		if a.Type == "ipv6" {
			fam = "ip6"
		}
		parts = append(parts, fam+" saddr @alias-"+ru.SourceAlias)
	}
	if ru.Service != "" {
		proto, port, err := config.ExpandService(ru.Service)
		if err != nil {
			return nil, fmt.Errorf("render: rule %q: service: %w", ru.Name, err)
		}
		parts = append(parts, fmt.Sprintf("%s dport %d", proto, port))
	}
	return parts, nil
}

func sortedZoneNames(c *config.Config) []string {
	names := make([]string, 0, len(c.Zones))
	for name := range c.Zones {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedAliasNames(c *config.Config) []string {
	names := make([]string, 0, len(c.Firewall.Aliases))
	for name := range c.Firewall.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hasCIDREntry reports whether any entry is a prefix (which requires the
// interval flag on the nftables set).
func hasCIDREntry(entries []string) bool {
	for _, e := range entries {
		if _, err := netip.ParsePrefix(e); err == nil {
			return true
		}
	}
	return false
}

// sortedUnique returns a deterministic element list: nftables sets cannot
// hold duplicates, and stable ordering is what golden tests rely on.
func sortedUnique(entries []string) []string {
	seen := make(map[string]bool, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}
