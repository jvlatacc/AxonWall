package backup

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

// Limits on archive members, applied at extraction so a hostile or
// corrupt archive cannot balloon memory or disk before it is verified.
const (
	maxManifestBytes = 1 << 16 // 64 KiB
	maxConfigBytes   = 1 << 20 // mirrors the API's config document limit
	maxBundleBytes   = 64 << 20
)

// Applier materializes a validated configuration on the system — axond's
// apply pipeline (render + nft apply + service reloads + health). Restore
// refuses to touch the store until the restored configuration has been
// applied successfully, mirroring the pipeline's apply-before-commit
// order.
type Applier interface {
	Apply(cfg *config.Config) error
}

// Restorer is the store capability restore needs: wholesale replacement
// of the store's contents plus lifecycle commits. *store.Store implements
// it.
type Restorer interface {
	Replace(from string) error
	CommitEvent(msg string) (string, error)
}

// FormatError reports an archive that is not a usable AxonWall backup:
// wrong shape, unexpected members, a manifest this build cannot honor,
// or a git bundle that does not restore.
type FormatError struct{ Reason string }

func (e *FormatError) Error() string { return "backup: " + e.Reason }

// Restore replaces the contents of the store with the archive read from
// r and returns the store's new HEAD revision.
//
// The order mirrors the normal pipeline, with the store swap standing in
// for the commit:
//
//  1. extract the archive into a staging directory (member whitelist,
//     regular files only, bounded member sizes);
//  2. rebuild the store from the git bundle (a full clone) and verify
//     its HEAD against the manifest;
//  3. lay the archived config file over the rebuilt working tree and
//     validate it — the archived file, not the bundle's committed copy,
//     is authoritative, so rendered-but-uncommitted state survives;
//  4. apply the restored configuration through applier — on failure the
//     live store is untouched;
//  5. replace the store's contents and commit a fresh history node
//     marking the restore.
//
// Restore is idempotent: restoring the same archive again converges to
// the same configuration and rendered state (each run adds its own
// marker node on top of the imported history).
func Restore(s Restorer, r io.Reader, applier Applier) (string, error) {
	if s == nil {
		return "", fmt.Errorf("backup: store must not be nil")
	}
	if applier == nil {
		return "", fmt.Errorf("backup: applier must not be nil")
	}

	stage, err := os.MkdirTemp("", "axonwall-restore-*")
	if err != nil {
		return "", fmt.Errorf("backup: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	m, cfgBytes, err := extract(r, stage)
	if err != nil {
		return "", err
	}

	// A bundle containing HEAD restores as a complete store: full history
	// plus the working tree checked out at the recorded revision.
	staged := filepath.Join(stage, "store")
	if _, err := runGit("clone", "--quiet", filepath.Join(stage, nameBundle), staged); err != nil {
		return "", &FormatError{Reason: fmt.Sprintf("git bundle does not restore: %v", err)}
	}
	// Hygiene: the clone's origin points at the deleted bundle file, and
	// nothing in a store ever fetches — the failure is harmless, so this
	// is deliberately best-effort.
	_, _ = runGit("-C", staged, "remote", "remove", "origin")

	imported, err := runGit("-C", staged, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("backup: imported store has no HEAD: %w", err)
	}
	if imported != m.Revision {
		return "", &FormatError{Reason: fmt.Sprintf(
			"bundle HEAD %s does not match manifest revision %s", imported, m.Revision)}
	}

	// The archived config file is authoritative (step 3 above); WriteFile
	// keeps the checkout's mode, so the store's 0600 is re-applied.
	cfgPath := filepath.Join(staged, store.FileName)
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		return "", fmt.Errorf("backup: write restored config: %w", err)
	}
	if err := os.Chmod(cfgPath, 0o600); err != nil {
		return "", fmt.Errorf("backup: restored config mode: %w", err)
	}

	cfg, err := config.Parse(cfgBytes)
	if err != nil {
		// A *config.ValidationError surfaces to the API as 422.
		return "", err
	}

	// Apply before the swap: the store must never claim state the system
	// did not accept.
	if err := applier.Apply(cfg); err != nil {
		return "", fmt.Errorf("backup: apply restored config: %w", err)
	}

	if err := s.Replace(staged); err != nil {
		return "", fmt.Errorf("backup: swap in restored store: %w", err)
	}
	newRev, err := s.CommitEvent(fmt.Sprintf("restore from backup (bundle HEAD %s)", m.Revision))
	if err != nil {
		return "", fmt.Errorf("backup: record restore node: %w", err)
	}
	return newRev, nil
}

// extract reads the archive members into stage and returns the manifest
// and config file bytes. Only the three expected regular-file members are
// accepted; anything else — directories, links, unknown or duplicate
// names, oversized members — is rejected before any store state changes.
func extract(r io.Reader, stage string) (*manifest, []byte, error) {
	tr := tar.NewReader(r)
	var m *manifest
	var cfgBytes []byte
	bundlePath := filepath.Join(stage, nameBundle)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, &FormatError{Reason: fmt.Sprintf("not a readable backup archive: %v", err)}
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, nil, &FormatError{Reason: fmt.Sprintf("member %q: only regular files are accepted", hdr.Name)}
		}
		if seen[hdr.Name] {
			return nil, nil, &FormatError{Reason: fmt.Sprintf("duplicate member %q", hdr.Name)}
		}
		seen[hdr.Name] = true
		switch hdr.Name {
		case nameManifest:
			data, err := readMember(tr, maxManifestBytes, nameManifest)
			if err != nil {
				return nil, nil, err
			}
			m = &manifest{}
			if err := json.Unmarshal(data, m); err != nil {
				return nil, nil, &FormatError{Reason: fmt.Sprintf("manifest is not valid JSON: %v", err)}
			}
			if m.Revision == "" {
				return nil, nil, &FormatError{Reason: "manifest has no revision"}
			}
			if m.Format != Format {
				return nil, nil, &FormatError{Reason: fmt.Sprintf(
					"archive format %d is not supported (this build reads format %d)", m.Format, Format)}
			}
		case nameConfig:
			cfgBytes, err = readMember(tr, maxConfigBytes, nameConfig)
			if err != nil {
				return nil, nil, err
			}
		case nameBundle:
			if err := writeFileLimited(bundlePath, tr, maxBundleBytes); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, &FormatError{Reason: fmt.Sprintf("unexpected member %q", hdr.Name)}
		}
	}
	if m == nil || cfgBytes == nil || !seen[nameBundle] {
		return nil, nil, &FormatError{Reason: fmt.Sprintf(
			"archive is missing required members (%s, %s, %s)", nameManifest, nameConfig, nameBundle)}
	}
	return m, cfgBytes, nil
}

// readMember reads one tar member, bounded by limit.
func readMember(tr *tar.Reader, limit int64, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(tr, limit+1))
	if err != nil {
		return nil, &FormatError{Reason: fmt.Sprintf("member %q is not readable: %v", name, err)}
	}
	if int64(len(data)) > limit {
		return nil, &FormatError{Reason: fmt.Sprintf("member %q exceeds %d bytes", name, limit)}
	}
	return data, nil
}

// writeFileLimited streams a member to path, refusing content beyond
// limit.
func writeFileLimited(path string, r io.Reader, limit int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("backup: write %s: %w", filepath.Base(path), err)
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("backup: write %s: %w", filepath.Base(path), err)
	}
	if n > limit {
		_ = os.Remove(path)
		return &FormatError{Reason: fmt.Sprintf("member %q exceeds %d bytes", filepath.Base(path), limit)}
	}
	return nil
}
