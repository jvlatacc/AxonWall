package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// systemd-networkd owns the appliance's interfaces: the networkd renderer
// turns each config-store interface into a .network unit (addressing) and —
// for physical devices — a .link unit that claims the device for AxonWall
// before any later unit can. Units are named 10-axonwall-<logical>.* so the
// stale-file collector in the service reloader can distinguish ours from
// every other unit in /etc/systemd/network.

// NetworkdUnitPrefix is the filename prefix of every unit the renderer owns;
// the service reloader's stale-file collector uses it to tell AxonWall's
// units apart from every other unit in /etc/systemd/network.
const NetworkdUnitPrefix = "10-axonwall-"

// renderNetworkd emits one .network unit per configured interface plus .link
// units for physical devices, keyed by unit file name. wg0 gets a .network
// unit when the wireguard service exists (per-interface IPv4 forwarding —
// routed traffic through the tunnel needs forwarding on both in and out
// interfaces) but never a .link unit: the interface is created by name at
// runtime, not claimed from udev.
func renderNetworkd(cfg *config.Config, rev string) (map[string][]byte, error) {
	units := map[string][]byte{}
	for _, iface := range sortedInterfaces(cfg) {
		network, err := renderNetworkUnit(cfg, iface, rev)
		if err != nil {
			return nil, err
		}
		units[NetworkdUnitPrefix+iface.Name+".network"] = network
		// A .link unit binds a physical device (matched by its kernel
		// name) to the logical interface. WireGuard interfaces are created
		// by name at runtime — no device for udev/.link to claim.
		if iface.Name != config.WireGuardInterfaceName {
			units[NetworkdUnitPrefix+iface.Name+".link"] = renderLinkUnit(iface, rev)
		}
	}
	if cfg.Services.WireGuard != nil {
		units[NetworkdUnitPrefix+config.WireGuardInterfaceName+".network"] = renderWireguardNetworkUnit(rev)
	}
	return units, nil
}

// renderWireguardNetworkUnit emits the wg0 .network unit: the interface is
// created by the wireguard service; the unit exists so networkd enables
// per-interface forwarding on it (and is the future home of wg0 addressing
// when the schema carries it).
func renderWireguardNetworkUnit(rev string) []byte {
	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed systemd-networkd unit", rev)
	fmt.Fprintf(&b, "\n# %s is created by the wireguard service (services.wireguard); this unit\n", config.WireGuardInterfaceName)
	b.WriteString("# only enables per-interface forwarding on the tunnel. The private key\n# lives in /etc/wireguard/wg0.key — never here.\n")
	b.WriteString("[Match]\n")
	fmt.Fprintf(&b, "Name=%s\n\n", config.WireGuardInterfaceName)
	b.WriteString("[Network]\n")
	b.WriteString("IPForward=ipv4\n")
	return []byte(b.String())
}

// sortedInterfaces orders interfaces by logical name: map iteration must
// never leak into unit emission order.
func sortedInterfaces(cfg *config.Config) []*config.Interface {
	names := make([]string, 0, len(cfg.Interfaces))
	byName := map[string]*config.Interface{}
	for i := range cfg.Interfaces {
		ifc := &cfg.Interfaces[i]
		names = append(names, ifc.Name)
		byName[ifc.Name] = ifc
	}
	sort.Strings(names)
	out := make([]*config.Interface, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

// renderNetworkUnit renders the .network unit: Match pins the kernel device,
// [Network] carries addressing (DHCP or static addresses) and enables IPv4
// packet forwarding — the appliance routes between zones by definition.
func renderNetworkUnit(cfg *config.Config, iface *config.Interface, rev string) ([]byte, error) {
	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed systemd-networkd unit", rev)
	b.WriteString("[Match]\n")
	fmt.Fprintf(&b, "Name=%s\n\n", iface.Match)
	b.WriteString("[Network]\n")
	switch iface.Addressing {
	case "dhcp":
		b.WriteString("DHCP=yes\n")
	case "static":
		if len(iface.Address) == 0 {
			return nil, fmt.Errorf("render: static interface %q has no addresses", iface.Name)
		}
		for _, addr := range iface.Address {
			fmt.Fprintf(&b, "Address=%s\n", addr)
		}
	default:
		return nil, fmt.Errorf("render: interface %q has unsupported addressing %q", iface.Name, iface.Addressing)
	}
	// Forwarding is per-interface in systemd-networkd; every interface the
	// appliance manages is a potential routed path between zones.
	b.WriteString("IPForward=ipv4\n")
	return []byte(b.String()), nil
}

// renderLinkUnit claims the physical device for AxonWall. A .link file that
// matched but carried no directives would still suppress 99-default.link —
// the first matching .link file wins and later files are ignored — so the
// unit replicates Debian's default naming policy exactly: it wins the match
// without changing naming behavior.
func renderLinkUnit(iface *config.Interface, rev string) []byte {
	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed .link unit", rev)
	fmt.Fprintf(&b, "# Claims the device bound to logical interface %s (match %q) so no\n", iface.Name, iface.Match)
	b.WriteString("# later .link unit can rename it out from under the rendered .network\n")
	b.WriteString("# file. Directives replicate Debian's default naming policy; AxonWall\n")
	b.WriteString("# adds no renaming of its own.\n")
	b.WriteString("[Match]\n")
	fmt.Fprintf(&b, "OriginalName=%s\n\n", iface.Match)
	b.WriteString("[Link]\n")
	b.WriteString("NamePolicy=kernel database onboard slot path\n")
	b.WriteString("AlternativeNamesPolicy=database onboard slot path\n")
	b.WriteString("MACAddressPolicy=persistent\n")
	return []byte(b.String())
}
