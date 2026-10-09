// API-level integration test: the web console's contract against a running
// axond fixture with the real built UI bundle mounted. Mirrors exactly what
// the wired HttpAxonWallClient does — bearer login, revision-guarded config
// writes, status reads, console assets over TLS — so a backend change that
// breaks the UI's live path fails here before it ships.
//
// Skips when ui/dist has not been built; CI (the ui job) builds the bundle
// before running this test. Override the bundle location with
// AXONWALL_UI_DIST.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// uiDistDir finds the built console bundle, or skips the test.
func uiDistDir(t *testing.T) string {
	t.Helper()
	if custom := os.Getenv("AXONWALL_UI_DIST"); custom != "" {
		if _, err := os.Stat(filepath.Join(custom, "index.html")); err != nil { // #nosec G703 - operator-provided bundle dir in a test harness; Stat only
			t.Fatalf("AXONWALL_UI_DIST=%s has no index.html: %v", custom, err)
		}
		return custom
	}
	dist := filepath.Join("..", "..", "..", "ui", "dist")
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Skipf("ui/dist not built (run npm run build in ui/): %v", err)
	}
	return dist
}

// axondFixture is a running axond API+console over TLS, backed by a real
// git-backed store in a temp dir.
type axondFixture struct {
	ts     *httptest.Server
	client *http.Client // trusts the fixture's self-signed cert
}

func newAxondFixture(t *testing.T) *axondFixture {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Init(dir, mustConfig(t, baseYAML))
	if err != nil {
		t.Fatalf("init store: %v", err)
	}
	srv := NewServer(st, token, nil)
	srv.SetUIDir(uiDistDir(t))

	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &axondFixture{ts: ts, client: ts.Client()}
}

// call performs one API request exactly as the console's fetch layer does.
func (f *axondFixture) call(t *testing.T, method, path, bearer string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// The TLS fixture proves the HTTPS transport the console uses on the LAN.
func TestUIIntegrationServesOverTLS(t *testing.T) {
	f := newAxondFixture(t)
	if !strings.HasPrefix(f.ts.URL, "https://") {
		t.Fatalf("fixture URL %q is not HTTPS", f.ts.URL)
	}
}

func TestUIIntegrationLoginFlow(t *testing.T) {
	f := newAxondFixture(t)

	// No token / wrong token: the console shows the login page.
	for name, bearer := range map[string]string{"no token": "", "wrong token": "nope"} {
		resp := f.call(t, http.MethodGet, "/config", bearer, nil, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: GET /config: %d, want 401", name, resp.StatusCode)
		}
	}

	// Correct token: the console loads the config and revision.
	resp := f.call(t, http.MethodGet, "/config", token, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config: %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-AxonWall-Revision") == "" {
		t.Fatal("config response must carry the revision header")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if _, err := config.Parse(body); err != nil {
		t.Fatalf("served config must parse: %v", err)
	}
}

func TestUIIntegrationRuleChangeThroughConfigStore(t *testing.T) {
	f := newAxondFixture(t)

	// Read the current config and revision (the console's load path).
	resp := f.call(t, http.MethodGet, "/config", token, nil, nil)
	original, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	revision := resp.Header.Get("X-AxonWall-Revision")

	// A rule change made in the UI is a config edit, not a daemon poke.
	// The edit adds the wan zone and interface the rule needs — the same
	// shape a real wizard/first-boot edit takes.
	candidate := mustConfig(t, string(original))
	candidate.Zones["wan"] = config.Zone{Interfaces: []string{"wan0"}}
	candidate.Interfaces = append(candidate.Interfaces, config.Interface{
		Name:       "wan0",
		Match:      "eth1",
		Addressing: "dhcp",
	})
	candidate.Firewall.Rules = append(candidate.Firewall.Rules, config.Rule{
		Name:    "ui-added-rule",
		From:    "lan",
		To:      "wan",
		Service: "tcp/443",
		Verdict: "accept",
	})
	next, err := config.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Write with the revision the client read (the console's save path).
	put := f.call(t, http.MethodPut, "/config", token, map[string]string{
		"Content-Type":        "application/yaml",
		"X-AxonWall-Revision": revision,
		"X-AxonWall-Message":  "ui: add lan-to-wan 443",
	}, next)
	if put.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(put.Body)
		t.Fatalf("PUT /config: %d, body %s", put.StatusCode, body)
	}
	var putBody struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(put.Body).Decode(&putBody); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if putBody.Revision == "" || putBody.Revision == revision {
		t.Fatalf("revision must advance: %q -> %q", revision, putBody.Revision)
	}

	// The stored config now carries the rule.
	resp = f.call(t, http.MethodGet, "/config", token, nil, nil)
	stored, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(stored), "ui-added-rule") {
		t.Fatal("rule change did not land in the config store")
	}

	// A stale editor cannot clobber the new revision.
	stalePut := f.call(t, http.MethodPut, "/config", token, map[string]string{
		"Content-Type":        "application/yaml",
		"X-AxonWall-Revision": revision,
	}, original)
	if stalePut.StatusCode != http.StatusConflict {
		t.Fatalf("stale PUT: %d, want 409", stalePut.StatusCode)
	}
}

func TestUIIntegrationStatusAndConsole(t *testing.T) {
	f := newAxondFixture(t)

	// Status snapshot the Status page renders.
	resp := f.call(t, http.MethodGet, "/status", token, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status: %d, want 200", resp.StatusCode)
	}
	var status systemStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Hostname == "" {
		t.Fatal("status.hostname must be populated")
	}

	// The console shell is served from the same origin.
	shell := f.call(t, http.MethodGet, "/", "", nil, nil)
	if shell.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %d, want 200", shell.StatusCode)
	}
	html, err := io.ReadAll(shell.Body)
	if err != nil {
		t.Fatalf("read shell: %v", err)
	}
	if !strings.Contains(string(html), "<div id=\"root\">") {
		t.Fatalf("console shell does not look like the built bundle: %q", string(html[:min(120, len(html))]))
	}

	// SPA fallback for client-side routes.
	fallback := f.call(t, http.MethodGet, "/firewall", "", nil, nil)
	if fallback.StatusCode != http.StatusOK {
		t.Fatalf("GET /firewall (SPA fallback): %d, want 200", fallback.StatusCode)
	}
}

func TestUIIntegrationTokenRotationEndToEnd(t *testing.T) {
	f := newAxondFixture(t)
	rotated := "rotated-in-integration-0123456789"

	body, err := json.Marshal(map[string]string{"token": rotated})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := f.call(t, http.MethodPost, "/auth/token", token, map[string]string{"Content-Type": "application/json"}, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /auth/token: %d, want 200", resp.StatusCode)
	}

	// The console's session store swaps the token; the old one is dead.
	if r := f.call(t, http.MethodGet, "/config", token, nil, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token: %d, want 401", r.StatusCode)
	}
	if r := f.call(t, http.MethodGet, "/config", rotated, nil, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("new token: %d, want 200", r.StatusCode)
	}
}
