package render

import (
	"fmt"
	"io"
)

// writeManagedHeader emits the comment block every rendered artifact opens
// with: what it is, which store revision produced it, and the rule that hand
// edits are out of model. The revision line matches the nftables ruleset's
// header ("uncommitted candidate" for an empty revision) so operators see
// the same provenance marker across artifacts.
func writeManagedHeader(w io.Writer, what string, rev string) {
	fmt.Fprintf(w, "# %s — rendered from the declarative config store.\n", what)
	if rev == "" {
		fmt.Fprintf(w, "# Source revision: uncommitted candidate\n")
	} else {
		fmt.Fprintf(w, "# Source revision: %s\n", rev)
	}
	fmt.Fprintf(w, "# DO NOT EDIT — regenerated on every apply; hand edits are out of model.\n")
}
