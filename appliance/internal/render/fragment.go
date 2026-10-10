package render

import (
	"fmt"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// SetUpdateFragment renders a standalone nftables transaction that replaces
// the element contents of one alias set — the unit the alias feed refresher
// applies when a URL-table feed is fetched. It is one `nft -f` transaction
// scoped to a single set (`flush set` + `add element`), so a feed update
// never re-renders the whole ruleset and can never partially land: the
// kernel installs the new element set completely or not at all.
func SetUpdateFragment(cfg *config.Config, aliasName string, entries []string) ([]byte, error) {
	a, ok := cfg.Firewall.Aliases[aliasName]
	if !ok {
		return nil, fmt.Errorf("render: alias %q not found", aliasName)
	}
	switch a.Type {
	case "ipv4", "url-table", "ipv6":
		// Element updates are type-agnostic; the set's family is fixed by
		// the ruleset rendering. The switch only rejects unknown types.
	default:
		return nil, fmt.Errorf("render: alias %q: unsupported type %q", aliasName, a.Type)
	}
	clean := sortedUnique(entries)
	var b strings.Builder
	fmt.Fprintf(&b, "# AxonWall alias feed update: alias-%s\n", aliasName)
	b.WriteString("table inet axonwall\n")
	fmt.Fprintf(&b, "flush set inet axonwall alias-%s\n", aliasName)
	if len(clean) > 0 {
		fmt.Fprintf(&b, "add element inet axonwall alias-%s { %s }\n", aliasName, strings.Join(clean, ", "))
	}
	return []byte(b.String()), nil
}
