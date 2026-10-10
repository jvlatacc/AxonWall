#!/usr/bin/env python3
"""AxonWall CI install-to-disk test (spec art_mxHM10hO, wave-1 criterion 3).

Runs the ISO's debian-installer unattended under QEMU/TCG (hosted runners have
no KVM). The installer kernel/initrd are booted straight from the ISO via
-kernel/-initrd/-append with the preseed on the kernel command line — the
first CI run showed boot-menu serial input (isolinux/GRUB arrow keys) does
not register over the QEMU stdio pipe, so menu interaction is designed out.
The installer reboots at the end; -no-reboot makes QEMU exit. The test then
boots the installed system from disk (no ISO attached), logs in over the
serial getty, probes the running appliance, powers off, and asserts — on the
serial transcripts and host-side against the disk image — that the install
is unattended, the firewall is active, and /config sits on its own
AXONCONFIG-labelled partition.

All evidence lands in the work directory: serial-install.log,
serial-runtime.log, assert-*.txt, assert-summary.txt.

Linux-only (losetup/blkid host asserts); QEMU and OVMF must be installed.
"""

import argparse
import glob
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

# Deterministic serial markers (keep in sync with the files they come from).
# Canonical appliance marker — keep in sync with axond (marker.go) and the
# live banner (includes.chroot_after_packages/.../axonwall-banner.conf).
FIREWALL_ACTIVE_MARKER = "AXONWALL: FIREWALL ACTIVE"
CONFIGURED_MARKER = "AxonWall: installed system configured"
# Bootstrap root password — public by design, documented in build/README.md.
ROOT_PASSWORD = "axonwall-bootstrap"

LOGIN_HINT = "login:"

OVMF_CANDIDATES = (
    ("/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"),
    ("/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"),
)


def log(msg: str) -> None:
    print(f"[install-test] {msg}", flush=True)


def die(msg: str) -> None:
    log(f"FATAL: {msg}")
    sys.exit(1)


def run(cmd: list, **kw) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=False, **kw)


class SerialVM:
    """A QEMU process whose serial line is our stdio."""

    def __init__(self, args: list, logfile) -> None:
        self.proc = subprocess.Popen(
            args,
            stdin=subprocess.PIPE,
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

    def wait_for(self, pattern: str, deadline: float) -> None:
        rx = re.compile(pattern.encode())
        while time.monotonic() < deadline:
            self.read_available()
            if rx.search(self.buf):
                return
            if self.proc.poll() is not None:
                self.read_available()
                die(f"QEMU exited early while waiting for {pattern!r}; see the serial log")
            time.sleep(0.2)
        die(f"timed out waiting for {pattern!r} on the serial console")

    def send(self, data: bytes) -> None:
        assert self.proc.stdin is not None
        self.proc.stdin.write(data)
        self.proc.stdin.flush()

    def wait_exit(self, deadline: float, what: str) -> None:
        while time.monotonic() < deadline:
            self.read_available()
            if self.proc.poll() is not None:
                self.read_available()
                return
            time.sleep(0.5)
        self.proc.kill()
        die(f"timed out waiting for QEMU to exit ({what}); see the serial log")


def find_ovmf(workdir: str) -> tuple:
    for code, vars_src in OVMF_CANDIDATES:
        if os.path.exists(code) and os.path.exists(vars_src):
            # VARS is the NVRAM: copy once into the work dir and REUSE it across
            # both boots so the installer's efibootmgr entry survives into the
            # boot-from-disk phase.
            vars_dst = os.path.join(workdir, "ovmf-vars.fd")
            if not os.path.exists(vars_dst):
                shutil.copyfile(vars_src, vars_dst)
            return code, vars_dst
    die("OVMF firmware not found (install the ovmf package) — required for --firmware uefi")


def qemu_base_args(disk: str, firmware: str, workdir: str) -> list:
    args = [
        "qemu-system-x86_64",
        "-accel", "tcg,thread=multi",
        # Debian 13 requires an x86-64-v2 CPU; "max" gives TCG's fullest
        # feature set (the default qemu64 model may lack v2 flags) — same
        # shape the boot-test job proves out.
        "-cpu", "max",
        "-smp", "2",
        "-m", "2048",
        "-drive", f"file={disk},format=raw,if=virtio",
        "-display", "none",
        "-serial", "stdio",
        "-monitor", "none",
        "-nic", "user,model=e1000",
        "-no-reboot",
    ]
    if firmware == "uefi":
        code, vars_file = find_ovmf(workdir)
        args += [
            "-drive", f"if=pflash,format=raw,file={code},readonly=on",
            "-drive", f"if=pflash,format=raw,file={vars_file}",
        ]
    return args


def extract_installer(iso: str, workdir: str) -> tuple:
    """Pull the debian-installer kernel/initrd out of the ISO (xorriso, no root)."""
    dest = os.path.join(workdir, "iso-extract")
    out = run(["xorriso", "-osirrox", "on", "-indev", iso,
               "-extract", "/install", os.path.join(dest, "install")],
              capture_output=True, text=True)
    kernel = os.path.join(dest, "install", "vmlinuz")
    initrd = os.path.join(dest, "install", "initrd.gz")
    if not (os.path.exists(kernel) and os.path.exists(initrd)):
        die(f"installer kernel/initrd not extracted from {iso}: "
            f"{out.stdout[-500:]} {out.stderr[-500:]}")
    return kernel, initrd


def phase_install(iso: str, disk: str, firmware: str, workdir: str, timeout_s: int) -> None:
    """Boot the ISO's installer kernel directly; the preseed runs it unattended."""
    kernel, initrd = extract_installer(iso, workdir)
    args = qemu_base_args(disk, firmware, workdir)
    args += [
        # The installer ISO attaches as a virtio-scsi CD, NOT the -cdrom IDE
        # shorthand. CI evidence (run 38006799791): under the runner's qemu
        # 8.2.2, the IDE CD attached via -cdrom never registered in the guest
        # when the d-i kernel was booted via -kernel/-initrd (both PATA ports
        # probed, no ATAPI device, cdrom-detect retried for the full timeout).
        # virtio is the transport this initrd provably uses: virtio_blk
        # registers the install disk, and the initrd ships virtio_scsi.ko,
        # sr_mod, and isofs — so scsi-cd surfaces at /dev/sr0 for cdrom-detect.
        "-drive", f"id=cd0,file={iso},media=cdrom,if=none,readonly=on",
        "-device", "virtio-scsi-pci",
        "-device", "scsi-cd,drive=cd0",
        "-kernel", kernel,
        "-initrd", initrd,
        # The same append live-build gives the installer entries, minus
        # initrd= (QEMU loads the initrd itself): preseed off the cdrom,
        # unattended, serial console.
        "-append", "file=/cdrom/install/preseed.cfg priority=critical "
                   "console=ttyS0,115200n8",
    ]
    with open(os.path.join(workdir, "serial-install.log"), "wb") as lf:
        vm = SerialVM(args, lf)
        # debian-installer runs unattended and reboots at the end; -no-reboot
        # exits QEMU. The late-command marker is asserted after exit.
        vm.wait_exit(time.monotonic() + timeout_s, "unattended install")
        if vm.proc.returncode not in (0, None):
            die(f"installer QEMU exited with {vm.proc.returncode}; see serial-install.log")
        text = vm.buf.decode("utf-8", "replace")
    if CONFIGURED_MARKER not in text:
        die(f"{CONFIGURED_MARKER!r} not seen on the installer serial console")
    log(f"[{firmware}] install completed unattended ({CONFIGURED_MARKER!r} seen)")


def phase_runtime(disk: str, firmware: str, workdir: str, timeout_s: int) -> str:
    """Boot the installed system from disk and probe it over the serial getty."""
    args = qemu_base_args(disk, firmware, workdir)
    if firmware == "bios":
        args += ["-boot", "c"]
    with open(os.path.join(workdir, "serial-runtime.log"), "wb") as lf:
        vm = SerialVM(args, lf)
        vm.wait_for(LOGIN_HINT, time.monotonic() + timeout_s)
        log(f"[{firmware}] installed system reached the serial getty; logging in")
        vm.send(b"root\n")
        vm.wait_for("Password:", time.monotonic() + 120)
        vm.send((ROOT_PASSWORD + "\n").encode())
        time.sleep(2.0)
        # A known prompt beats guessing the default PS1. Marker strings are
        # split in the typed input ("AXON""PROMPT") so the tty echo of the
        # command can never satisfy a wait meant for real command output.
        vm.send(b"PS1='AXON''PROMPT# '\n")
        vm.wait_for("AXONPROMPT#", time.monotonic() + 120)

        def cmd(line: str) -> None:
            vm.send(f"{line}\n".encode())
            vm.wait_for("AXONPROMPT#", time.monotonic() + 300)

        # Markers are split the same way; the OUTPUT carries them contiguous.
        cmd("echo \"AXONPART-\"\"BEGIN\"; lsblk -o NAME,LABEL,FSTYPE,SIZE,MOUNTPOINTS; "
            "echo ---; blkid; echo ---; findmnt /config; echo \"AXONPART-\"\"END\"")
        cmd("echo \"AXONFW-\"\"BEGIN\"; nft list ruleset; echo \"AXONFW-\"\"END\"")
        cmd("echo \"AXONSVC-\"\"BEGIN\"; systemctl is-enabled nftables.service "
            "unbound.service systemd-networkd.service ssh.service "
            "serial-getty@ttyS0.service axond.service 2>&1; echo ---; "
            "systemctl is-enabled dnsmasq.service 2>&1; echo ---; hostname; "
            "echo \"AXONSVC-\"\"END\"")
        vm.send(b"poweroff\n")
        vm.wait_exit(time.monotonic() + timeout_s, "runtime boot + probes")
        return vm.buf.decode("utf-8", "replace")


def block(text: str, begin: str, end: str) -> str:
    m = re.search(re.escape(begin) + r"(.*?)" + re.escape(end), text, re.S)
    return m.group(1) if m else ""


def assert_block(runtime_text: str, name: str, begin: str, end: str, workdir: str) -> str:
    text = block(runtime_text, begin, end)
    with open(os.path.join(workdir, f"assert-{name}.txt"), "w") as f:
        f.write(text)
    return text


def phase_asserts(firmware: str, runtime_text: str, disk: str, workdir: str) -> None:
    failures = []
    checks = []

    def check(ok: bool, what: str) -> None:
        checks.append(f"{'PASS' if ok else 'FAIL'}: {what}")
        if not ok:
            failures.append(what)

    # --- runtime (serial transcript) asserts -----------------------------------
    part = assert_block(runtime_text, "part", "AXONPART-BEGIN", "AXONPART-END", workdir)
    fw = assert_block(runtime_text, "firewall", "AXONFW-BEGIN", "AXONFW-END", workdir)
    svc = assert_block(runtime_text, "services", "AXONSVC-BEGIN", "AXONSVC-END", workdir)

    check(FIREWALL_ACTIVE_MARKER in runtime_text,
          "firewall-active serial marker after reboot from disk")
    check("table inet filter" in fw, "nft ruleset: inet filter table loaded")
    check("policy drop" in fw, "nft ruleset: default-drop policy active")
    check('LABEL="AXONCONFIG"' in part, "blkid: AXONCONFIG label present")
    check(re.search(r"/config\s", part) is not None and "ext4" in part,
          "lsblk/findmnt: /config mounted as ext4")

    # `systemctl is-enabled a b c` prints one bare status word per unit, in order.
    svc_lines = [l.strip() for l in svc.strip().splitlines()
                 if l.strip() and l.strip() != "---"]
    units = ("nftables.service", "unbound.service", "systemd-networkd.service",
             "ssh.service", "serial-getty@ttyS0.service", "axond.service")
    for i, unit in enumerate(units):
        check(len(svc_lines) > i and svc_lines[i] == "enabled", f"unit enabled: {unit}")
    dnsmasq_status = svc_lines[len(units)] if len(svc_lines) > len(units) else ""
    check(dnsmasq_status in ("disabled", "not-found", "inactive"), "dnsmasq not enabled")
    check(len(svc_lines) > len(units) + 1 and svc_lines[len(units) + 1] == "axonwall",
          "hostname is axonwall")

    # --- host-side asserts against the disk image ------------------------------
    out = run(["sudo", "losetup", "-fP", "--show", "-r", disk], capture_output=True, text=True)
    loop = out.stdout.strip()
    if not loop:
        die(f"losetup failed: {out.stderr}")
    try:
        parts = sorted(
            p for p in os.listdir(f"/sys/block/{os.path.basename(loop)}")
            if re.fullmatch(r".*p\d+", p)
        )
        partdevs = [f"/dev/{os.path.basename(loop)}p{n + 1}" for n in range(len(parts))]
        blk = run(["sudo", "blkid", *partdevs], capture_output=True, text=True)
        with open(os.path.join(workdir, "assert-disk.txt"), "w") as f:
            f.write(blk.stdout + blk.stderr)
        check('LABEL="AXONCONFIG"' in blk.stdout, "disk image: AXONCONFIG ext4 partition exists")
        check('TYPE="vfat"' in blk.stdout, "disk image: EFI system partition present")
        check('TYPE="swap"' in blk.stdout, "disk image: swap partition present")
        # /config is the recipe's last partition (appends-friendly layout).
        if partdevs:
            last_dev = partdevs[-1]
            line = next((l for l in blk.stdout.splitlines() if l.startswith(f"{last_dev}:")), "")
            check('LABEL="AXONCONFIG"' in line, "disk image: /config is the last partition")

        # Mount the ext4 partition that is neither /boot nor /config → the root fs.
        root_mount = tempfile.mkdtemp(prefix="axonroot-", dir=workdir)
        cfg_mount = tempfile.mkdtemp(prefix="axoncfg-", dir=workdir)
        root_dev = None
        for dev in partdevs:
            line = next((l for l in blk.stdout.splitlines() if l.startswith(f"{dev}:")), "")
            if 'TYPE="ext4"' not in line or 'LABEL="AXONCONFIG"' in line:
                continue
            probe = run(["sudo", "mount", "-o", "ro", dev, root_mount],
                        capture_output=True, text=True)
            if probe.returncode == 0 and os.path.exists(os.path.join(root_mount, "etc/fstab")):
                root_dev = dev
                break
            run(["sudo", "umount", root_mount])
        check(root_dev is not None, "disk image: root filesystem located and mounted")

        if root_dev:
            def read_root(path: str) -> str:
                with open(os.path.join(root_mount, path)) as f:
                    return f.read()

            fstab = read_root("etc/fstab")
            check(re.search(r"LABEL=AXONCONFIG\s+/config\s+ext4\s+.*nofail", fstab) is not None,
                  "fstab: LABEL=AXONCONFIG /config line with nofail")
            check(re.search(r"\s/boot/efi\s+vfat\s", fstab) is not None,
                  "fstab: /boot/efi entry present")
            check(read_root("etc/hostname").strip() == "axonwall", "hostname file is axonwall")
            check("PasswordAuthentication no" in
                  read_root("etc/ssh/sshd_config.d/10-axonwall-hardening.conf"),
                  "key-only SSH drop-in staged")
            check("policy drop" in read_root("etc/nftables.conf"), "nftables baseline staged")
            check("127.0.0.1" in read_root("etc/resolv.conf"), "self-resolver resolv.conf staged")
            wants = os.path.join(root_mount, "etc/systemd/system/multi-user.target.wants")
            for unit in (*units,):
                check(os.path.exists(os.path.join(wants, unit)), f"unit staged enabled: {unit}")
            check(not os.path.lexists(os.path.join(wants, "dnsmasq.service")),
                  "dnsmasq not staged enabled")
            check("console=ttyS0,115200n8" in read_root("boot/grub/grub.cfg"),
                  "installed grub.cfg carries the serial console")

            # /config itself: mounts standalone (fresh install — empty store).
            cfg_dev = next(
                l.split(":")[0] for l in blk.stdout.splitlines() if 'LABEL="AXONCONFIG"' in l)
            probe = run(["sudo", "mount", "-o", "ro", cfg_dev, cfg_mount],
                        capture_output=True, text=True)
            check(probe.returncode == 0, "/config partition mounts standalone")
            if probe.returncode == 0:
                run(["sudo", "umount", cfg_mount])
            run(["sudo", "umount", root_mount])
    finally:
        run(["sudo", "losetup", "-d", loop])

    with open(os.path.join(workdir, "assert-summary.txt"), "w") as f:
        f.write("\n".join(checks) + "\n")
    for line in checks:
        log(line)
    if failures:
        die(f"{len(failures)} assert(s) failed on {firmware}")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--iso", required=True, help="path to the AxonWall ISO (glob ok)")
    ap.add_argument("--firmware", required=True, choices=("bios", "uefi"))
    ap.add_argument("--work", required=True, help="work directory for disk + evidence")
    ap.add_argument("--disk-size", default="8G")
    ap.add_argument("--install-timeout", type=int, default=4200,
                    help="seconds for the install phase")
    ap.add_argument("--runtime-timeout", type=int, default=900,
                    help="seconds for the boot-from-disk phase")
    parsed = ap.parse_args()

    if shutil.which("qemu-system-x86_64") is None:
        die("qemu-system-x86_64 not found")
    matches = glob.glob(parsed.iso)
    if not matches:
        die(f"no ISO matches {parsed.iso}")
    iso = matches[0]
    os.makedirs(parsed.work, exist_ok=True)
    disk = os.path.join(parsed.work, "disk.raw")
    if not os.path.exists(disk):
        run(["truncate", "-s", parsed.disk_size, disk])

    log(f"[{parsed.firmware}] phase 1: unattended install from {os.path.basename(iso)}")
    phase_install(iso, disk, parsed.firmware, parsed.work, parsed.install_timeout)
    log(f"[{parsed.firmware}] phase 2: boot from disk (no ISO attached)")
    runtime_text = phase_runtime(disk, parsed.firmware, parsed.work, parsed.runtime_timeout)
    log(f"[{parsed.firmware}] phase 3: asserts")
    phase_asserts(parsed.firmware, runtime_text, disk, parsed.work)
    log(f"[{parsed.firmware}] ALL CHECKS PASSED")


if __name__ == "__main__":
    main()
