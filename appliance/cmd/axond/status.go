// Runtime status reporting for the web console: a read-only snapshot of
// host, interface, service, and connection-tracking state. Sources are
// best-effort by section — an environment without systemd or conntrack
// reports honest empty/stopped sections, never fabricated data. The UI
// renders whatever this returns; it does not guess.
package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jvlatacc/AxonWall/appliance/internal/config"
)

// version is the appliance build version, overridable at link time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/axond
var version = "dev"

// conntrackEntriesLimit caps how many connection states are parsed per
// status request — the UI table paginates, and the endpoint must stay
// cheap on a busy gateway.
const conntrackEntriesLimit = 500

// interfaceStatus is the JSON shape of SystemStatus.interfaces (ui/src/api/types.ts).
type interfaceStatus struct {
	Name       string   `json:"name"`
	Zone       string   `json:"zone"`
	Addressing string   `json:"addressing"`
	Addresses  []string `json:"addresses"`
	State      string   `json:"state"`
	RxBytes    uint64   `json:"rxBytes"`
	TxBytes    uint64   `json:"txBytes"`
}

// serviceStatus is the JSON shape of SystemStatus.services.
type serviceStatus struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// stateEntry is the JSON shape of SystemStatus.states.
type stateEntry struct {
	Protocol       string `json:"protocol"`
	Source         string `json:"source"`
	Destination    string `json:"destination"`
	State          string `json:"state"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// statusLogEntry is the JSON shape of LogEntry. Wave 1 has no live drop-log
// source wired into axond yet (nftables drop logging lands with the log
// backend), so the endpoint always returns an empty log — the UI shows its
// empty state rather than stale data.
type statusLogEntry struct {
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}

// systemStatus is the JSON shape of SystemStatus (ui/src/api/types.ts).
type systemStatus struct {
	Hostname      string            `json:"hostname"`
	Version       string            `json:"version"`
	UptimeSeconds int               `json:"uptimeSeconds"`
	Revision      string            `json:"revision"`
	Interfaces    []interfaceStatus `json:"interfaces"`
	Services      []serviceStatus   `json:"services"`
	States        []stateEntry      `json:"states"`
	Log           []statusLogEntry  `json:"log"`
}

// handleStatus serves GET /status: host facts from the kernel, interface
// rows from the config plus runtime state, service rows from systemd, and
// connection states from conntrack. Store reads are serialized against
// config PUTs so a status snapshot can never straddle an apply.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, rev, err := s.store.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	status := systemStatus{
		Hostname:      hostname(),
		Version:       version,
		UptimeSeconds: uptimeSeconds(),
		Revision:      rev,
		Interfaces:    interfaceRows(cfg),
		Services:      serviceRows(cfg),
		States:        conntrackStates(),
		Log:           []statusLogEntry{},
	}
	writeJSON(w, http.StatusOK, status)
}

// handleNetDevices serves GET /net/devices: the kernel network devices a
// first-boot setup wizard can offer for WAN/LAN assignment. Loopback is
// never assignable and is excluded.
func (s *Server) handleNetDevices(w http.ResponseWriter, _ *http.Request) {
	ifaces, err := net.Interfaces()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type netDevice struct {
		Name string `json:"name"`
		MAC  string `json:"mac"`
		Up   bool   `json:"up"`
	}
	devices := []netDevice{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		devices = append(devices, netDevice{
			Name: ifc.Name,
			MAC:  ifc.HardwareAddr.String(),
			Up:   ifc.Flags&net.FlagUp != 0,
		})
	}
	writeJSON(w, http.StatusOK, devices)
}

// hostname never fails on Linux; an empty string is the honest degenerate case.
func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}

// uptimeSeconds reads /proc/uptime; 0 when unavailable (non-Linux dev host).
func uptimeSeconds() int {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int(uptime)
}

// interfaceRows joins the config's logical interfaces with runtime state:
// up/down and DHCP addresses from the kernel device named by `match`,
// counters from /proc/net/dev. Static addresses come from the config —
// the store is the source of truth for what the appliance declares.
func interfaceRows(cfg *config.Config) []interfaceStatus {
	zoneOf := map[string]string{}
	for zone, z := range cfg.Zones {
		for _, name := range z.Interfaces {
			zoneOf[name] = zone
		}
	}
	counters := devCounters()

	rows := []interfaceStatus{}
	for i := range cfg.Interfaces {
		ifc := cfg.Interfaces[i]
		row := interfaceStatus{
			Name:       ifc.Name,
			Zone:       zoneOf[ifc.Name],
			Addressing: ifc.Addressing,
			Addresses:  []string{},
			State:      "down",
		}
		if ifc.Addressing == "static" {
			row.Addresses = append(row.Addresses, ifc.Address...)
		}
		if dev, err := net.InterfaceByName(ifc.Match); err == nil {
			if dev.Flags&net.FlagUp != 0 {
				row.State = "up"
			}
			if ifc.Addressing == "dhcp" {
				row.Addresses = append(row.Addresses, runtimeAddresses(dev)...)
			}
		}
		if c, ok := counters[ifc.Match]; ok {
			row.RxBytes, row.TxBytes = c.rx, c.tx
		}
		rows = append(rows, row)
	}
	return rows
}

// runtimeAddresses returns an interface's IPv4/IPv6 addresses as CIDR strings.
func runtimeAddresses(dev *net.Interface) []string {
	addrs, err := dev.Addrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			out = append(out, ipnet.String())
		}
	}
	return out
}

type devCounter struct{ rx, tx uint64 }

// devCounters parses /proc/net/dev into per-device byte counters; a missing
// file (non-Linux) yields an empty map and zeroed counters.
func devCounters() map[string]devCounter {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	counters := map[string]devCounter{}
	scan := bufio.NewScanner(f)
	for line := 0; scan.Scan(); line++ {
		// Two header lines precede the data rows.
		if line < 2 {
			continue
		}
		name, rest, ok := strings.Cut(scan.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		// Column 0 is rx bytes, column 8 tx bytes.
		if len(fields) < 16 {
			continue
		}
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[8], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		counters[strings.TrimSpace(name)] = devCounter{rx: rx, tx: tx}
	}
	return counters
}

// serviceRows reports each configured service's unit state. Wave-1 units:
// DNS and DHCP render to unbound/dnsmasq (services.NewReloader unit names),
// WireGuard brings up wg0. systemctl unavailable (containers, test hosts)
// reports "stopped" with the reason — never a fabricated "running".
func serviceRows(cfg *config.Config) []serviceStatus {
	type unit struct{ name, unit string }
	units := []unit{}
	if cfg.Services.DNS != nil {
		units = append(units, unit{name: "dns (unbound)", unit: "unbound"})
	}
	if cfg.Services.DHCP != nil {
		units = append(units, unit{name: "dhcp (dnsmasq)", unit: "dnsmasq"})
	}
	if cfg.Services.WireGuard != nil {
		units = append(units, unit{name: "wireguard (wg0)", unit: "wg-quick@wg0"})
	}

	rows := []serviceStatus{}
	for _, u := range units {
		state, detail := unitState(u.unit)
		rows = append(rows, serviceStatus{Name: u.name, State: state, Detail: detail})
	}
	return rows
}

// unitState asks systemd for a unit's active state, with a timeout so a
// hung init system cannot stall the status endpoint.
func unitState(unit string) (string, string) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "stopped", "systemd is not available in this environment"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output() // #nosec G204 - unit names come from the internal serviceUnits list, never request input
	state := strings.TrimSpace(string(out))
	if err != nil {
		detail := strings.TrimSpace(state)
		if detail == "" {
			detail = "cannot query systemd"
		}
		return "stopped", detail
	}
	switch state {
	case "active":
		return "running", ""
	case "failed":
		return "degraded", "unit is failed"
	default: // inactive, activating, unknown…
		return "stopped", state
	}
}

// conntrackStates reads the kernel's connection table. Unreadable (no
// conntrack, permission denied in a container) is an honest empty list —
// the UI renders its empty state.
func conntrackStates() []stateEntry {
	for _, path := range []string{"/proc/net/nf_conntrack", "/proc/net/ip_conntrack"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		entries, parseErr := parseConntrack(f)
		_ = f.Close()
		if parseErr == nil && len(entries) > 0 {
			return entries
		}
	}
	return []stateEntry{}
}

// parseConntrack extracts proto/src/dst/state/timeout from /proc/net
// conntrack lines:
//
//	ipv4 2 tcp 6 431993 ESTABLISHED src=10.0.0.2 dst=10.0.0.1 sport=443 dport=50000
func parseConntrack(f *os.File) ([]stateEntry, error) {
	entries := []stateEntry{}
	scan := bufio.NewScanner(f)
	for scan.Scan() && len(entries) < conntrackEntriesLimit {
		fields := strings.Fields(scan.Text())
		if len(fields) < 5 {
			continue
		}
		proto := fields[2]
		if proto != "tcp" && proto != "udp" && proto != "icmp" {
			continue
		}
		timeout, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		state := "NEW"
		entry := stateEntry{Protocol: proto, State: state, TimeoutSeconds: timeout}
		for _, kv := range fields[5:] {
			switch kv {
			case "ESTABLISHED", "TIME_WAIT", "NEW":
				entry.State = kv
				continue
			}
			if key, val, ok := strings.Cut(kv, "="); ok {
				switch key {
				case "src":
					entry.Source = val
				case "dst":
					entry.Destination = val
				}
			}
		}
		if entry.Source != "" || entry.Destination != "" {
			entries = append(entries, entry)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
