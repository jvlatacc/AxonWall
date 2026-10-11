package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestMarkerFirewallActiveContract pins the marker string: the CI boot test
// asserts on this exact line on the serial console, far away from this file.
// A drive-by edit here breaks CI without any local signal.
func TestMarkerFirewallActiveContract(t *testing.T) {
	if got := MarkerFirewallActive; got != "AXONWALL: FIREWALL ACTIVE" {
		t.Fatalf("marker contract changed: %q", got)
	}
}

func TestWriteConsoleMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console")
	// writeConsoleMarker targets an existing device (O_WRONLY, no O_CREATE) —
	// stage an empty file the way a console device exists.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("stage console file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close staged console file: %v", err)
	}
	if err := writeConsoleMarker(path); err != nil {
		t.Fatalf("writeConsoleMarker: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker file: %v", err)
	}
	if got := string(data); got != MarkerFirewallActive+"\n" {
		t.Fatalf("marker content = %q, want %q", got, MarkerFirewallActive+"\n")
	}
}

// A missing console device is the development-container case: the error must
// classify as fs.ErrNotExist so emitFirewallActiveMarker stays quiet instead
// of logging noise on every dev start.
func TestWriteConsoleMarkerMissingDevice(t *testing.T) {
	err := writeConsoleMarker(filepath.Join(t.TempDir(), "no-such-dir", "console"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}
