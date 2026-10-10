package apply

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
)

func testFragment(t *testing.T) []byte {
	t.Helper()
	cfg := config.Default()
	cfg.Firewall.Aliases = map[string]config.Alias{
		"ads": {Type: "url-table", URL: "https://example.com/ads.txt"},
	}
	frag, err := render.SetUpdateFragment(cfg, "ads", []string{"10.0.0.1", "10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	return frag
}

func TestApplyFragment_Applied(t *testing.T) {
	a, readApplied := fakeNft(t)
	if err := a.ApplyFragment(context.Background(), testFragment(t)); err != nil {
		t.Fatalf("ApplyFragment: %v", err)
	}
	if got := readApplied(); !strings.Contains(string(got), "flush set inet axonwall alias-ads") {
		t.Fatalf("fragment not handed to nft -f:\n%s", got)
	}
}

func TestApplyFragment_EmptyRefused(t *testing.T) {
	a, _ := fakeNft(t)
	if err := a.ApplyFragment(context.Background(), nil); err == nil {
		t.Fatal("empty fragment must be refused")
	}
	if err := a.ApplyFragment(context.Background(), []byte("   \n")); err == nil {
		t.Fatal("whitespace-only fragment must be refused")
	}
}

func TestApplyFragment_UnprivilegedNoOp(t *testing.T) {
	a, _ := fakeNft(t)
	t.Setenv("AXW_NFT_LIST_FAIL", "1") // capability probe fails → no-op
	if err := a.ApplyFragment(context.Background(), testFragment(t)); err != nil {
		t.Fatalf("unprivileged ApplyFragment should be a silent no-op, got %v", err)
	}
	// The fake pre-creates its capture files; the invocation log is the
	// authoritative record of whether nft -f ran.
	logged, err := os.ReadFile(os.Getenv("AXW_NFT_LOG")) // #nosec G703 -- path is written by fakeNft via t.TempDir in-process
	if err != nil {
		t.Fatalf("read fake log: %v", err)
	}
	if strings.Contains(string(logged), "-f") {
		t.Fatalf("no-op must not invoke nft -f, log = %s", logged)
	}
}
