// Status snapshot and network-device endpoints, plus static serving of the
// built web console bundle.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsHostAndConfig(t *testing.T) {
	resp := do(t, testHandler(t), http.MethodGet, "/status", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status: %d, want 200", resp.StatusCode)
	}
	var status systemStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.Hostname == "" {
		t.Fatal("hostname must be populated")
	}
	if status.Version == "" {
		t.Fatal("version must be populated")
	}
	if status.UptimeSeconds < 0 {
		t.Fatalf("uptime: %d", status.UptimeSeconds)
	}

	// Revision matches the config document's revision header.
	cfgResp := do(t, testHandler(t), http.MethodGet, "/config", token, nil)
	if got := cfgResp.Header.Get("X-AxonWall-Revision"); got != status.Revision {
		t.Fatalf("status revision %q != config revision %q", status.Revision, got)
	}

	// The test config (baseYAML) defines lan0 in zone lan.
	foundLan := false
	for _, row := range status.Interfaces {
		if row.Name != "lan0" {
			continue
		}
		foundLan = true
		if row.Zone != "lan" {
			t.Fatalf("lan0 zone: %q, want lan", row.Zone)
		}
		if row.Addressing != "static" {
			t.Fatalf("lan0 addressing: %q, want static", row.Addressing)
		}
		// Static addresses come from the config regardless of runtime.
		if len(row.Addresses) != 1 || row.Addresses[0] != "192.168.1.1/24" {
			t.Fatalf("lan0 addresses: %v, want [192.168.1.1/24]", row.Addresses)
		}
		// eth0 may not exist in every environment; state is honest either way.
		if row.State != "up" && row.State != "down" {
			t.Fatalf("lan0 state: %q", row.State)
		}
	}
	if !foundLan {
		t.Fatalf("status interfaces missing lan0: %+v", status.Interfaces)
	}
	if status.Services == nil || status.States == nil || status.Log == nil {
		t.Fatal("status lists must be non-null (UI renders empty states, not undefined)")
	}
}

func TestStatusNeedsAuth(t *testing.T) {
	if resp := do(t, testHandler(t), http.MethodGet, "/status", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /status without token: %d, want 401", resp.StatusCode)
	}
}

func TestNetDevicesExcludesLoopback(t *testing.T) {
	resp := do(t, testHandler(t), http.MethodGet, "/net/devices", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /net/devices: %d, want 200", resp.StatusCode)
	}
	var devices []struct {
		Name string `json:"name"`
		MAC  string `json:"mac"`
		Up   bool   `json:"up"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, d := range devices {
		if d.Name == "lo" {
			t.Fatal("loopback must be excluded from assignable devices")
		}
		if d.Name == "" {
			t.Fatal("device name must be populated")
		}
	}
}

// writeUIBundle stages a minimal console bundle: index.html plus one
// content-hashed asset — the shape Vite emits.
func writeUIBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	index := `<!doctype html><html><head><title>AxonWall Console</title></head><body><div id="root"></div><script type="module" src="/assets/index-test123.js"></script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index-test123.js"), []byte("console.log('bundle')"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	return dir
}

func TestStaticUIServesBundle(t *testing.T) {
	_, srv := newTestServer(t, nil)
	srv.SetUIDir(writeUIBundle(t))
	h := srv.Handler()

	// Root serves index.html with no-store (a UI upgrade must be picked up).
	resp := do(t, h, http.MethodGet, "/", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("index cache-control: %q, want no-store", cc)
	}

	// Content-hashed assets cache long.
	resp = do(t, h, http.MethodGet, "/assets/index-test123.js", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET asset: %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset cache-control: %q, want immutable", cc)
	}

	// SPA fallback: client-side routes render the shell.
	resp = do(t, h, http.MethodGet, "/dashboard", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /dashboard (fallback): %d, want 200", resp.StatusCode)
	}

	// Static serving is unauthenticated by design — the console shell is
	// public; the API it calls is not.
	resp = do(t, h, http.MethodGet, "/", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / without token (console shell is public): %d, want 200", resp.StatusCode)
	}
}

func TestStaticUIDirectoryListingDenied(t *testing.T) {
	_, srv := newTestServer(t, nil)
	srv.SetUIDir(writeUIBundle(t))

	resp := do(t, srv.Handler(), http.MethodGet, "/assets", token, nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<div id=\"root\">") {
		t.Fatalf("directory request must fall back to index.html, got %d: %q", resp.StatusCode, body)
	}
}

func TestStaticUITraversalRejected(t *testing.T) {
	_, srv := newTestServer(t, nil)
	srv.SetUIDir(writeUIBundle(t))

	resp := do(t, srv.Handler(), http.MethodGet, "/../server.go", token, nil)
	body := readAll(t, resp)
	if strings.Contains(body, "package main") {
		t.Fatal("traversal escaped the UI bundle directory")
	}
}

func TestStaticUINonGetRejected(t *testing.T) {
	_, srv := newTestServer(t, nil)
	srv.SetUIDir(writeUIBundle(t))
	resp := do(t, srv.Handler(), http.MethodPost, "/", token, []byte{})
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: %d, want 405", resp.StatusCode)
	}
}

func TestAPIUnaffectedByUIMount(t *testing.T) {
	_, srv := newTestServer(t, nil)
	srv.SetUIDir(writeUIBundle(t))
	h := srv.Handler()

	// API paths keep their auth requirement and handlers even with the
	// console mounted at "/".
	if resp := do(t, h, http.MethodGet, "/config", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /config without token: %d, want 401", resp.StatusCode)
	}
	if resp := do(t, h, http.MethodGet, "/config", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config: %d, want 200", resp.StatusCode)
	}
	if resp := do(t, h, http.MethodGet, "/healthz", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz: %d, want 200", resp.StatusCode)
	}
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	_, srv := newTestServer(t, nil)
	return srv.Handler()
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
