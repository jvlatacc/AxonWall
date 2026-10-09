package render

import (
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

func fragmentFixture() *config.Config {
	cfg := config.Default()
	cfg.Firewall.Aliases = map[string]config.Alias{
		"ads":     {Type: "url-table", URL: "https://example.com/ads.txt"},
		"v6hosts": {Type: "ipv6", Entries: []string{"2001:db8::1"}},
		"hosts":   {Type: "ipv4", Entries: []string{"192.168.1.10"}},
	}
	return cfg
}

func TestSetUpdateFragment_URLTable(t *testing.T) {
	frag, err := SetUpdateFragment(fragmentFixture(), "ads", []string{"10.0.0.1", "10.0.0.0/24", "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(frag)
	for _, want := range []string{
		"table inet axonwall\n",
		"flush set inet axonwall alias-ads\n",
		"add element inet axonwall alias-ads { 10.0.0.0/24, 10.0.0.1 }",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("fragment missing %q:\n%s", want, s)
		}
	}
}

func TestSetUpdateFragment_EmptyFlushesOnly(t *testing.T) {
	frag, err := SetUpdateFragment(fragmentFixture(), "ads", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(frag)
	if strings.Contains(s, "add element") {
		t.Fatalf("empty feed must not carry an add element line:\n%s", s)
	}
	if !strings.Contains(s, "flush set inet axonwall alias-ads\n") {
		t.Fatalf("flush line missing:\n%s", s)
	}
}

func TestSetUpdateFragment_IPv6(t *testing.T) {
	frag, err := SetUpdateFragment(fragmentFixture(), "v6hosts", []string{"2001:db8::2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frag), "add element inet axonwall alias-v6hosts { 2001:db8::2 }") {
		t.Fatalf("ipv6 fragment wrong:\n%s", frag)
	}
}

func TestSetUpdateFragment_UnknownAlias(t *testing.T) {
	if _, err := SetUpdateFragment(fragmentFixture(), "ghost", nil); err == nil {
		t.Fatal("unknown alias should error")
	}
}

func TestSetUpdateFragment_Deterministic(t *testing.T) {
	entries := []string{"10.0.0.1", "10.0.0.0/24", "10.0.0.1"}
	a, err := SetUpdateFragment(fragmentFixture(), "ads", entries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SetUpdateFragment(fragmentFixture(), "ads", entries)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("identical inputs must produce byte-identical fragments")
	}
}
