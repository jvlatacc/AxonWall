package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// Alias feed refresh: url-table aliases pull their entries from the feed
// URL and update the kernel set directly through a scoped nftables
// fragment — never a full ruleset apply. Failure behavior is
// keep-last-good: a failed fetch or parse leaves the running set untouched
// and is logged. A refresh concurrent with a config apply cannot partially
// land (both are atomic transactions); the worst case is the apply's seed
// entries overwriting the refreshed set, corrected at the next tick.

// aliasRefreshInterval matches OPNsense's URL-table refresh convention.
const aliasRefreshInterval = time.Hour

// fragmentApplier is the part of the nft applier the refresher needs.
type fragmentApplier interface {
	ApplyFragment(ctx context.Context, fragment []byte) error
}

// startAliasRefresher runs the refresh loop until ctx is done: an
// immediate refresh (sets are current from boot, not an hour later), then
// one pass per interval.
func startAliasRefresher(ctx context.Context, st *store.Store, applier fragmentApplier) {
	go func() {
		ticker := time.NewTicker(aliasRefreshInterval)
		defer ticker.Stop()
		for {
			refreshAliases(ctx, st, applier)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// refreshAliases performs one refresh pass over the store's url-table
// aliases. Per-alias failures are logged and skipped — one dead feed must
// not block the others.
func refreshAliases(ctx context.Context, st *store.Store, applier fragmentApplier) {
	cfg, _, err := st.Load()
	if err != nil {
		log.Printf("axond: alias refresh: load config: %v", err)
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, name := range urlTableAliasNames(cfg) {
		a := cfg.Firewall.Aliases[name]
		entries, err := fetchFeedEntries(client, a.URL)
		if err != nil {
			log.Printf("axond: alias refresh: %s: %v (keeping last-good set)", name, err)
			continue
		}
		fragment, err := render.SetUpdateFragment(cfg, name, entries)
		if err != nil {
			log.Printf("axond: alias refresh: %s: %v (keeping last-good set)", name, err)
			continue
		}
		if err := applier.ApplyFragment(ctx, fragment); err != nil {
			log.Printf("axond: alias refresh: %s: apply failed: %v (keeping last-good set)", name, err)
			continue
		}
		log.Printf("axond: alias refresh: %s: %d entries", name, len(entries))
	}
}

// urlTableAliasNames returns the sorted names of the config's url-table
// aliases, so refresh passes are deterministic.
func urlTableAliasNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Firewall.Aliases))
	for name, a := range cfg.Firewall.Aliases {
		if a.Type == "url-table" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// maxFeedSize caps a feed body at 1 MiB — public blocklists are kilobytes;
// anything larger is a hostile or misconfigured feed.
const maxFeedSize = 1 << 20

// fetchFeedEntries retrieves and parses one feed. Non-200 responses are
// errors (a 404 must not flush the set to empty).
func fetchFeedEntries(client *http.Client, feedURL string) ([]string, error) {
	if feedURL == "" {
		return nil, fmt.Errorf("url-table alias has no feed URL")
	}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize))
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	return parseFeedEntries(string(body)), nil
}

// parseFeedEntries extracts IPv4 addresses and CIDR prefixes from a feed
// body. Comment text (from # to end of line) and unparseable tokens are
// skipped — public feeds routinely carry prose headers. Output is
// deduplicated and sorted so identical feeds produce identical fragments.
func parseFeedEntries(body string) []string {
	seen := map[string]bool{}
	var entries []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, tok := range strings.FieldsFunc(line, isTokenSeparator) {
			if seen[tok] || !validFeedEntry(tok) {
				continue
			}
			seen[tok] = true
			entries = append(entries, tok)
		}
	}
	sort.Strings(entries)
	return entries
}

// isTokenSeparator covers whitespace and the separators blocklists use
// (commas, semicolons).
func isTokenSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '\r', ',', ';':
		return true
	}
	return false
}

// validFeedEntry accepts IPv4 addresses and IPv4 CIDR prefixes (url-table
// sets are ipv4_addr). IPv6 is skipped by design — the set type is IPv4.
func validFeedEntry(tok string) bool {
	if ip := net.ParseIP(tok); ip != nil {
		return ip.To4() != nil
	}
	ip, _, err := net.ParseCIDR(tok)
	if err != nil {
		return false
	}
	return ip.To4() != nil
}
