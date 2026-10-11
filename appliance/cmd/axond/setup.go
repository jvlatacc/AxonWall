// First-boot setup state and admin-token rotation.
//
// A fresh appliance boots with a provisioning-generated bootstrap token and
// the default gateway config. The web console's setup wizard is shown while
// setup is incomplete: it rotates the admin token, assigns WAN/LAN devices
// through a config PUT (the normal apply pipeline), and then stamps setup
// complete. Setup state lives in a small file on the persistent /config
// partition — it is lifecycle state, not configuration, so it sits beside
// the store rather than inside the config document.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// minTokenLength bounds a rotated admin token. The wizard generates a
// 32-char random token; hand-set tokens below this length are rejected as
// a security floor, not a format opinion.
const minTokenLength = 16

// setupStateFileName is stored in the config store directory (on /config).
const setupStateFileName = "setup-state.json"

// setupState is the persisted wizard marker.
type setupState struct {
	Required    bool   `json:"required"`
	CompletedAt string `json:"completedAt,omitempty"`
}

// handleGetSetup serves GET /setup: whether first-boot setup is still
// pending. A missing marker file means first boot.
func (s *Server) handleGetSetup(w http.ResponseWriter, _ *http.Request) {
	state, err := readSetupState(s.setupStatePath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": state.Required})
}

// handleSetupComplete serves POST /setup/complete: stamps the marker so the
// wizard no longer gates the console. Serialized under s.mu with config
// writes so a stamped file can never race a store swap.
func (s *Server) handleSetupComplete(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := writeSetupState(s.setupStatePath(), setupState{
		Required:    false,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": false})
}

func (s *Server) setupStatePath() string {
	return filepath.Join(s.store.Dir(), setupStateFileName)
}

func readSetupState(path string) (setupState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return setupState{Required: true}, nil
	}
	if err != nil {
		return setupState{}, fmt.Errorf("read setup state: %w", err)
	}
	var state setupState
	if err := json.Unmarshal(data, &state); err != nil {
		return setupState{}, fmt.Errorf("parse setup state: %w", err)
	}
	return state, nil
}

// writeSetupState persists atomically (temp file + rename) so a power cut
// mid-write cannot leave a half-written marker.
func writeSetupState(path string, state setupState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode setup state: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".setup-state-*")
	if err != nil {
		return fmt.Errorf("stage setup state: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write setup state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write setup state: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("secure setup state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("commit setup state: %w", err)
	}
	return nil
}

// rotatedTokenBody is the POST /auth/token request document.
type rotatedTokenBody struct {
	Token string `json:"token"`
}

// handleRotateToken serves POST /auth/token: replaces the admin bearer
// token. The new token must be present in the JSON body and meet the
// length floor. Persistence happens before the in-memory swap: if the
// token file write fails, the daemon answers 500 and the old token stays
// authoritative on both sides.
func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r, 4<<10)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var req rotatedTokenBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be JSON: {\"token\": \"...\"}"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if len(token) < minTokenLength {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("token must be at least %d characters", minTokenLength),
		})
		return
	}
	if strings.ContainsAny(token, " \t\r\n") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token must not contain whitespace"})
		return
	}

	persisted := false
	if path := s.tokenFilePath(); path != "" {
		if err := writeTokenFile(path, token); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": fmt.Sprintf("rotate token: %v", err),
			})
			return
		}
		persisted = true
	}
	s.currentToken.Store(&token)
	writeJSON(w, http.StatusOK, map[string]any{"rotated": true, "persisted": persisted})
}

// writeTokenFile replaces the token file (0600) atomically.
func writeTokenFile(path, token string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("stage token file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("secure token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("commit token file: %w", err)
	}
	return nil
}
