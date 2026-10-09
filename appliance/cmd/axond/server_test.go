package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

const baseYAML = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [192.168.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const updatedYAML = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
  wg0: { interfaces: [wg0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.1.1/24] }
services:
  wireguard:
    listen-port: 51820
    peers:
      - { name: laptop, public-key: "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=", allowed-ips: [10.10.0.2/32] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  rules:
    - { name: wg-handshake, from: wan, to: firewall, service: udp/51820, verdict: accept }
`

const token = "test-token-1234"

func newTestServer(t *testing.T, applier Applier) (*store.Store, *Server) {
	t.Helper()
	s, err := store.Init(t.TempDir(), mustConfig(t, baseYAML))
	if err != nil {
		t.Fatalf("Init store: %v", err)
	}
	return s, NewServer(s, token, applier)
}

func mustConfig(t *testing.T, doc string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return cfg
}

// do issues an HTTP request against the API with optional bearer auth.
func do(t *testing.T, h http.Handler, method, path, bearer string, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHealthzIsOpen(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp := do(t, srv.Handler(), http.MethodGet, "/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", resp.StatusCode)
	}
}

// The acceptance test: unauthenticated and wrongly authenticated requests
// are rejected on the config endpoints.
func TestConfigEndpointRejectsUnauthenticated(t *testing.T) {
	_, srv := newTestServer(t, nil)
	h := srv.Handler()

	for _, tc := range []struct {
		name   string
		bearer string
	}{
		{"no token", ""},
		{"wrong token", "nope"},
	} {
		resp := do(t, h, http.MethodGet, "/config", tc.bearer, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET /config (%s) = %d, want 401", tc.name, resp.StatusCode)
		}
		resp = do(t, h, http.MethodPut, "/config", tc.bearer, []byte(baseYAML))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("PUT /config (%s) = %d, want 401", tc.name, resp.StatusCode)
		}
	}
}

func TestGetConfigReturnsYAMLAndRevision(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp := do(t, srv.Handler(), http.MethodGet, "/config", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/yaml") {
		t.Fatalf("Content-Type = %q, want application/yaml", got)
	}
	if resp.Header.Get("X-AxonWall-Revision") == "" {
		t.Fatal("GET /config did not return a revision header")
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(buf.String(), "lan0") {
		t.Fatalf("GET /config body missing lan0:\n%s", buf.String())
	}
}

func TestPutConfigCommitsAfterApply(t *testing.T) {
	applied := false
	applier := applierFunc(func(cfg *config.Config) error {
		applied = true
		if _, ok := cfg.Zones["wg0"]; !ok {
			t.Errorf("applier received config without the wg0 zone")
		}
		return nil
	})
	s, srv := newTestServer(t, applier)
	h := srv.Handler()

	before, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}

	resp := do(t, h, http.MethodPut, "/config", token, []byte(updatedYAML))
	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		t.Fatalf("PUT /config = %d, want 200: %s", resp.StatusCode, buf.String())
	}
	if !applied {
		t.Fatal("applier was never invoked")
	}

	after, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev after put: %v", err)
	}
	if after == before {
		t.Fatal("PUT /config did not advance the store revision")
	}
	if resp.Header.Get("X-AxonWall-Revision") != after {
		t.Fatalf("response revision %q != store revision %q",
			resp.Header.Get("X-AxonWall-Revision"), after)
	}

	// The committed store must now serve the updated config.
	cfg, _, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Zones["wg0"]; !ok {
		t.Fatal("store did not retain the committed wg0 zone")
	}
}

func TestPutConfigInvalidIsRejectedWithoutCommit(t *testing.T) {
	s, srv := newTestServer(t, nil)
	before, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}

	bad := strings.Replace(baseYAML, "input: drop", "input: reject", 1)
	resp := do(t, srv.Handler(), http.MethodPut, "/config", token, []byte(bad))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PUT invalid config = %d, want 422", resp.StatusCode)
	}
	after, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}
	if after != before {
		t.Fatal("rejected config advanced the store revision")
	}
}

type failingApplier struct{ called bool }

func (f *failingApplier) Apply(*config.Config) error {
	f.called = true
	return errors.New("nft check failed")
}

func TestPutConfigApplyFailureAborts(t *testing.T) {
	s, srv := newTestServer(t, &failingApplier{})
	before, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}

	resp := do(t, srv.Handler(), http.MethodPut, "/config", token, []byte(updatedYAML))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("PUT with failing applier = %d, want 500", resp.StatusCode)
	}
	after, err := s.Rev()
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}
	if after != before {
		t.Fatal("apply failure committed the config — commit must follow apply")
	}
}

func TestPutConflictReturns409(t *testing.T) {
	s, srv := newTestServer(t, nil)
	h := srv.Handler()

	// First PUT establishes a head; then a direct transaction opened
	// against that head is invalidated by a competing commit.
	resp := do(t, h, http.MethodPut, "/config", token, []byte(updatedYAML))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first PUT = %d, want 200", resp.StatusCode)
	}

	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Set(mustConfig(t, updatedYAML)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	st, err := store.Open(s.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tx2, err := st.Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}
	if err := tx2.Set(mustConfig(t, baseYAML)); err != nil {
		t.Fatalf("Set tx2: %v", err)
	}
	if _, err := tx2.Commit("competing change"); err != nil {
		t.Fatalf("Commit tx2: %v", err)
	}

	_, err = tx.Commit("stale change")
	var conflict *store.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("stale commit = %v, want *store.ConflictError", err)
	}
}

func TestLanBindHost(t *testing.T) {
	cfg := mustConfig(t, baseYAML)
	if got := lanBindHost(cfg); got != "192.168.1.1" {
		t.Fatalf("lanBindHost = %q, want 192.168.1.1", got)
	}

	// No lan zone: bind to all interfaces.
	noLan := mustConfig(t, baseYAML)
	delete(noLan.Zones, "lan")
	if got := lanBindHost(noLan); got != "" {
		t.Fatalf("lanBindHost without lan = %q, want empty", got)
	}

	// LAN on DHCP: no static address, bind to all interfaces.
	dhcpYAML := `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: dhcp }
firewall:
  default: { input: drop, forward: drop, output: accept }
`
	if got := lanBindHost(mustConfig(t, dhcpYAML)); got != "" {
		t.Fatalf("lanBindHost with dhcp lan = %q, want empty", got)
	}
}

func TestTLSServingSmoke(t *testing.T) {
	_, srv := newTestServer(t, nil)
	cert, err := selfSignedCert()
	if err != nil {
		t.Fatalf("selfSignedCert: %v", err)
	}
	tlsSrv := httptest.NewUnstartedServer(srv.Handler())
	tlsSrv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	client := tlsSrv.Client()
	req, err := http.NewRequest(http.MethodGet, tlsSrv.URL+"/config", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET over TLS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config over TLS = %d, want 200", resp.StatusCode)
	}
}

// applierFunc adapts a function to the Applier interface.
type applierFunc func(*config.Config) error

func (f applierFunc) Apply(cfg *config.Config) error { return f(cfg) }
