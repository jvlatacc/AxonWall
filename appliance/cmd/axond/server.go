// Command axond is the AxonWall management daemon: it serves the REST API
// (TLS + token auth) and drives the apply pipeline against the git-backed
// config store. Daemons are render targets; the store is the source of
// truth — nothing edits daemon configs by hand.
package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/jvlatacc/AxonWall/appliance/internal/backup"
	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// Applier materializes a validated candidate configuration on the system
// (render + nft apply + service reloads + health). The apply pipeline owns
// this; until it lands, axond runs with a no-op applier so the transaction
// flow is exercised end to end.
type Applier interface {
	Apply(cfg *config.Config) error
}

// Server is the axond REST API.
type Server struct {
	store   Store
	token   string
	applier Applier

	// mu serializes whole apply-and-commit sequences against each other:
	// config PUTs, backup restores (extract through swap), and exports.
	// Store-level optimistic concurrency stays as the second line of
	// defense for callers that bypass the server.
	mu sync.Mutex
}

// Store narrows *store.Store to what the API needs.
type Store interface {
	Load() (*config.Config, string, error)
	Begin() (*store.Tx, error)
	Rev() (string, error)
	Dir() string
	Replace(from string) error
	CommitEvent(msg string) (string, error)
}

// NewServer builds the API server. token enables bearer auth on the config
// endpoints; applier is consulted between validation and commit.
func NewServer(s Store, token string, applier Applier) *Server {
	if applier == nil {
		applier = noopApplier{}
	}
	return &Server{store: s, token: token, applier: applier}
}

type noopApplier struct{}

func (noopApplier) Apply(*config.Config) error { return nil }

// Handler returns the HTTP routing for axond.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /config", s.auth(s.handleGetConfig))
	mux.HandleFunc("PUT /config", s.auth(s.handlePutConfig))
	mux.HandleFunc("GET /backup/export", s.auth(s.handleBackupExport))
	mux.HandleFunc("POST /backup/restore", s.auth(s.handleBackupRestore))
	return mux
}

// auth rejects requests without the correct bearer token. /healthz stays
// open: it reports nothing but liveness.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := bearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "missing or invalid bearer token",
			})
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, rev, err := s.store.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out, err := config.Marshal(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("X-AxonWall-Revision", rev)
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := readBody(r, maxConfigBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	// Parse strictly first: an invalid document never opens a transaction.
	candidate, err := config.Parse(body)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, configErrorBody(err))
		return
	}

	tx, err := s.store.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Anything below that fails must not leave the candidate committed.
	ok := false
	defer func() {
		if !ok {
			tx.Abort()
		}
	}()

	if err := tx.Set(candidate); err != nil {
		var valErr *config.ValidationError
		if errors.As(err, &valErr) {
			writeJSON(w, http.StatusUnprocessableEntity, configErrorBody(err))
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}

	// The applier is the sole committer: the store records the change only
	// after the candidate has been applied to the system.
	if err := s.applier.Apply(candidate); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": fmt.Sprintf("apply failed, transaction aborted: %v", err),
		})
		return
	}

	msg := strings.TrimSpace(r.Header.Get("X-AxonWall-Message"))
	if msg == "" {
		msg = "config update via API"
	}
	newRev, err := tx.Commit(msg)
	if err != nil {
		var conflict *store.ConflictError
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "config changed concurrently; retry from the current revision",
				"current": conflict.Current,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ok = true

	w.Header().Set("X-AxonWall-Revision", newRev)
	writeJSON(w, http.StatusOK, map[string]any{"revision": newRev})
}

// handleBackupExport streams a backup archive of the store: the config
// file, the store's full git history as a bundle, and a manifest. The
// archive is self-verifying on restore, so a mid-stream failure surfaces
// on the receiving side as a rejected archive rather than silently
// truncating a backup.
func (s *Server) handleBackupExport(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rev, err := s.store.Rev()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("X-AxonWall-Revision", rev)
	w.Header().Set("Content-Disposition", "attachment; filename=axonwall-backup.tar")
	if err := backup.Export(s.store.Dir(), w); err != nil {
		log.Printf("axond: backup export failed: %v", err)
	}
}

// handleBackupRestore imports a backup archive: wipe the store, import
// the backup, re-render and apply through the normal pipeline, and leave
// a fresh git history node (backup.Restore). Serialized against config
// PUTs so an apply can never interleave with the store swap.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBackupBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("read backup: %v", err)})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request body is empty"})
		return
	}
	if int64(len(body)) > maxBackupBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": fmt.Sprintf("backup archive exceeds %d bytes", maxBackupBytes),
		})
		return
	}

	newRev, err := backup.Restore(s.store, bytes.NewReader(body), s.applier)
	if err != nil {
		var formatErr *backup.FormatError
		switch {
		case errors.As(err, &formatErr):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		default:
			// Invalid restored config surfaces as *config.ValidationError
			// and renders the standard 422 body; everything else is 500.
			status := http.StatusInternalServerError
			var valErr *config.ValidationError
			if errors.As(err, &valErr) {
				status = http.StatusUnprocessableEntity
			}
			writeJSON(w, status, configErrorBody(err))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": newRev})
}

// A backup archive is a config file, a manifest, and the store's git
// history — generous for wave 1.
const maxBackupBytes = 64 << 20

const maxConfigBytes = 1 << 20 // 1 MiB is generous for wave-1 configs

func readBody(r *http.Request, limit int64) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}
	return data, nil
}

func configErrorBody(err error) map[string]any {
	var valErr *config.ValidationError
	if errors.As(err, &valErr) {
		return map[string]any{"error": "invalid config", "issues": valErr.Issues}
	}
	return map[string]any{"error": err.Error()}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// lanBindHost derives the API bind host from the config: the first static
// address of the lan zone. It returns "" (all interfaces) when the lan zone
// has no static address yet — e.g. before first-boot setup assigns one.
func lanBindHost(cfg *config.Config) string {
	lan, ok := cfg.Zones["lan"]
	if !ok {
		return ""
	}
	for _, ifName := range lan.Interfaces {
		ifc, ok := cfg.InterfaceByName(ifName)
		if !ok {
			continue
		}
		for _, a := range ifc.Address {
			prefix, err := netip.ParsePrefix(a)
			if err != nil {
				continue
			}
			if prefix.Addr().Is4() {
				return prefix.Addr().String()
			}
		}
	}
	return ""
}

// lanListener binds the API to the LAN interface address per the config.
func lanListener(cfg *config.Config, port string) (net.Listener, error) {
	host := lanBindHost(cfg)
	addr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind API listener on %s: %w", addr, err)
	}
	return ln, nil
}
