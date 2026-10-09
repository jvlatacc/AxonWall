#!/usr/bin/env python3
"""AxonWall CI boot test (spec art_mxHM10hO, wave-1 criterion 2).

Boots the built LIVE ISO in QEMU under TCG — hosted runners have no KVM —
on both firmware paths (BIOS/SeaBIOS and UEFI/OVMF) and asserts, on the
SERIAL CONSOLE transcript (never on build logs), that the appliance reaches
a working firewall: the deterministic "AXONWALL: FIREWALL ACTIVE" marker.

The marker is emitted by nftables.service's ExecStartPost banner
(build/livebuild/config/includes.chroot_after_packages/etc/systemd/system/
nftables.service.d/axonwall-banner.conf), which runs only after `nft -f`
committed the baseline ruleset — default-drop forward policy included.
Seeing it on serial means: boot chain works, kernel up, live system
initialized, firewall ruleset loaded.

Boot-menu handling: the stock bootloaders wait for a keystroke indefinitely
(syslinux `timeout 0` DISABLES the auto-boot — the opposite of GRUB — and
GRUB's stock config.cfg sets no timeout at all, which also waits forever).
The image therefore sets explicit finite auto-boot timeouts (grub.cfg
`set timeout=10`; isolinux `timeout 100` with a serial menu), so the default
(live) entry boots on its own — the appliance-correct posture for a headless
box, and the first CI boot-test failure (2026-10-09) was exactly the missing
timeout: the menu sat on serial for the whole assertion window. As a
fallback the driver still presses Enter via the QEMU monitor (`sendkey`) on
a timer — on a waiting menu, Enter boots the default entry; after boot it
lands on the serial getty prompt where it is harmless. Monitor responses
are logged so an injection failure is visible instead of silent.

No disk is attached: this tests the boot-the-ISO path only (the
install-to-disk path has its own driver, build/ci/install-test.py).

All evidence lands in the work directory: serial.log, qemu-cmd.txt,
assert-*.txt, assert-summary.txt. QEMU and OVMF must be installed.
"""

import argparse
import glob
import os
import shutil
import socket
import subprocess
import sys
import time

# Deterministic serial marker (keep in sync with the emission sites:
# build/livebuild/config/includes.chroot_after_packages/.../axonwall-banner.conf
# and appliance/cmd/axond/marker.go).
MARKER = "AXONWALL: FIREWALL ACTIVE"

# A serial transcript showing this means the boot itself broke.
KERNEL_PANIC = "Kernel panic"

FIRST_ENTER_DELAY_S = 20  # SeaBIOS/OVMF + menu load under TCG
ENTER_INTERVAL_S = 15
MONITOR_DRAIN_S = 1  # per-command HMP response drain

OVMF_CANDIDATES = (
    ("/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"),
    ("/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"),
)


def log(msg: str) -> None:
    print(f"[boot-test] {msg}", flush=True)


def die(msg: str) -> None:
    log(f"FATAL: {msg}")
    sys.exit(1)


class SerialVM:
    """A QEMU process whose serial line is our stdout pipe."""

    def __init__(self, args: list, logfile) -> None:
        self.proc = subprocess.Popen(
            args,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            bufsize=0,
        )
        self.logfile = logfile
        self.fd = self.proc.stdout.fileno()
        os.set_blocking(self.fd, False)
        self.buf = b""

    def read_available(self) -> None:
        while True:
            try:
                chunk = os.read(self.fd, 65536)
            except BlockingIOError:
                return
            if not chunk:
                return
            self.buf += chunk
            self.logfile.write(chunk)
            self.logfile.flush()

    def wait_exit(self, grace_s: float = 10) -> None:
        """Terminate the VM once the marker is seen; evidence is captured."""
        self.proc.terminate()
        deadline = time.monotonic() + grace_s
        while time.monotonic() < deadline and self.proc.poll() is None:
            time.sleep(0.2)
        if self.proc.poll() is None:
            self.proc.kill()


class Monitor:
    """HMP over a unix socket — the emulated-keyboard driver."""

    def __init__(self, path: str) -> None:
        self.path = path
        self.sock = None

    def connect_when_ready(self, deadline: float) -> None:
        while time.monotonic() < deadline:
            try:
                s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                s.connect(self.path)
                self.sock = s
                banner = self._read_available(2.0)
                log(f"[monitor] connected; banner: {banner!r}")
                return
            except (FileNotFoundError, ConnectionRefusedError, OSError):
                time.sleep(0.5)
        die("QEMU monitor socket never appeared")

    def _read_available(self, wait_s: float) -> bytes:
        """Read whatever the monitor sends within wait_s seconds."""
        data = b""
        deadline = time.monotonic() + wait_s
        self.sock.setblocking(False)
        try:
            while time.monotonic() < deadline:
                try:
                    chunk = self.sock.recv(4096)
                    if chunk:
                        data += chunk
                except BlockingIOError:
                    time.sleep(0.05)
        finally:
            self.sock.setblocking(True)
        return data

    def command(self, cmd: str) -> bytes:
        """Send an HMP command and log its response — never a blind drain."""
        self.sock.sendall(cmd.encode() + b"\n")
        resp = self._read_available(MONITOR_DRAIN_S)
        log(f"[monitor] {cmd!r} -> {resp!r}")
        return resp

    def sendkey_enter(self) -> None:
        # HMP sendkey performs the press and release in one command.
        resp = self.command("sendkey enter")
        low = resp.lower()
        if b"error" in low or b"unknown" in low or b"invalid" in low:
            log("[monitor] WARNING: sendkey may have been rejected — "
                "the boot relies on the image's finite menu timeouts")


def find_ovmf(workdir: str) -> tuple:
    for code, vars_src in OVMF_CANDIDATES:
        if os.path.exists(code) and os.path.exists(vars_src):
            # VARS is the NVRAM: copy once into the work dir and use the copy.
            vars_dst = os.path.join(workdir, "ovmf-vars.fd")
            if not os.path.exists(vars_dst):
                shutil.copyfile(vars_src, vars_dst)
            return code, vars_dst
    die("OVMF firmware not found (install the ovmf package) — required for --firmware uefi")


def qemu_args(iso: str, firmware: str, workdir: str) -> list:
    args = [
        "qemu-system-x86_64",
        # TCG software emulation — hosted runners expose no /dev/kvm.
        "-accel", "tcg,thread=multi",
        # Debian 13 requires an x86-64-v2 CPU; "max" gives TCG's fullest
        # feature set (the default qemu64 model may lack v2 flags).
        "-cpu", "max",
        "-smp", "2",
        "-m", "2048",
        "-display", "none",
        "-serial", "stdio",
        "-monitor", f"unix:{os.path.join(workdir, 'monitor.sock')},server,nowait",
        "-nic", "user,model=e1000",
        # An unexpected guest reboot exits QEMU — caught as an early exit.
        "-no-reboot",
        "-cdrom", iso,
        "-boot", "d",
    ]
    if firmware == "uefi":
        code, vars_file = find_ovmf(workdir)
        args += [
            "-drive", f"if=pflash,format=raw,readonly=on,file={code}",
            "-drive", f"if=pflash,format=raw,file={vars_file}",
        ]
    return args


def resolve_iso(spec: str) -> str:
    matches = sorted(glob.glob(spec))
    if not matches and os.path.exists(spec):
        matches = [spec]
    if not matches:
        die(f"no ISO matches {spec!r}")
    if len(matches) > 1:
        die(f"multiple ISOs match {spec!r}: {matches}")
    return matches[0]


def context_around(text: str, needle: str, window: int = 2) -> str:
    lines = text.splitlines()
    for i, line in enumerate(lines):
        if needle in line:
            lo, hi = max(0, i - window), min(len(lines), i + window + 1)
            return "\n".join(lines[lo:hi])
    return ""


def write_asserts(workdir: str, firmware: str, text: str, elapsed: float,
                  marker_seen: bool, why: str | None = None) -> list:
    """Write all assert evidence — called on success AND on every failure path,
    so the uploaded evidence artifact always carries the assertions."""
    tail = text[-2000:] if not marker_seen else context_around(text, MARKER)
    with open(os.path.join(workdir, "assert-marker.txt"), "w") as f:
        f.write(f"{MARKER!r} {'SEEN' if marker_seen else 'NOT SEEN'} after {elapsed:.0f}s\n")
        if why:
            f.write(f"reason: {why}\n")
        if not marker_seen:
            f.write("\nserial transcript tail (last 2000 chars):\n")
        f.write(tail + "\n")

    panic_present = KERNEL_PANIC in text
    with open(os.path.join(workdir, "assert-no-panic.txt"), "w") as f:
        f.write("no kernel panic in the serial transcript\n" if not panic_present
                else "KERNEL PANIC PRESENT\n" + context_around(text, KERNEL_PANIC) + "\n")

    checks = [
        f"{'PASS' if marker_seen else 'FAIL'}: firewall-active serial marker on the "
        f"{firmware} boot ({elapsed:.0f}s)",
        f"{'PASS' if not panic_present else 'FAIL'}: no kernel panic in the serial transcript",
    ]
    with open(os.path.join(workdir, "assert-summary.txt"), "w") as f:
        f.write("\n".join(checks) + "\n")
    for line in checks:
        log(line)
    return checks


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--iso", required=True, help="path to the AxonWall ISO (glob ok)")
    ap.add_argument("--firmware", required=True, choices=("bios", "uefi"))
    ap.add_argument("--work", required=True, help="work directory for evidence")
    ap.add_argument("--boot-timeout", type=int, default=3600,
                    help="seconds allowed to reach the marker on serial")
    parsed = ap.parse_args()

    if shutil.which("qemu-system-x86_64") is None:
        die("qemu-system-x86_64 not found")
    iso = resolve_iso(parsed.iso)
    os.makedirs(parsed.work, exist_ok=True)

    qemu_cmd = qemu_args(iso, parsed.firmware, parsed.work)
    with open(os.path.join(parsed.work, "qemu-cmd.txt"), "w") as f:
        f.write(" ".join(qemu_cmd) + "\n")

    serial_log_path = os.path.join(parsed.work, "serial.log")
    started = time.monotonic()
    with open(serial_log_path, "wb") as lf:
        vm = SerialVM(qemu_cmd, lf)
        monitor = Monitor(os.path.join(parsed.work, "monitor.sock"))
        monitor.connect_when_ready(time.monotonic() + 300)

        marker_deadline = time.monotonic() + parsed.boot_timeout
        next_enter = time.monotonic() + FIRST_ENTER_DELAY_S
        entered = 0
        failure: str | None = None
        while failure is None:
            vm.read_available()
            if MARKER.encode() in vm.buf:
                break
            if KERNEL_PANIC.encode() in vm.buf:
                failure = "kernel panic on the serial console"
            elif vm.proc.poll() is not None:
                vm.read_available()
                failure = f"QEMU exited early (rc={vm.proc.returncode}) before the marker"
            elif time.monotonic() >= marker_deadline:
                failure = (f"timed out after {parsed.boot_timeout}s waiting for "
                           f"{MARKER!r} on the serial console")
            elif time.monotonic() >= next_enter:
                monitor.sendkey_enter()
                entered += 1
                log(f"[{parsed.firmware}] Enter #{entered} sent to the boot menu/keyboard")
                next_enter += ENTER_INTERVAL_S
            time.sleep(0.5)

        elapsed = time.monotonic() - started
        if failure is not None:
            vm.wait_exit()
        else:
            # Marker seen — capture a final slice of serial output, then stop.
            time.sleep(2)
            vm.read_available()
            vm.wait_exit()

    text = vm.buf.decode("utf-8", "replace")
    write_asserts(parsed.work, parsed.firmware, text, elapsed,
                  failure is None, failure)
    if failure is not None:
        die(f"{failure}; see {serial_log_path}")
    log(f"[{parsed.firmware}] ALL CHECKS PASSED")


if __name__ == "__main__":
    main()
