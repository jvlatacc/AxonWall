package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
)

// MarkerFirewallActive is the deterministic appliance marker, emitted once
// the ruleset is loaded and services are up. The CI boot test asserts on
// this exact string on the serial console — keep it stable, and keep it in
// sync with the live-boot banner (build/livebuild/config/includes.chroot_after_packages/
// etc/systemd/system/nftables.service.d/axonwall-banner.conf).
const MarkerFirewallActive = "AXONWALL: FIREWALL ACTIVE"

// emitFirewallActiveMarker announces the marker on the appliance console —
// /dev/console follows the last console= kernel parameter, i.e. the serial
// line on headless boots — and in the daemon log. The console write is
// best-effort by contract: development containers have no console device,
// and there the log line still carries the marker.
func emitFirewallActiveMarker() {
	if err := writeConsoleMarker("/dev/console"); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("axond: console marker not written: %v", err)
		}
	}
	log.Printf("%s (ruleset loaded, services up)", MarkerFirewallActive)
}

// writeConsoleMarker writes exactly one marker line to path — the console
// device in production, a regular file under test.
func writeConsoleMarker(path string) error {
	//nolint:gosec // G302: console devices are 0200 by nature; no group/other secrets involved
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open console: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", MarkerFirewallActive); err != nil {
		return fmt.Errorf("write console: %w", err)
	}
	return nil
}
