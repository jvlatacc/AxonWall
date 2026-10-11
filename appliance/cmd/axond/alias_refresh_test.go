package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

const refreshBaseYAML = `version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  aliases:
    ads: { type: url-table, url: "%s" }
`

type captureApplier struct {
	attempts int
	frags    [][]byte
	fail     bool
}

func (c *captureApplier) ApplyFragment(ctx context.Context, fragment []byte) error {
	c.attempts++
	if c.fail {
		return context.DeadlineExceeded
	}
	c.frags = append(c.frags, fragment)
	return nil
}

// testFeedServer serves one fixed body over httptest.
func testFeedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func refreshTestStore(t *testing.T, feedURL string) *store.Store {
	t.Helper()
	st, err := store.Init(t.TempDir(), mustConfig(t, fmt.Sprintf(refreshBaseYAML, feedURL)))
	if err != nil {
		t.Fatalf("Init store: %v", err)
	}
	return st
}

func TestRefreshAliases_UpdatesFeedSet(t *testing.T) {
	body := "# provider header\n10.0.0.1\nnot-an-ip\n10.0.0.0/24\n192.168.5.5/32\n\n10.0.0.1\n"
	srv := testFeedServer(t, body)

	st := refreshTestStore(t, srv.URL)
	cap := &captureApplier{}
	refreshAliases(context.Background(), st, cap)

	if len(cap.frags) != 1 {
		t.Fatalf("expected exactly one fragment, got %d", len(cap.frags))
	}
	got := string(cap.frags[0])
	want := "add element inet axonwall alias-ads { 10.0.0.0/24, 10.0.0.1, 192.168.5.5/32 }"
	if !strings.Contains(got, want) {
		t.Fatalf("fragment missing feed entries:\n%s", got)
	}
}

func TestRefreshAliases_FailedFeedKeepsLastGood(t *testing.T) {
	srv := testFeedServer(t, "gone")
	srv.Close() // dead feed: connection refused

	st := refreshTestStore(t, srv.URL)
	cap := &captureApplier{}
	refreshAliases(context.Background(), st, cap)

	if len(cap.frags) != 0 {
		t.Fatalf("failed feed must not produce a fragment, got %d", len(cap.frags))
	}
}

func TestRefreshAliases_ApplyFailureKeepsLastGood(t *testing.T) {
	srv := testFeedServer(t, "10.0.0.1\n")

	st := refreshTestStore(t, srv.URL)
	cap := &captureApplier{fail: true}
	refreshAliases(context.Background(), st, cap)

	// The refresh attempt is made; keep-last-good lives in the kernel
	// transaction itself (atomic fragment) — failure surfaces as a logged
	// error, not a partial set.
	if cap.attempts != 1 || len(cap.frags) != 0 {
		t.Fatalf("refresh should attempt exactly one fragment and record none on failure, got %d attempts / %d frags", cap.attempts, len(cap.frags))
	}
}

func TestUrlTableAliasNames(t *testing.T) {
	st := refreshTestStore(t, "https://feeds.example/ads.txt")
	cfg, _, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if names := urlTableAliasNames(cfg); len(names) != 1 || names[0] != "ads" {
		t.Fatalf("urlTableAliasNames = %v, want [ads]", names)
	}
}

func TestParseFeedEntries(t *testing.T) {
	body := "# header prose\n10.0.0.1,10.0.0.2;not-an-ip\n2001:db8::1\n10.0.0.0/24 # trailing comment\n10.0.0.1\n"
	got := parseFeedEntries(body)
	want := []string{"10.0.0.0/24", "10.0.0.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("parseFeedEntries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseFeedEntries = %v, want %v", got, want)
		}
	}
}
