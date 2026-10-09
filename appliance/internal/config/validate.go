package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// nameRE covers zone, logical-interface, and alias names: lowercase,
// start with a letter, may contain digits and dashes.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// matchRE covers kernel device names: eth0, enp1s0, wlp3s0, eth0.100, ens3.
var matchRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.:@_-]{0,30}$`)

var (
	validPolicies   = []string{"accept", "drop"}
	validVerdicts   = []string{"accept", "drop", "reject"}
	validAliasTypes = []string{"ipv4", "ipv6", "url-table"}
	validAddressing = []string{"dhcp", "static"}
	validNATModes   = []string{"masquerade", "port-forward"}
	validProtos     = []string{"tcp", "udp"}
)

// namedServices maps the small set of named services wave 1 understands to
// their proto/port expansion.
var namedServices = map[string]string{
	"ssh":   "tcp/22",
	"http":  "tcp/80",
	"https": "tcp/443",
	"dns":   "udp/53",
}

// ValidateService checks a service expression: a named service (ssh, http,
// https, dns) or "proto/port" with proto in {tcp, udp} and port 1-65535.
func ValidateService(s string) error {
	_, _, err := ExpandService(s)
	return err
}

// ExpandService resolves a service expression to its protocol and port.
// Named services expand per namedServices; "proto/port" passes through.
// The renderer consumes this so expansion has one source of truth.
func ExpandService(s string) (proto string, port int, err error) {
	if expr, ok := namedServices[s]; ok {
		s = expr
	}
	proto, portStr, ok := strings.Cut(s, "/")
	if !ok || (proto != "tcp" && proto != "udp") {
		return "", 0, fmt.Errorf("unknown service %q (named: %s, or tcp|udp/port)", s, strings.Join(sortedKeys(namedServices), ", "))
	}
	n, err := strconv.Atoi(portStr)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("invalid port %q in service %q", portStr, s)
	}
	return proto, n, nil
}

// ParseWGKey validates a base64-encoded 32-byte WireGuard public key.
func ParseWGKey(s string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("key must decode to 32 bytes (got %d)", len(k))
	}
	return k, nil
}

// ParseCIDR parses an address in CIDR notation (a.b.c.d/nn).
func ParseCIDR(s string) (*net.IPNet, error) {
	_, ipn, err := net.ParseCIDR(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q", s)
	}
	return ipn, nil
}

type validator struct{ issues []string }

func (v *validator) addf(format string, args ...any) {
	v.issues = append(v.issues, fmt.Sprintf(format, args...))
}

func (v *validator) oneOf(field, val string, allowed []string) bool {
	for _, a := range allowed {
		if val == a {
			return true
		}
	}
	if val == "" {
		v.addf("%s: must be set (one of: %s)", field, strings.Join(allowed, ", "))
	} else {
		v.addf("%s: invalid value %q (one of: %s)", field, val, strings.Join(allowed, ", "))
	}
	return false
}

// Validate checks a configuration against the wave-1 semantic rules and
// returns a *ValidationError listing every problem found.
func Validate(c *Config) error {
	v := &validator{}
	v.checkVersion(c)
	ifn := v.checkInterfaces(c)
	zones := v.checkZones(c, ifn)
	v.checkServices(c, zones, ifn)
	v.checkFirewall(c, zones, ifn)
	if len(v.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: v.issues}
}

func (v *validator) checkVersion(c *Config) {
	if c.Version != Version {
		v.addf("version: unsupported schema version %d (supported: %d)", c.Version, Version)
	}
}

// checkInterfaces validates the interface list and indexes it by name.
func (v *validator) checkInterfaces(c *Config) map[string]*Interface {
	byName := make(map[string]*Interface, len(c.Interfaces))
	type cidrRef struct {
		iface string
		idx   int
		net   *net.IPNet
	}
	var cidrs []cidrRef
	for i := range c.Interfaces {
		ifc := &c.Interfaces[i]
		if ifc.Name == "" {
			v.addf("interfaces[%d]: name must be set", i)
			continue
		}
		field := "interfaces[" + ifc.Name + "]"
		if !nameRE.MatchString(ifc.Name) {
			v.addf("%s: invalid interface name %q (must match %s)", field, ifc.Name, nameRE)
		}
		if ifc.Name == WireGuardInterfaceName {
			v.addf("%s: %q is materialized by the wireguard service and must not be declared", field, WireGuardInterfaceName)
		}
		if prev, dup := byName[ifc.Name]; dup {
			v.addf("%s: duplicate interface name (already defined with match %q)", field, prev.Match)
			continue
		}
		byName[ifc.Name] = ifc
		if !matchRE.MatchString(ifc.Match) {
			v.addf("%s: invalid match pattern %q", field, ifc.Match)
		}
		// The spec's sample omits `addressing` for static interfaces and
		// just lists addresses; infer it so that shape parses as-is.
		if ifc.Addressing == "" && len(ifc.Address) > 0 {
			ifc.Addressing = "static"
		}
		if v.oneOf(field+".addressing", ifc.Addressing, validAddressing) {
			switch ifc.Addressing {
			case "static":
				if len(ifc.Address) == 0 {
					v.addf("%s: static addressing requires at least one address", field)
				}
			case "dhcp":
				if len(ifc.Address) > 0 {
					v.addf("%s: dhcp interfaces must not list static addresses", field)
				}
			}
		}
		for j, a := range ifc.Address {
			ipn, err := ParseCIDR(a)
			if err != nil {
				v.addf("%s.address[%d]: %v", field, j, err)
				continue
			}
			cidrs = append(cidrs, cidrRef{iface: ifc.Name, idx: j, net: ipn})
		}
	}
	// Overlapping subnets make routing ambiguous; every pair must be disjoint.
	for i := range cidrs {
		for j := i + 1; j < len(cidrs); j++ {
			if netsOverlap(cidrs[i].net, cidrs[j].net) {
				v.addf("interfaces[%s].address[%d] and interfaces[%s].address[%d]: overlapping subnets (%s vs %s)",
					cidrs[i].iface, cidrs[i].idx, cidrs[j].iface, cidrs[j].idx, cidrs[i].net, cidrs[j].net)
			}
		}
	}
	if len(c.Interfaces) == 0 {
		v.addf("interfaces: at least one interface must be defined")
	}
	return byName
}

// checkZones validates zone definitions and returns the set of zone names.
func (v *validator) checkZones(c *Config, ifn map[string]*Interface) map[string]bool {
	zones := make(map[string]bool, len(c.Zones))
	assigned := map[string]string{} // interface name -> zone name
	for _, name := range sortedKeys(c.Zones) {
		z := c.Zones[name]
		field := "zones[" + name + "]"
		if name == FirewallZone {
			v.addf("%s: %q is a reserved pseudo-zone", field, FirewallZone)
		} else if !nameRE.MatchString(name) {
			v.addf("%s: invalid zone name (must match %s)", field, nameRE)
		}
		if len(z.Interfaces) == 0 {
			v.addf("%s: zone must list at least one interface", field)
		}
		for _, in := range z.Interfaces {
			if _, ok := ifn[in]; !ok {
				if in == WireGuardInterfaceName {
					if c.Services.WireGuard == nil {
						v.addf("%s: references interface %q but no wireguard service is configured", field, in)
					}
				} else {
					v.addf("%s: references undefined interface %q", field, in)
					continue
				}
			}
			if prevZone, seen := assigned[in]; seen && prevZone != name {
				v.addf("%s: interface %q already assigned to zone %q", field, in, prevZone)
				continue
			}
			assigned[in] = name
		}
		zones[name] = true
	}
	return zones
}

func (v *validator) checkServices(c *Config, zones map[string]bool, ifn map[string]*Interface) {
	if d := c.Services.DNS; d != nil {
		if d.Resolver != "unbound" {
			v.addf("services.dns.resolver: must be %q (got %q)", "unbound", d.Resolver)
		}
		if len(d.Listen) == 0 {
			v.addf("services.dns.listen: must list at least one zone")
		}
		for _, z := range d.Listen {
			if !zones[z] {
				v.addf("services.dns.listen: unknown zone %q", z)
			}
		}
	}
	if d := c.Services.DHCP; d != nil {
		v.checkDHCP(c, d, zones, ifn)
	}
	if w := c.Services.WireGuard; w != nil {
		v.checkWireGuard(w)
	}
}

func (v *validator) checkDHCP(c *Config, d *DHCP, zones map[string]bool, ifn map[string]*Interface) {
	type poolRange struct {
		pool       int
		start, end net.IP
	}
	var ranges []poolRange
	for i := range d.Pools {
		p := &d.Pools[i]
		field := fmt.Sprintf("services.dhcp.pools[%d]", i)
		if !zones[p.Zone] {
			v.addf("%s: unknown zone %q", field, p.Zone)
		}
		start, errStart := parseIP(p.Range[0])
		end, errEnd := parseIP(p.Range[1])
		if errStart != nil {
			v.addf("%s.range: %v", field, errStart)
		}
		if errEnd != nil {
			v.addf("%s.range: %v", field, errEnd)
		}
		if errStart == nil && errEnd == nil {
			if bytes.Compare(start, end) >= 0 {
				v.addf("%s.range: start %s must be below end %s", field, start, end)
			}
			ranges = append(ranges, poolRange{pool: i, start: start, end: end})
			if zones[p.Zone] {
				subs := zoneSubnets(c, p.Zone, ifn)
				if len(subs) > 0 && (!inSubnets(start, subs) || !inSubnets(end, subs)) {
					v.addf("%s.range: %s-%s is outside the subnets of zone %q", field, start, end, p.Zone)
				}
			}
		}
		if p.Gateway == "" {
			v.addf("%s.gateway: must be set", field)
		} else if g := net.ParseIP(p.Gateway); g == nil {
			v.addf("%s.gateway: invalid IP %q", field, p.Gateway)
		} else if zones[p.Zone] {
			subs := zoneSubnets(c, p.Zone, ifn)
			if len(subs) > 0 && !inSubnets(g, subs) {
				v.addf("%s.gateway: %s is outside the subnets of zone %q", field, g, p.Zone)
			}
		}
		if p.DNS == "" {
			v.addf("%s.dns: must be set", field)
		} else if net.ParseIP(p.DNS) == nil {
			v.addf("%s.dns: invalid IP %q", field, p.DNS)
		}
	}
	for i := range ranges {
		for j := i + 1; j < len(ranges); j++ {
			// Ranges overlap when each starts before the other ends.
			if bytes.Compare(ranges[i].start, ranges[j].end) <= 0 && bytes.Compare(ranges[j].start, ranges[i].end) <= 0 {
				v.addf("services.dhcp.pools[%d] and services.dhcp.pools[%d]: overlapping ranges", ranges[i].pool, ranges[j].pool)
			}
		}
	}
}

func (v *validator) checkWireGuard(w *WireGuard) {
	if w.ListenPort < 1 || w.ListenPort > 65535 {
		v.addf("services.wireguard.listen-port: must be between 1 and 65535 (got %d)", w.ListenPort)
	}
	seenName := map[string]bool{}
	seenKey := map[string]bool{}
	for i := range w.Peers {
		p := &w.Peers[i]
		field := fmt.Sprintf("services.wireguard.peers[%d]", i)
		if p.Name == "" {
			v.addf("%s: name must be set", field)
		} else if seenName[p.Name] {
			v.addf("%s: duplicate peer name %q", field, p.Name)
		}
		seenName[p.Name] = true
		if _, err := ParseWGKey(p.PublicKey); err != nil {
			v.addf("%s.public-key: %v", field, err)
		} else if seenKey[p.PublicKey] {
			v.addf("%s.public-key: duplicate key", field)
		}
		seenKey[p.PublicKey] = true
		if len(p.AllowedIPs) == 0 {
			v.addf("%s.allowed-ips: must list at least one CIDR", field)
		}
		for j, a := range p.AllowedIPs {
			if _, _, err := net.ParseCIDR(a); err != nil {
				v.addf("%s.allowed-ips[%d]: invalid CIDR %q", field, j, a)
			}
		}
	}
}

func (v *validator) checkFirewall(c *Config, zones map[string]bool, ifn map[string]*Interface) {
	fw := &c.Firewall
	v.oneOf("firewall.default.input", fw.Default.Input, validPolicies)
	v.oneOf("firewall.default.forward", fw.Default.Forward, validPolicies)
	v.oneOf("firewall.default.output", fw.Default.Output, validPolicies)

	aliases := map[string]bool{}
	for _, name := range sortedKeys(fw.Aliases) {
		a := fw.Aliases[name]
		aliases[name] = true
		field := "firewall.aliases[" + name + "]"
		if !nameRE.MatchString(name) {
			v.addf("%s: invalid alias name (must match %s)", field, nameRE)
		}
		v.oneOf(field+".type", a.Type, validAliasTypes)
		if a.Type == "url-table" {
			// The runtime set is IPv4 (feed-format decision); entries are
			// optional — refresh populates the set from the URL.
			if u, err := url.Parse(a.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				v.addf("%s.url: must be an absolute http(s) URL (got %q)", field, a.URL)
			}
		} else if len(a.Entries) == 0 {
			v.addf("%s: must list at least one entry", field)
		}
		// URL-table sets are IPv4 by decision; validate their entries as such.
		entryType := a.Type
		if entryType == "url-table" {
			entryType = "ipv4"
		}
		for j, e := range a.Entries {
			checkAliasEntry(v, fmt.Sprintf("%s.entries[%d]", field, j), entryType, e)
		}
	}

	seenNAT := map[string]bool{}
	for i := range fw.NAT {
		n := &fw.NAT[i]
		field := fmt.Sprintf("firewall.nat[%d]", i)
		if n.Name == "" {
			v.addf("%s: name must be set", field)
		} else if seenNAT[n.Name] {
			v.addf("%s: duplicate name %q", field, n.Name)
		}
		seenNAT[n.Name] = true
		v.oneOf(field+".mode", n.Mode, validNATModes)
		switch n.Mode {
		case "masquerade":
			// Keep the two modes mutually exclusive so a typo lands as a
			// validation error instead of a silently ignored field.
			if n.In != "" || n.Proto != "" || n.DstPort != 0 || n.To != "" {
				v.addf("%s: port-forward fields (in, proto, dst-port, to) must be empty in masquerade mode", field)
			}
			if _, ok := ifn[n.Out]; !ok {
				v.addf("%s: out references undefined interface %q", field, n.Out)
			}
			if !zones[n.Source] {
				v.addf("%s: source references unknown zone %q", field, n.Source)
			}
		case "port-forward":
			if n.Out != "" || n.Source != "" {
				v.addf("%s: out and source belong to masquerade mode and must be empty in port-forward mode", field)
			}
			if _, ok := ifn[n.In]; !ok {
				v.addf("%s: in references undefined interface %q", field, n.In)
			}
			v.oneOf(field+".proto", n.Proto, validProtos)
			if n.DstPort < 1 || n.DstPort > 65535 {
				v.addf("%s.dst-port: must be between 1 and 65535 (got %d)", field, n.DstPort)
			}
			if err := validatePortForwardTarget(n.To); err != nil {
				v.addf("%s.to: %v", field, err)
			}
		}
	}

	seenRule := map[string]bool{}
	for i := range fw.Rules {
		r := &fw.Rules[i]
		field := fmt.Sprintf("firewall.rules[%d]", i)
		if r.Name == "" {
			v.addf("%s: name must be set", field)
		} else if seenRule[r.Name] {
			v.addf("%s: duplicate name %q", field, r.Name)
		}
		seenRule[r.Name] = true
		for _, side := range []struct{ label, val string }{{"from", r.From}, {"to", r.To}} {
			if side.val == FirewallZone {
				continue
			}
			if side.val == "" {
				v.addf("%s: %s must be set (zone name or %q)", field, side.label, FirewallZone)
			} else if !zones[side.val] {
				v.addf("%s: %s references unknown zone %q", field, side.label, side.val)
			}
		}
		if r.Service != "" {
			if err := ValidateService(r.Service); err != nil {
				v.addf("%s.service: %v", field, err)
			}
		}
		if r.SourceAlias != "" && !aliases[r.SourceAlias] {
			v.addf("%s: source-alias %q is not defined in firewall.aliases", field, r.SourceAlias)
		}
		v.oneOf(field+".verdict", r.Verdict, validVerdicts)
	}
}

// PortForwardTarget is a parsed port-forward destination: an IPv4 address
// and the optional rewritten port.
type PortForwardTarget struct {
	IP   string
	Port int // 0 = keep the public port
}

// ParsePortForwardTarget parses a port-forward destination: an IPv4 address
// or ip:port. IPv6 targets are later work (wave-1 feed/feature decision).
func ParsePortForwardTarget(s string) (PortForwardTarget, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		// No port part: the whole string must be a bare IPv4 address.
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			return PortForwardTarget{}, fmt.Errorf("must be an IPv4 address or ip:port (got %q)", s)
		}
		return PortForwardTarget{IP: ip.String()}, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return PortForwardTarget{}, fmt.Errorf("must be an IPv4 address or ip:port (got %q)", s)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return PortForwardTarget{}, fmt.Errorf("invalid target port %q", portStr)
	}
	return PortForwardTarget{IP: ip.String(), Port: port}, nil
}

// validatePortForwardTarget checks the "to" field of a port-forward rule.
func validatePortForwardTarget(s string) error {
	_, err := ParsePortForwardTarget(s)
	return err
}

// checkAliasEntry validates one alias entry against the alias's family.
func checkAliasEntry(v *validator, field, typ, entry string) {
	ip := net.ParseIP(entry)
	if ip != nil {
		switch typ {
		case "ipv4":
			if ip.To4() == nil {
				v.addf("%s: %q is not an IPv4 address", field, entry)
			}
		case "ipv6":
			if ip.To4() != nil {
				v.addf("%s: %q is not an IPv6 address", field, entry)
			}
		}
		return
	}
	ipn, err := ParseCIDR(entry)
	if err != nil {
		v.addf("%s: invalid %s address or CIDR %q", field, typ, entry)
		return
	}
	switch typ {
	case "ipv4":
		if ipn.IP.To4() == nil {
			v.addf("%s: %q is not an IPv4 CIDR", field, entry)
		}
	case "ipv6":
		if ipn.IP.To4() != nil {
			v.addf("%s: %q is not an IPv6 CIDR", field, entry)
		}
	}
}

// netsOverlap reports whether two subnets share any address space.
func netsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func parseIP(s string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil, fmt.Errorf("invalid IP %q", s)
	}
	return ip, nil
}

// zoneSubnets returns the static subnets of a zone's interfaces.
func zoneSubnets(c *Config, zone string, ifn map[string]*Interface) []*net.IPNet {
	var out []*net.IPNet
	z, ok := c.Zones[zone]
	if !ok {
		return nil
	}
	for _, name := range z.Interfaces {
		ifc := ifn[name]
		if ifc == nil {
			continue
		}
		for _, a := range ifc.Address {
			if ipn, err := ParseCIDR(a); err == nil {
				out = append(out, ipn)
			}
		}
	}
	return out
}

func inSubnets(ip net.IP, subnets []*net.IPNet) bool {
	for _, s := range subnets {
		if s.Contains(ip) {
			return true
		}
	}
	return false
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
