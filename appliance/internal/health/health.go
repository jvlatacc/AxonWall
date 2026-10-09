// Package health implements the post-apply self-check: the independent
// verification that an applied ruleset left the appliance operable. It
// checks the two things a locking ruleset breaks first — the management
// API answering on its own listener, and the kernel still tracking
// connection state — and is what the confirm-or-rollback loop consults
// before a change is allowed to stand.
package health

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// conntrackCountPath is the kernel's connection-tracking accounting sysctl;
// its existence implies conntrack is loaded (stateful inspection active)
// and its value is the live state count. A variable so tests can point it
// at a fixture file.
var conntrackCountPath = "/proc/sys/net/netfilter/nf_conntrack_count"

// Checker runs the self-check against a running axond.
type Checker struct {
	// APIAddr is the management listener's host:port, dialed to prove the
	// appliance is still reachable for management after the apply.
	APIAddr string
	// Timeout caps each individual check.
	Timeout time.Duration
	// Probe replaces the default TCP-dial reachability probe. axond sets a
	// TLS-handshake probe: a bare connect to the TLS listener aborts the
	// handshake (log noise server-side) and proves less.
	Probe func(ctx context.Context, addr string, timeout time.Duration) error
}

// NewChecker returns a checker for the given management listener address.
// Timeout 0 means 5 seconds.
func NewChecker(apiAddr string, timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Checker{APIAddr: apiAddr, Timeout: timeout}
}

// SelfCheck verifies management reachability and conntrack liveness. Any
// failure means the candidate ruleset may have locked the operator out or
// broken stateful filtering — the caller must restore the known-good
// ruleset.
func (c *Checker) SelfCheck(ctx context.Context) error {
	if err := c.checkManagement(ctx); err != nil {
		return fmt.Errorf("health: %w", err)
	}
	if err := checkConntrack(); err != nil {
		return fmt.Errorf("health: %w", err)
	}
	return nil
}

// checkManagement probes the API listener through the freshly applied
// ruleset — the strongest signal the operator can still reach the box.
func (c *Checker) checkManagement(ctx context.Context) error {
	if c.APIAddr == "" {
		// No listener address configured (tests, embedders): management
		// reachability is uncheckable — not unhealthy.
		return nil
	}
	if c.Probe != nil {
		if err := c.Probe(ctx, c.APIAddr, c.Timeout); err != nil {
			return fmt.Errorf("management listener %s unreachable: %w", c.APIAddr, err)
		}
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(dialCtx, "tcp", c.APIAddr)
	if err != nil {
		return fmt.Errorf("management listener %s unreachable: %w", c.APIAddr, err)
	}
	_ = conn.Close()
	return nil
}

// checkConntrack verifies the kernel's connection tracking is present and
// accounting: a stateful firewall whose conntrack accounting vanished is
// not healthy. The count itself may legitimately be zero on an idle box.
func checkConntrack() error {
	data, err := os.ReadFile(conntrackCountPath)
	if err != nil {
		return fmt.Errorf("conntrack accounting unreadable (%s): %w", conntrackCountPath, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return fmt.Errorf("conntrack accounting malformed: %q", strings.TrimSpace(string(data)))
	}
	return nil
}
