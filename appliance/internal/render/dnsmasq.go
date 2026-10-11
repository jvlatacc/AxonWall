package render

import (
	"fmt"
	"net"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// The dnsmasq renderer owns DHCP: pools from services.dhcp become tagged
// dhcp-range / dhcp-option directives in a complete /etc/dnsmasq.conf. DNS
// is disabled outright (port=0) — the appliance's resolver is unbound, and
// two daemons on :53 is a race, not a feature.

// renderDnsmasq renders the complete dnsmasq.conf, or nil when services.dhcp
// is absent (no pools → no DHCP service → nothing for dnsmasq to own).
func renderDnsmasq(cfg *config.Config, rev string) ([]byte, error) {
	dhcp := cfg.Services.DHCP
	if dhcp == nil || len(dhcp.Pools) == 0 {
		return nil, nil
	}

	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed dnsmasq configuration", rev)
	b.WriteString("\n")
	b.WriteString("# DHCP only. DNS is unbound's job (services.dns) — port=0 disables\n")
	b.WriteString("# dnsmasq's DNS listener entirely: one DNS listener per appliance.\n")
	b.WriteString("port=0\n")
	b.WriteString("# Follow interfaces as systemd-networkd brings them up or down.\n")
	b.WriteString("bind-dynamic\n")
	b.WriteString("# Authoritative on the appliance's own segments: answer from the local\n")
	b.WriteString("# pool immediately instead of waiting out an answering timeout.\n")
	b.WriteString("dhcp-authoritative\n")

	// DHCP is served only on the interfaces of zones that carry pools.
	ifaces := map[string]bool{}
	for _, pool := range dhcp.Pools {
		for _, in := range zoneInterfaces(cfg, pool.Zone) {
			k, err := kernelNameOf(cfg, in)
			if err != nil {
				return nil, err
			}
			ifaces[k] = true
		}
	}
	if len(ifaces) > 0 {
		b.WriteString("\n# Serve DHCP only on the interfaces of zones that have pools.\n")
		for _, k := range sortedKeys(ifaces) {
			fmt.Fprintf(&b, "interface=%s\n", k)
		}
	}

	// Ranges in config order (the order the operator wrote them); the
	// per-zone router/DNS options are deduplicated across pools of the same
	// zone — clients of a tagged pool receive one router and one DNS server.
	b.WriteString("\n# Pools: tagged ranges so per-zone options reach the right clients.\n")
	type zoneOptions struct{ router, dns string }
	opts := map[string]zoneOptions{}
	for _, pool := range dhcp.Pools {
		start, err := parseIP(pool.Range[0])
		if err != nil {
			return nil, fmt.Errorf("render: dhcp pool %q range start: %w", pool.Zone, err)
		}
		end, err := parseIP(pool.Range[1])
		if err != nil {
			return nil, fmt.Errorf("render: dhcp pool %q range end: %w", pool.Zone, err)
		}
		// The netmask is derivable only from a static subnet of the zone;
		// dnsmasq infers it from the interface otherwise (dnsmasq(8):
		// the netmask is optional for directly connected networks).
		if mask := subnetMaskFor(cfg, start); mask != "" {
			fmt.Fprintf(&b, "dhcp-range=set:%s,%s,%s,%s\n", pool.Zone, start, end, mask)
		} else {
			fmt.Fprintf(&b, "dhcp-range=set:%s,%s,%s\n", pool.Zone, start, end)
		}
		if prev, seen := opts[pool.Zone]; seen {
			if prev.router != pool.Gateway {
				return nil, fmt.Errorf("render: pools in zone %q declare conflicting gateways (%q vs %q)", pool.Zone, prev.router, pool.Gateway)
			}
			if prev.dns != pool.DNS {
				return nil, fmt.Errorf("render: pools in zone %q declare conflicting DNS servers (%q vs %q)", pool.Zone, prev.dns, pool.DNS)
			}
		}
		opts[pool.Zone] = zoneOptions{router: pool.Gateway, dns: pool.DNS}
	}

	b.WriteString("\n# Per-zone DHCP options: gateway and DNS server handed to clients.\n")
	for _, zone := range sortedZoneNames(cfg) {
		o, ok := opts[zone]
		if !ok {
			continue
		}
		if o.router != "" {
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:router,%s\n", zone, o.router)
		}
		if o.dns != "" {
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:dns-server,%s\n", zone, o.dns)
		}
	}
	return []byte(b.String()), nil
}

// zoneInterfaces returns a zone's logical interface names in config order.
func zoneInterfaces(cfg *config.Config, zone string) []string {
	z, ok := cfg.Zones[zone]
	if !ok {
		return nil
	}
	return z.Interfaces
}

// subnetMaskFor returns the dotted-quad netmask of the zone subnet that
// contains addr, or "" when no static subnet covers it (DHCP-addressed
// zones have no render-time subnet).
func subnetMaskFor(cfg *config.Config, addr net.IP) string {
	for _, zone := range sortedZoneNames(cfg) {
		for _, subnet := range zoneStaticSubnets(cfg, zone) {
			if subnet.Contains(addr) {
				return net.IP(subnet.Mask).String()
			}
		}
	}
	return ""
}
