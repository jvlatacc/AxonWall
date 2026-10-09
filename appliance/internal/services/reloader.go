// Package services re-materializes rendered service artifacts on the
// appliance: it is the only code that installs rendered daemon configs and
// restarts daemons, and the apply pipeline calls it between the atomic
// nftables apply and the health check. A failed sync restores the
// known-good ruleset like any other apply failure; files already written
// stay on disk and are overwritten by the next successful apply — the
// store remains the source of truth (docs/service-renderers.md).
package services

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
	"github.com/jvlatacc/AxonWall/appliance/internal/render"
)

// Runner executes a command and returns its combined output. The seam keeps
// reload orchestration testable (fake runners assert the exact command
// sequence) and lets integration tests shim only the systemd-dependent
// commands via PATH.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner is the production runner: run the command, fail with its
// output attached (a bare exit-code error is undiagnosable in a journal).
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // command names are fixed at each service call site
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Reloader syncs rendered artifacts to the filesystem and re-materializes
// the services. Root is the filesystem root ("/" in production, a tempdir
// in tests); Run defaults to ExecRunner.
type Reloader struct {
	Root string
	Run  Runner
	// ReadKey reads a private key file; defaults to os.ReadFile. The seam
	// lets tests fail the key source without a real filesystem error.
	ReadKey func(name string) ([]byte, error)
}

// NewReloader returns the production reloader.
func NewReloader() *Reloader {
	return &Reloader{Root: "/", Run: ExecRunner}
}

func (r *Reloader) keyReader() func(string) ([]byte, error) {
	if r.ReadKey != nil {
		return r.ReadKey
	}
	return os.ReadFile
}

// Sync installs every rendered artifact and reloads or restarts the owning
// service. Sections absent from the rendered set have their previously
// rendered files removed — the running state follows the store in both
// directions.
func (r *Reloader) Sync(ctx context.Context, rendered *render.Rendered) error {
	run := r.runner()
	if err := r.syncNetworkd(ctx, run, rendered.Networkd); err != nil {
		return err
	}
	if err := r.syncFileService(ctx, run, path(r.Root, "etc/dnsmasq.conf"), "dnsmasq", rendered.Dnsmasq); err != nil {
		return err
	}
	if err := r.syncFileService(ctx, run, path(r.Root, "etc/unbound/unbound.conf"), "unbound", rendered.Unbound); err != nil {
		return err
	}
	return r.syncWireGuard(ctx, run, rendered)
}

func (r *Reloader) runner() Runner {
	if r.Run == nil {
		return ExecRunner
	}
	return r.Run
}

// syncNetworkd installs the rendered units (stale AxonWall units removed),
// then reloads networkd when anything changed — networkctl reload applies
// .network/.link changes without restarting the daemon.
func (r *Reloader) syncNetworkd(ctx context.Context, run Runner, units map[string][]byte) error {
	dir := path(r.Root, "etc/systemd/network")
	existing, err := managedFiles(dir, render.NetworkdUnitPrefix, []string{".network", ".link"})
	if err != nil {
		return err
	}
	changed := false
	for name := range existing {
		if _, keep := units[name]; !keep {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("services: remove stale unit %s: %w", name, err)
			}
			changed = true
		}
	}
	for name, content := range units {
		written, err := writeFileIfChanged(filepath.Join(dir, name), content, 0o644)
		if err != nil {
			return err
		}
		changed = changed || written
	}
	if changed {
		if _, err := run(ctx, "networkctl", "reload"); err != nil {
			return fmt.Errorf("services: networkd reload: %w", err)
		}
	}
	return nil
}

// syncFileService installs (or removes) one whole-file daemon config. A
// content change restarts the owning unit — a restart, not a reload, is the
// point for unbound (listen interfaces change only on restart, not on
// SIGHUP) and dnsmasq (DHCP configuration is not fully re-read on reload).
// A removal stops the unit: a daemon left running on distro-default config
// would open a second DNS listener on :53 that the store never asked for.
func (r *Reloader) syncFileService(ctx context.Context, run Runner, file, unit string, content []byte) error {
	existed, err := fileExists(file)
	if err != nil {
		return err
	}
	if content == nil {
		if !existed {
			return nil // service absent before and after: nothing to do
		}
		if err := os.Remove(file); err != nil {
			return fmt.Errorf("services: remove %s: %w", file, err)
		}
		if _, err := run(ctx, "systemctl", "stop", unit); err != nil {
			return fmt.Errorf("services: stop %s: %w", unit, err)
		}
		return nil
	}
	written, err := writeFileIfChanged(file, content, 0o644)
	if err != nil {
		return err
	}
	if !written && existed {
		return nil // unchanged render: the running daemon already has it
	}
	if _, err := run(ctx, "systemctl", "restart", unit); err != nil {
		return fmt.Errorf("services: restart %s: %w", unit, err)
	}
	return nil
}

// syncWireGuard materializes wg0: ensure the private key, install the
// rendered public config, join them into a /run sync file (the private key
// never appears in a rendered artifact), ensure the link exists, and apply
// with wg syncconf. An absent config section tears the interface down.
func (r *Reloader) syncWireGuard(ctx context.Context, run Runner, rendered *render.Rendered) error {
	confFile := path(r.Root, "etc/wireguard/wg0.conf")
	keyFile := path(r.Root, "etc/wireguard/wg0.key")
	runtimeFile := path(r.Root, "run/axonwall/wg0.conf")

	if rendered.WireGuard == nil {
		if _, err := run(ctx, "ip", "link", "show", config.WireGuardInterfaceName); err == nil {
			if _, err := run(ctx, "ip", "link", "delete", config.WireGuardInterfaceName); err != nil {
				return fmt.Errorf("services: delete %s: %w", config.WireGuardInterfaceName, err)
			}
		}
		return removeFiles(confFile, runtimeFile)
	}

	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		out, err := run(ctx, "wg", "genkey")
		if err != nil {
			return fmt.Errorf("services: generate %s private key: %w", config.WireGuardInterfaceName, err)
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return fmt.Errorf("services: create key dir: %w", err)
		}
		if err := os.WriteFile(keyFile, bytes.TrimSpace(out), 0o600); err != nil {
			return fmt.Errorf("services: write %s private key: %w", config.WireGuardInterfaceName, err)
		}
	}
	key, err := r.keyReader()(keyFile)
	if err != nil {
		return fmt.Errorf("services: read %s private key: %w", config.WireGuardInterfaceName, err)
	}

	if _, err := writeFileIfChanged(confFile, rendered.WireGuard, 0o600); err != nil {
		return err
	}
	// The runtime sync file carries the private key: tmpfs (/run), 0600,
	// never the rendered artifact itself. Written only when its content
	// changes: a no-change sync then runs no commands at all, and the
	// syncconf/bring-up sequence below runs exactly when the interface
	// state must move.
	var runtime bytes.Buffer
	fmt.Fprintf(&runtime, "[Interface]\nPrivateKey = %s\n", strings.TrimSpace(string(key)))
	runtime.Write(rendered.WireGuard)
	if err := os.MkdirAll(filepath.Dir(runtimeFile), 0o700); err != nil {
		return fmt.Errorf("services: create runtime dir: %w", err)
	}
	written, err := writeFileIfChanged(runtimeFile, runtime.Bytes(), 0o600)
	if err != nil {
		return err
	}
	if !written {
		return nil // interface state already matches the store
	}
	if _, err := run(ctx, "ip", "link", "show", config.WireGuardInterfaceName); err != nil {
		if _, err := run(ctx, "ip", "link", "add", config.WireGuardInterfaceName, "type", "wireguard"); err != nil {
			return fmt.Errorf("services: create %s: %w", config.WireGuardInterfaceName, err)
		}
	}
	if _, err := run(ctx, "wg", "syncconf", config.WireGuardInterfaceName, runtimeFile); err != nil {
		return fmt.Errorf("services: syncconf %s: %w", config.WireGuardInterfaceName, err)
	}
	if _, err := run(ctx, "ip", "link", "set", config.WireGuardInterfaceName, "up"); err != nil {
		return fmt.Errorf("services: bring up %s: %w", config.WireGuardInterfaceName, err)
	}
	return nil
}

// fileExists reports whether file exists (readable or not); a read error
// other than absence is a sync error, not a "missing file".
func fileExists(file string) (bool, error) {
	_, err := os.Stat(file)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("services: stat %s: %w", file, err)
}

// managedFiles lists AxonWall-owned unit files in dir.
func managedFiles(dir, prefix string, suffixes []string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("services: list %s: %w", dir, err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				out[name] = true
				break
			}
		}
	}
	return out, nil
}

// writeFileIfChanged writes content atomically (temp file + rename) when it
// differs from what is on disk; it reports whether anything was written.
func writeFileIfChanged(file string, content []byte, mode os.FileMode) (bool, error) {
	existing, err := os.ReadFile(file)
	switch {
	case err == nil && bytes.Equal(existing, content):
		return false, nil
	case err != nil && !os.IsNotExist(err):
		return false, fmt.Errorf("services: read %s: %w", file, err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return false, fmt.Errorf("services: create dir for %s: %w", file, err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return false, fmt.Errorf("services: write %s: %w", file, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return false, fmt.Errorf("services: install %s: %w", file, err)
	}
	return true, nil
}

// removeFiles removes files, ignoring absence (idempotent teardown).
func removeFiles(files ...string) error {
	for _, f := range files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("services: remove %s: %w", f, err)
		}
	}
	return nil
}

// path joins the reloader's filesystem root with an absolute system path
// ("" or "/" both mean the real root).
func path(root, system string) string {
	if root == "" {
		return system
	}
	return filepath.Join(root, system)
}
