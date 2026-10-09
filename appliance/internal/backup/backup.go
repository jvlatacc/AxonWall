// Package backup implements export and restore of the git-backed
// configuration store.
//
// An export is a tar archive with three members: a format manifest, the
// store's configuration file, and a git bundle of the store's complete
// history. Restore rebuilds a store from that archive: the imported
// configuration is validated and applied through the normal pipeline
// (behind the Applier seam) before the store's contents are replaced and
// a fresh history node marks the restore. The archive is self-verifying —
// restore refuses archives whose members, manifest, and git bundle do
// not agree.
package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// Format is the backup archive format version this build writes and reads.
const Format = 1

// Archive member names.
const (
	nameManifest = "manifest.json"
	nameConfig   = store.FileName
	nameBundle   = "config.git.bundle"
)

// manifest is the archive's self-description, verified by restore.
type manifest struct {
	Format   int    `json:"format"`
	Revision string `json:"revision"`
	Created  string `json:"created"`
}

// Export writes a backup archive of the git-backed store at dir to w.
//
// The archive contains three members in fixed order: the manifest
// (format version and the store's HEAD revision), the configuration file
// exactly as it sits in the store directory, and a git bundle of the
// store's complete history. The config file is read from the working
// tree rather than from HEAD: the store keeps the two identical after
// every commit, and if a crash ever left rendered-but-uncommitted state
// behind, the working tree is the truth worth backing up.
//
// Member order and metadata are fixed; the manifest's created timestamp
// is the only wall-clock input.
func Export(dir string, w io.Writer) error {
	rev, err := headRev(dir)
	if err != nil {
		return fmt.Errorf("backup: store has no history to export: %w", err)
	}
	cfgBytes, err := os.ReadFile(store.Join(dir))
	if err != nil {
		return fmt.Errorf("backup: read config: %w", err)
	}

	// The bundle is built to a temp file so the tar member size is known
	// before streaming starts.
	bundleFile, err := os.CreateTemp("", "axonwall-backup-*.bundle")
	if err != nil {
		return fmt.Errorf("backup: temp bundle: %w", err)
	}
	bundlePath := bundleFile.Name()
	_ = bundleFile.Close() // named the file; git opens the path itself
	defer func() { _ = os.Remove(bundlePath) }()
	if _, err := runGit("-C", dir, "bundle", "create", bundlePath, "--all", "HEAD"); err != nil {
		return fmt.Errorf("backup: create git bundle: %w", err)
	}
	bundle, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("backup: open bundle: %w", err)
	}
	defer func() { _ = bundle.Close() }()
	bundleInfo, err := bundle.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat bundle: %w", err)
	}

	manifestBytes, err := json.Marshal(manifest{
		Format:   Format,
		Revision: rev,
		Created:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("backup: marshal manifest: %w", err)
	}

	tw := tar.NewWriter(w)
	defer func() { _ = tw.Close() }()
	if err := writeMember(tw, nameManifest, manifestBytes); err != nil {
		return err
	}
	if err := writeMember(tw, nameConfig, cfgBytes); err != nil {
		return err
	}
	if err := writeMemberFrom(tw, nameBundle, bundle, bundleInfo.Size()); err != nil {
		return err
	}
	return nil
}

// headRev returns the store's HEAD revision, failing for a directory with
// no git history.
func headRev(dir string) (string, error) {
	return runGit("-C", dir, "rev-parse", "HEAD")
}

// writeMember writes an in-memory tar member with fixed metadata.
func writeMember(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: tar header %s: %w", name, err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("backup: tar body %s: %w", name, err)
	}
	return nil
}

// writeMemberFrom streams an open file into a tar member of known size.
func writeMemberFrom(tw *tar.Writer, name string, r io.Reader, size int64) error {
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: size}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: tar header %s: %w", name, err)
	}
	if _, err := io.Copy(tw, r); err != nil {
		return fmt.Errorf("backup: tar body %s: %w", name, err)
	}
	return nil
}

// runGit runs a git command and returns trimmed stdout.
func runGit(args ...string) (string, error) {
	// args are internal git verbs over operator-supplied store paths,
	// mirroring internal/store/git.go.
	cmd := exec.Command("git", args...) //nolint:gosec // internal wrapper, operator-supplied store dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}
