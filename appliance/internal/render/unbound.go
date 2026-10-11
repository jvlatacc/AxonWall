package render

import (
	"fmt"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// The unbound renderer owns DNS resolution: services.dns becomes a complete
// /etc/unbound/unbound.conf — a recursive, DNSSEC-validating resolver whose
// listeners track the zones in services.dns.listen. See docs/service-renderers.md
// for the listen/ACL decisions and why apply restarts rather than reloads.

// renderUnbound renders the complete unbound.conf, or nil when services.dns
// is absent.
func renderUnbound(cfg *config.Config, rev string) ([]byte, error) {
	dns := cfg.Services.DNS
	if dns == nil {
		return nil, nil
	}
	// Validation enforces this; the renderer re-checks so a programmatic
	// caller gets a render error instead of a config for the wrong daemon.
	if dns.Resolver != "unbound" {
		return nil, fmt.Errorf("render: services.dns.resolver must be \"unbound\" (got %q)", dns.Resolver)
	}

	var b strings.Builder
	writeManagedHeader(&b, "AxonWall managed unbound configuration", rev)
	b.WriteString("\n")
	b.WriteString("server:\n")
	b.WriteString("    # Recursive, DNSSEC-validating resolver (the OPNsense-parity\n")
	b.WriteString("    # default). The trust anchor bootstraps DNSSEC validation.\n")
	b.WriteString("    auto-trust-anchor-file: \"/var/lib/unbound/root.key\"\n")
	b.WriteString("    # Listen on the interfaces of the zones in services.dns.listen —\n")
	b.WriteString("    # unbound binds each named interface's addresses.\n")

	// Listen entries in config order (zone order, then the zone's interface
	// order), deduplicated: a zone listed twice must not yield two listeners.
	interfaces := map[string]bool{}
	for _, zone := range dns.Listen {
		for _, in := range zoneInterfaces(cfg, zone) {
			k, err := kernelNameOf(cfg, in)
			if err != nil {
				return nil, err
			}
			if !interfaces[k] {
				interfaces[k] = true
				fmt.Fprintf(&b, "    interface: %s\n", k)
			}
		}
	}

	b.WriteString("    # Allow the static subnets of the listened zones. Clients that\n")
	b.WriteString("    # match no access-control line are refused (unbound's default) —\n")
	b.WriteString("    # the firewall's input chain is the outer gate, this is the inner.\n")
	allowed := map[string]bool{}
	for _, zone := range dns.Listen {
		for _, subnet := range zoneStaticSubnets(cfg, zone) {
			s := subnet.String()
			if !allowed[s] {
				allowed[s] = true
				fmt.Fprintf(&b, "    access-control: %s allow\n", s)
			}
		}
	}

	// Forward mode: every query goes to the configured upstream resolvers
	// instead of iterating from the roots. Recursive mode is the default
	// and renders no forward-zone.
	if dns.Mode == "forward" {
		b.WriteString("\n# Forward all queries to the configured upstream resolvers.\n")
		b.WriteString("forward-zone:\n")
		b.WriteString("    name: \".\"\n")
		for _, f := range dns.Forwarders {
			fmt.Fprintf(&b, "    forward-addr: %s\n", strings.TrimSpace(f))
		}
	}
	return []byte(b.String()), nil
}
