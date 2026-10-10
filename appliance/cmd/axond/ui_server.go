// Static serving of the built web console: axond mounts the ui/dist bundle
// on the same TLS listener as the API. Same origin means one certificate
// and no CORS; the SPA fallback renders unknown paths client-side. API
// routes are registered on more specific patterns and can never be
// shadowed by this handler.
package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// spaFileServer serves files from dir; any GET that does not name an
// existing file falls back to index.html (client-side routing). Directory
// listings are never produced: a directory hit falls back too.
func spaFileServer(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}

		full, ok := safeJoin(dir, r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid path"})
			return
		}

		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			cacheHeader(w, r.URL.Path)
			http.ServeFile(w, r, full)
			return
		}

		// SPA fallback: the console shell renders the route client-side.
		cacheHeader(w, "/")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

// safeJoin resolves name inside root, refusing any result that escapes it.
// The lexical containment check is defense in depth on top of the Clean —
// and the explicit proof the taint analyzer needs.
func safeJoin(root, name string) (string, bool) {
	full := filepath.Join(root, filepath.Clean("/"+name))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return full, true
}

// cacheHeader matches the Vite output contract: content-hashed asset files
// cache long, everything else (index.html) must be revalidated so a UI
// upgrade is picked up on the next load.
func cacheHeader(w http.ResponseWriter, upath string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(upath, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
}
