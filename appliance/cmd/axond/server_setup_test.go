// First-boot setup state and admin-token rotation: the API surface the web
// console's setup wizard drives.
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSetupTestServer boots a server whose store dir backs the setup-state
// file, optionally persisting token rotations to a token file.
func newSetupTestServer(t *testing.T, withTokenFile bool) (*Server, string) {
	t.Helper()
	s, srv := newTestServer(t, nil)
	if withTokenFile {
		tokenFile := filepath.Join(t.TempDir(), "api-token")
		if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
			t.Fatalf("write token file: %v", err)
		}
		srv.SetTokenFile(tokenFile)
	}
	return srv, s.Dir()
}

func TestSetupRequiredByDefault(t *testing.T) {
	srv, _ := newSetupTestServer(t, false)
	resp := do(t, srv.Handler(), http.MethodGet, "/setup", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /setup: %d, want 200", resp.StatusCode)
	}
	var body struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Required {
		t.Fatal("fresh store must report setup required")
	}
}

func TestSetupCompletePersists(t *testing.T) {
	srv, dir := newSetupTestServer(t, false)
	resp := do(t, srv.Handler(), http.MethodPost, "/setup/complete", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /setup/complete: %d, want 200", resp.StatusCode)
	}

	// The marker is on disk: re-reading it is what a rebooted daemon does.
	resp = do(t, srv.Handler(), http.MethodGet, "/setup", token, nil)
	var body struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Required {
		t.Fatal("setup must report complete after stamping")
	}
	state, err := readSetupState(filepath.Join(dir, setupStateFileName))
	if err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if state.Required || state.CompletedAt == "" {
		t.Fatalf("marker not stamped correctly: %+v", state)
	}
}

func TestSetupEndpointsNeedAuth(t *testing.T) {
	srv, _ := newSetupTestServer(t, false)
	if resp := do(t, srv.Handler(), http.MethodGet, "/setup", "wrong-token", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /setup with bad token: %d, want 401", resp.StatusCode)
	}
	if resp := do(t, srv.Handler(), http.MethodPost, "/setup/complete", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /setup/complete without token: %d, want 401", resp.StatusCode)
	}
}

func TestRotateTokenRequiresCurrentAuth(t *testing.T) {
	srv, _ := newSetupTestServer(t, false)
	resp := do(t, srv.Handler(), http.MethodPost, "/auth/token", "wrong-token", []byte(`{"token":"new-token-value-0123456789"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotation with bad token: %d, want 401", resp.StatusCode)
	}
}

func TestRotateTokenInMemoryWithoutTokenFile(t *testing.T) {
	srv, _ := newSetupTestServer(t, false)
	newToken := "rotated-token-0123456789abcdef"
	resp := do(t, srv.Handler(), http.MethodPost, "/auth/token", token, []byte(`{"token":"`+newToken+`"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotation: %d, want 200", resp.StatusCode)
	}
	var body struct {
		Rotated   bool `json:"rotated"`
		Persisted bool `json:"persisted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Rotated || body.Persisted {
		t.Fatalf("expected rotated without persistence (no token file), got %+v", body)
	}

	// Old token is dead immediately; new token authenticates.
	if resp := do(t, srv.Handler(), http.MethodGet, "/config", token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after rotation: %d, want 401", resp.StatusCode)
	}
	if resp := do(t, srv.Handler(), http.MethodGet, "/config", newToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("new token after rotation: %d, want 200", resp.StatusCode)
	}
}

func TestRotateTokenPersistsToTokenFile(t *testing.T) {
	srv, _ := newSetupTestServer(t, true)
	newToken := "rotated-token-0123456789abcdef"
	resp := do(t, srv.Handler(), http.MethodPost, "/auth/token", token, []byte(`{"token":"`+newToken+`"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotation: %d, want 200", resp.StatusCode)
	}
	var body struct {
		Persisted bool `json:"persisted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Persisted {
		t.Fatal("rotation must persist when a token file is configured")
	}
	data, err := os.ReadFile(srv.tokenFilePath())
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if strings.TrimSpace(string(data)) != newToken {
		t.Fatalf("token file contains %q, want %q", string(data), newToken)
	}
	if info, err := os.Stat(srv.tokenFilePath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode: %v (%v)", info.Mode(), err)
	}
}

func TestRotateTokenRejectsWeakTokens(t *testing.T) {
	srv, _ := newSetupTestServer(t, false)
	cases := map[string]string{
		"too short":       `{"token":"short"}`,
		"with whitespace": `{"token":"has a space in the middle of the token"}`,
		"empty":           `{"token":""}`,
		"not json":        `token`,
		"missing token":   `{}`,
	}
	for name, body := range cases {
		resp := do(t, srv.Handler(), http.MethodPost, "/auth/token", token, []byte(body))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, resp.StatusCode)
		}
	}
}
