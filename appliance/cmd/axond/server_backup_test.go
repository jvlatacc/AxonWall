package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

func TestBackupExportRequiresAuth(t *testing.T) {
	_, srv := newTestServer(t, nil)
	h := srv.Handler()

	for _, tc := range []struct {
		name   string
		bearer string
	}{
		{"no token", ""},
		{"wrong token", "nope"},
	} {
		resp := do(t, h, http.MethodGet, "/backup/export", tc.bearer, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET /backup/export (%s) = %d, want 401", tc.name, resp.StatusCode)
		}
	}
}

func TestBackupExportStreamsArchive(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp := do(t, srv.Handler(), http.MethodGet, "/backup/export", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /backup/export = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-tar" {
		t.Errorf("content type = %q, want application/x-tar", ct)
	}
	if resp.Header.Get("X-AxonWall-Revision") == "" {
		t.Error("missing X-AxonWall-Revision header")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(body))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("body is not a tar archive: %v", err)
	}
	if hdr.Name != "manifest.json" {
		t.Errorf("first member = %q, want manifest.json", hdr.Name)
	}
}

func TestBackupRestoreRequiresAuth(t *testing.T) {
	_, srv := newTestServer(t, nil)
	h := srv.Handler()

	for _, tc := range []struct {
		name   string
		bearer string
	}{
		{"no token", ""},
		{"wrong token", "nope"},
	} {
		resp := do(t, h, http.MethodPost, "/backup/restore", tc.bearer, []byte("junk"))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST /backup/restore (%s) = %d, want 401", tc.name, resp.StatusCode)
		}
	}
}

func TestBackupRestoreRejectsGarbage(t *testing.T) {
	s, srv := newTestServer(t, nil)

	resp := do(t, srv.Handler(), http.MethodPost, "/backup/restore", token, []byte("not an archive at all"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /backup/restore = %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if out.Error == "" {
		t.Error("empty error message")
	}

	// The store is untouched: a rejected restore must never damage it.
	if _, _, err := s.Load(); err != nil {
		t.Fatalf("store load after rejected restore: %v", err)
	}
}

func TestBackupRestoreRejectsEmptyBody(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp := do(t, srv.Handler(), http.MethodPost, "/backup/restore", token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /backup/restore (empty) = %d, want 400", resp.StatusCode)
	}
}

// wipeStoreDir removes everything inside the store directory, as a lost
// /config partition would present.
func wipeStoreDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("wipe %s: %v", e.Name(), err)
		}
	}
}

// TestBackupRestoreRoundTripThroughAPI runs spec criterion 7 at the API
// boundary: configure via PUT, export via GET, wipe the store, restore
// via POST — the served configuration is identical to pre-export and the
// store history has a fresh marker node.
func TestBackupRestoreRoundTripThroughAPI(t *testing.T) {
	var applied [][]byte
	recorder := applierFunc(func(cfg *config.Config) error {
		out, err := config.Marshal(cfg)
		if err != nil {
			return err
		}
		applied = append(applied, out)
		return nil
	})
	s, srv := newTestServer(t, recorder)
	h := srv.Handler()

	// Configure through the API.
	resp := do(t, h, http.MethodPut, "/config", token, []byte(updatedYAML))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /config = %d, want 200", resp.StatusCode)
	}
	preRev := resp.Header.Get("X-AxonWall-Revision")
	if preRev == "" {
		t.Fatal("PUT /config returned no revision header")
	}

	// The served configuration, pre-export.
	resp = do(t, h, http.MethodGet, "/config", token, nil)
	preConfig, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /config: %v", err)
	}

	// Export through the API.
	resp = do(t, h, http.MethodGet, "/backup/export", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /backup/export = %d, want 200", resp.StatusCode)
	}
	archive, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}

	// Wipe the store — the disaster being recovered from.
	wipeStoreDir(t, s.Dir())

	// Restore through the API.
	resp = do(t, h, http.MethodPost, "/backup/restore", token, archive)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /backup/restore = %d, want 200 (%s)", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if out.Revision == "" || out.Revision == preRev {
		t.Errorf("restore revision = %q, want a fresh node distinct from %q", out.Revision, preRev)
	}

	// The served configuration is identical to pre-export.
	resp = do(t, h, http.MethodGet, "/config", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config after restore = %d, want 200", resp.StatusCode)
	}
	postConfig, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /config after restore: %v", err)
	}
	if !bytes.Equal(preConfig, postConfig) {
		t.Error("configuration after restore differs from the pre-export configuration")
	}

	// The restored config is what the applier received last.
	lastApplied := applied[len(applied)-1]
	want, err := config.Marshal(mustConfig(t, updatedYAML))
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if !bytes.Equal(lastApplied, want) {
		t.Errorf("applier received %q, want the restored config", lastApplied)
	}
}
