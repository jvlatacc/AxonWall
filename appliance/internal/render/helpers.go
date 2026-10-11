package render

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// parseIP parses a bare IP (pool bound, DNS forwarders). Trimmed: operators
// hand-write these values.
func parseIP(s string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil, fmt.Errorf("invalid IP %q", s)
	}
	return ip, nil
}

// sortedKeys orders a string set deterministically.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// kernelNameOf maps a logical interface to the kernel device it binds to
// (wg0 maps to itself; undefined interfaces are a render error).
func kernelNameOf(cfg *config.Config, logical string) (string, error) {
	if logical == config.WireGuardInterfaceName {
		// wg0 is materialized by the wireguard service; it has no
		// interfaces entry by definition.
		return logical, nil
	}
	ifc, ok := cfg.InterfaceByName(logical)
	if !ok {
		return "", fmt.Errorf("render: reference to undefined interface %q", logical)
	}
	return ifc.Match, nil
}

// zoneStaticSubnets returns the static subnets of a zone's interfaces in
// config order. DHCP-addressed interfaces contribute nothing — their subnet
// is unknowable at render time.
func zoneStaticSubnets(cfg *config.Config, zone string) []*net.IPNet {
	var out []*net.IPNet
	z, ok := cfg.Zones[zone]
	if !ok {
		return out
	}
	for _, in := range z.Interfaces {
		ifc, ok := cfg.InterfaceByName(in)
		if !ok || ifc.Addressing != "static" {
			continue
		}
		for _, a := range ifc.Address {
			if _, ipnet, err := net.ParseCIDR(a); err == nil {
				out = append(out, ipnet)
			}
		}
	}
	return out
}
