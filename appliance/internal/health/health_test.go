package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useConntrackFixture points the conntrack check at a temp file for the
// duration of the test and restores the real path after.
func useConntrackFixture(t *testing.T, content string, missing bool) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nf_conntrack_count")
	if !missing {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	old := conntrackCountPath
	conntrackCountPath = p
	t.Cleanup(func() { conntrackCountPath = old })
}

func TestSelfCheck_ProbeFailure(t *testing.T) {
	useConntrackFixture(t, "0\n", false)
	c := NewChecker("127.0.0.1:1", time.Second)
	c.Probe = func(context.Context, string, time.Duration) error {
		return errors.New("connection refused")
	}
	err := c.SelfCheck(context.Background())
	if err == nil {
		t.Fatal("expected error when the management listener is unreachable")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should name the unreachable listener, got %v", err)
	}
}

// TestSelfCheck_ProbeSuccess verifies a passing probe plus conntrack
// accounting yields a healthy result (count 0 is legitimate on an idle box).
func TestSelfCheck_ProbeSuccess(t *testing.T) {
	useConntrackFixture(t, "0\n", false)
	c := NewChecker("127.0.0.1:1", time.Second)
	c.Probe = func(context.Context, string, time.Duration) error { return nil }
	if err := c.SelfCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy self-check, got %v", err)
	}
}

// TestSelfCheck_NoAddrSkipsManagement: with no listener address configured
// the management check is uncheckable — not unhealthy. The probe would fail
// the test if it were called.
func TestSelfCheck_NoAddrSkipsManagement(t *testing.T) {
	useConntrackFixture(t, "42\n", false)
	c := NewChecker("", time.Second)
	c.Probe = func(context.Context, string, time.Duration) error {
		t.Error("probe must not be called when no listener address is configured")
		return errors.New("should not be called")
	}
	if err := c.SelfCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy self-check, got %v", err)
	}
}

// TestSelfCheck_ConntrackMissing: unreadable conntrack accounting fails the
// check — a stateful firewall without conntrack is not healthy.
func TestSelfCheck_ConntrackMissing(t *testing.T) {
	useConntrackFixture(t, "", true)
	c := NewChecker("", time.Second)
	err := c.SelfCheck(context.Background())
	if err == nil {
		t.Fatal("expected error when conntrack accounting is unreadable")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("error should report unreadable accounting, got %v", err)
	}
}

// TestSelfCheck_ConntrackMalformed covers a corrupt count value.
func TestSelfCheck_ConntrackMalformed(t *testing.T) {
	useConntrackFixture(t, "not-a-number\n", false)
	c := NewChecker("", time.Second)
	err := c.SelfCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed conntrack accounting")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should report malformed accounting, got %v", err)
	}
}

// TestSelfCheck_ManagementBeforeConntrack: management reachability is
// checked first — the operator-lockout signal outranks kernel state.
func TestSelfCheck_ManagementBeforeConntrack(t *testing.T) {
	useConntrackFixture(t, "", true) // conntrack missing
	c := NewChecker("127.0.0.1:1", time.Second)
	c.Probe = func(context.Context, string, time.Duration) error {
		return errors.New("connection refused")
	}
	err := c.SelfCheck(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("management failure should be reported, got %v", err)
	}
}

// TestNewCheckerDefaults: zero timeout falls back to the 5s default.
func TestNewCheckerDefaults(t *testing.T) {
	c := NewChecker("127.0.0.1:8443", 0)
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}
