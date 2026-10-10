package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// wipeDir removes everything inside dir, as a lost /config partition
// would present.
func wipeDir(t *testing.T, dir string) {
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

// TestRoundTrip is spec criterion 7: configure, render, export, wipe the
// store, restore — the rendered state is identical to pre-export and the
// git history has a fresh node on top of the imported one.
func TestRoundTrip(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	configApplier := &recordingApplier{}

	// Configure: a second, richer config through the normal pipeline.
	applyUpdate(t, s, configApplier, updatedYAML)
	preRev, err := s.Rev()
	if err != nil {
		t.Fatalf("rev: %v", err)
	}
	rendered, err := config.Marshal(mustParse(t, updatedYAML))
	if err != nil {
		t.Fatalf("marshal rendered: %v", err)
	}
	if !bytes.Equal(configApplier.last(t), rendered) {
		t.Fatal("sanity: applied config is not the updated fixture")
	}

	// Export.
	var archive bytes.Buffer
	if err := Export(s.Dir(), &archive); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Wipe the store — the disaster being recovered from.
	wipeDir(t, s.Dir())

	// Restore through a fresh applier.
	restoreApplier := &recordingApplier{}
	newRev, err := Restore(s, bytes.NewReader(archive.Bytes()), restoreApplier)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Rendered state is identical to pre-export.
	if !bytes.Equal(restoreApplier.last(t), rendered) {
		t.Error("restored config differs from the pre-export rendered state")
	}

	// The store serves the restored config at the new revision.
	cfg, rev, err := s.Load()
	if err != nil {
		t.Fatalf("load after restore: %v", err)
	}
	if rev != newRev {
		t.Errorf("load revision = %q, want restore revision %q", rev, newRev)
	}
	served, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal served: %v", err)
	}
	if !bytes.Equal(served, rendered) {
		t.Error("store config after restore differs from the pre-export rendered state")
	}

	// The git history has the fresh node on top of the imported one.
	log := gitIn(t, s.Dir(), "log", "--format=%H %s")
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 3 {
		t.Fatalf("history after restore = %d commits, want 3 (initial, update, restore):\n%s", len(lines), log)
	}
	if !strings.HasPrefix(lines[0], newRev) {
		t.Errorf("HEAD = %q, want the restore revision %q", lines[0], newRev)
	}
	if !strings.Contains(lines[0], "restore from backup") {
		t.Errorf("HEAD message = %q, want the restore marker", lines[0])
	}
	if !strings.HasPrefix(lines[1], preRev) {
		t.Errorf("imported history lost the pre-export update commit:\n%s", log)
	}
	if !strings.Contains(log, "Initial configuration") {
		t.Error("imported history lost the initial commit")
	}
	if newRev == preRev {
		t.Error("restore did not create a fresh history node")
	}

	// Idempotency: restoring the same archive again converges to the same
	// state with its own fresh marker node. Restores import the backup's
	// history, so markers do not accumulate across restores of the same
	// archive — the live history after every restore is the imported
	// history plus one marker. The sleep separates the two restores into
	// different wall-clock seconds: git commits are deterministic (tree +
	// parent + message + timestamp), so same-second identical markers
	// would legitimately share one hash.
	time.Sleep(1100 * time.Millisecond)
	againRev, err := Restore(s, bytes.NewReader(archive.Bytes()), restoreApplier)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if !bytes.Equal(restoreApplier.last(t), rendered) {
		t.Error("second restore produced a different rendered state")
	}
	if _, rev, err := s.Load(); err != nil || rev != againRev {
		t.Errorf("second restore: load rev = %q (err %v), want %q", rev, err, againRev)
	}
	if againRev == newRev {
		t.Error("second restore reused the first restore's history node")
	}
	log = gitIn(t, s.Dir(), "log", "--format=%H %s")
	lines = strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 3 {
		t.Fatalf("history after second restore = %d commits, want 3 (imported two plus the fresh marker):\n%s", len(lines), log)
	}
	if !strings.HasPrefix(lines[0], againRev) || !strings.Contains(lines[0], "restore from backup") {
		t.Errorf("HEAD after second restore = %q, want the fresh restore marker", lines[0])
	}
}
