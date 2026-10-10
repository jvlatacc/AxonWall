package render

import (
	"fmt"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// The WireGuard renderer owns the wg0 interface configuration: the listen
// port and peers from services.wireguard become a wg-quick-shaped config
// file the service reloader applies with `wg syncconf` (verified against
// wg(8): syncconf reads the INI-style file, comments included, and applies
// it as the running configuration). The config store holds public keys only;
// the appliance's private key is joined at sync time under /run.

// renderWireGuard renders the wg0 syncconf config, or nil when
// services.wireguard is absent. wg0's own address is not part of the wave-1
// schema (see docs/service-renderers.md, honest gaps).
func renderWireGuard(cfg *config.Config, rev string) ([]byte, error) {
	wg := cfg.Services.WireGuard
	if wg == nil {
		return nil, nil
	}

	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed WireGuard configuration (wg0)", rev)
	b.WriteString("\n")
	b.WriteString("# Applied with `wg syncconf wg0` by the service reloader; the private\n")
	b.WriteString("# key is joined from /etc/wireguard/wg0.key into a /run copy at sync\n")
	b.WriteString("# time — never rendered here (the store holds public keys only).\n")
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "ListenPort = %d\n", wg.ListenPort)

	for _, peer := range wg.Peers {
		// A malformed key is a render error, never a broken config file:
		// syncconf would reject it after the apply had already started.
		// ParseWGKey decodes for validation; the rendered file carries the
		// operator's base64 form, which is what wg accepts on input.
		if _, err := config.ParseWGKey(peer.PublicKey); err != nil {
			return nil, fmt.Errorf("render: wireguard peer %q: %w", peer.Name, err)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "# peer: %s\n", peer.Name)
		b.WriteString("[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", strings.TrimSpace(peer.PublicKey))
		fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(peer.AllowedIPs, ", "))
	}
	return []byte(b.String()), nil
}
