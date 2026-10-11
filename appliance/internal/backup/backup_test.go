package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/store"
)

const baseYAML = `
version: 1
zones:
  lan: { interfaces: [lan0] }
interfaces:
  - { name: lan0, match: eth0, addressing: static, address: [192.168.1.1/24] }
firewall:
  default: { input: drop, forward: drop, output: accept }
`

const updatedYAML = `
version: 1
zones:
  wan: { interfaces: [wan0] }
  lan: { interfaces: [lan0] }
  wg0: { interfaces: [wg0] }
interfaces:
  - { name: wan0, match: ens3, addressing: dhcp }
  - { name: lan0, match: ens4, addressing: static, address: [192.168.1.1/24] }
services:
  wireguard:
    listen-port: 51820
    peers:
      - { name: laptop, public-key: "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=", allowed-ips: [10.10.0.2/32] }
firewall:
  default: { input: drop, forward: drop, output: accept }
  rules:
    - { name: wg-handshake, from: wan, to: firewall, service: udp/51820, verdict: accept }
`

// mustParse parses a fixture document, failing the test on invalid YAML.
func mustParse(t *testing.T, doc string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return cfg
}

// newTestStore initializes a store in a temp dir with cfg.
func newTestStore(t *testing.T, cfg *config.Config) *store.Store {
	t.Helper()
	s, err := store.Init(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("init store: %v", err)
	}
	return s
}

// applyUpdate runs a config change through the normal pipeline:
// transaction, applier, commit.
func applyUpdate(t *testing.T, s *store.Store, applier Applier, doc string) {
	t.Helper()
	cfg := mustParse(t, doc)
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Set(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := applier.Apply(cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := tx.Commit("test update"); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// recordingApplier records the marshaled form of every Apply call.
type recordingApplier struct {
	mu      sync.Mutex
	applied [][]byte
}

func (a *recordingApplier) Apply(cfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, err := config.Marshal(cfg)
	if err != nil {
		return err
	}
	a.applied = append(a.applied, out)
	return nil
}

// last returns the most recently applied configuration's marshaled form.
func (a *recordingApplier) last(t *testing.T) []byte {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.applied) == 0 {
		t.Fatal("applier was never called")
	}
	return a.applied[len(a.applied)-1]
}

// gitIn runs a git command against dir; tests assert on history through it.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // test helper over a temp dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out))
}

// readArchive reads a tar archive into an ordered name list and a member
// map.
func readArchive(t *testing.T, data []byte) ([]string, map[string][]byte) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(data))
	var names []string
	members := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read member %s: %v", hdr.Name, err)
		}
		names = append(names, hdr.Name)
		members[hdr.Name] = body
	}
	return names, members
}

func TestExportArchiveShape(t *testing.T) {
	s := newTestStore(t, mustParse(t, baseYAML))
	applier := &recordingApplier{}
	applyUpdate(t, s, applier, updatedYAML)

	var archive bytes.Buffer
	if err := Export(s.Dir(), &archive); err != nil {
		t.Fatalf("export: %v", err)
	}

	names, members := readArchive(t, archive.Bytes())
	wantNames := []string{"manifest.json", "axonwall.yaml", "config.git.bundle"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("archive members = %v, want %v", names, wantNames)
	}

	var m manifest
	if err := json.Unmarshal(members["manifest.json"], &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	wantRev := gitIn(t, s.Dir(), "rev-parse", "HEAD")
	if m.Format != Format {
		t.Errorf("manifest format = %d, want %d", m.Format, Format)
	}
	if m.Revision != wantRev {
		t.Errorf("manifest revision = %q, want store HEAD %q", m.Revision, wantRev)
	}

	cfg, err := config.Parse(members["axonwall.yaml"])
	if err != nil {
		t.Fatalf("archived config does not parse: %v", err)
	}
	live, _, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	liveBytes, err := config.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live: %v", err)
	}
	archivedBytes, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal archived: %v", err)
	}
	if !bytes.Equal(liveBytes, archivedBytes) {
		t.Error("archived config differs from the store's config")
	}
}

func TestExportEmptyStoreFails(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := Export(dir, &buf); err == nil {
		t.Fatal("export of a store with no history succeeded")
	}
}
