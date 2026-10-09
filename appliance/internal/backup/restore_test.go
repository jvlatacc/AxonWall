package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// buildArchive builds an in-memory tar with the given members.
func buildArchive(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range names {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(members[name]))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(members[name]); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func testManifest(t *testing.T, rev string, format int) []byte {
	t.Helper()
	data, err := json.Marshal(manifest{Format: format, Revision: rev, Created: "2026-10-09T00:00:00Z"})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return data
}

func formatErrReason(t *testing.T, err error) string {
	t.Helper()
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("error is %T (%v), want *backup.FormatError", err, err)
	}
	return fe.Reason
}

func TestRestoreRejectsGarbage(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	preRev, err := s.Rev()
	if err != nil {
		t.Fatalf("rev: %v", err)
	}

	_, err = Restore(s, strings.NewReader("definitely not a tar"), &recordingApplier{})
	formatErrReason(t, err)

	// The store is untouched.
	if postRev, _ := s.Rev(); postRev != preRev {
		t.Errorf("store revision changed on rejected restore: %s -> %s", preRev, postRev)
	}
}

func TestRestoreRejectsUnknownMember(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	archive := buildArchive(t, map[string][]byte{
		"manifest.json": testManifest(t, "deadbeef", Format),
		"evil.sh":       []byte("rm -rf /"),
	})
	_, err := Restore(s, bytes.NewReader(archive), &recordingApplier{})
	if reason := formatErrReason(t, err); !strings.Contains(reason, "evil.sh") {
		t.Errorf("reason = %q, want mention of the unexpected member", reason)
	}
}

func TestRestoreRejectsTraversalMember(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	archive := buildArchive(t, map[string][]byte{
		"manifest.json": testManifest(t, "deadbeef", Format),
		"../escape.txt": []byte("nope"),
	})
	_, err := Restore(s, bytes.NewReader(archive), &recordingApplier{})
	if reason := formatErrReason(t, err); !strings.Contains(reason, "../escape.txt") {
		t.Errorf("reason = %q, want mention of the traversal member", reason)
	}
}

func TestRestoreRejectsUnsupportedFormat(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	archive := buildArchive(t, map[string][]byte{
		"manifest.json": testManifest(t, "deadbeef", 99),
	})
	_, err := Restore(s, bytes.NewReader(archive), &recordingApplier{})
	if reason := formatErrReason(t, err); !strings.Contains(reason, "format 99") {
		t.Errorf("reason = %q, want the unsupported-format explanation", reason)
	}
}

func TestRestoreRejectsBrokenBundle(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	archive := buildArchive(t, map[string][]byte{
		"manifest.json":     testManifest(t, "deadbeef", Format),
		"axonwall.yaml":     []byte(baseYAML),
		"config.git.bundle": []byte("not a git bundle"),
	})
	_, err := Restore(s, bytes.NewReader(archive), &recordingApplier{})
	if reason := formatErrReason(t, err); !strings.Contains(reason, "bundle") {
		t.Errorf("reason = %q, want the bundle failure", reason)
	}
}

func TestRestoreRejectsMissingMembers(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	archive := buildArchive(t, map[string][]byte{
		"manifest.json": testManifest(t, "deadbeef", Format),
	})
	_, err := Restore(s, bytes.NewReader(archive), &recordingApplier{})
	formatErrReason(t, err)
}

func TestRestoreKeepsRenderedButUncommittedState(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))

	// Simulate a crash that left a rendered-but-uncommitted config behind:
	// a valid, different config in the working tree, bypassing the commit
	// path. Export reads the working tree, so the backup carries it.
	driftedBytes, err := config.Marshal(mustParse(t, updatedYAML))
	if err != nil {
		t.Fatalf("marshal drifted: %v", err)
	}
	if err := os.WriteFile(store.Join(s.Dir()), driftedBytes, 0o600); err != nil {
		t.Fatalf("write drifted config: %v", err)
	}

	var archive bytes.Buffer
	if err := Export(s.Dir(), &archive); err != nil {
		t.Fatalf("export: %v", err)
	}

	applier := &recordingApplier{}
	if _, err := Restore(s, bytes.NewReader(archive.Bytes()), applier); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// The working-tree truth — not HEAD's copy — was applied and committed.
	if !bytes.Equal(applier.last(t), driftedBytes) {
		t.Error("restore applied a config other than the exported working tree")
	}
	committed := gitIn(t, s.Dir(), "show", "HEAD:"+store.FileName)
	if committed != strings.TrimSpace(string(driftedBytes)) {
		t.Error("restored config was not committed as the fresh history node's content")
	}
}
