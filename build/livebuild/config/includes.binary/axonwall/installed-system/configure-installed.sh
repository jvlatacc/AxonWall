#!/bin/sh
# AxonWall installed-system configuration — executed by d-i preseed/late_command
# (runs in the installer environment; /target is the mounted installed system and
# /cdrom is the AxonWall ISO).
#
# Carries the live image's appliance state into the installed system:
#   - the nftables baseline, resolver config, and key-only SSH drop-in (identical
#     files to includes.chroot_after_packages — keep in sync),
#   - the same service enable set as hooks/normal/0100-axonwall-enable-services,
#   - deterministic identity, serial-console getty and GRUB wiring.
set -eu

# Serial-visible progress: the late_command's output goes to d-i's internal log
# (tty4) by default, invisible to anything tailing the serial console. /dev/console
# IS the serial console under console=ttyS0, so route execution there — this is how
# the CI install test (and any operator watching serial) sees what the configurator
# is doing and where it stops. stdin is cut: a command here that reads stdin would
# otherwise block forever with no terminal to answer it.
exec > /dev/console 2>&1 < /dev/null
echo "AxonWall: configurator started"

STAGING=/cdrom/axonwall/installed-system

# --- appliance files (same firewall state as the live image) --------------------
cp "$STAGING/etc/nftables.conf" /target/etc/nftables.conf
# The live image self-resolves via Unbound on loopback; the installed system must
# too (rm first: d-i's resolv.conf may be a symlink into its own runtime).
rm -f /target/etc/resolv.conf
cp "$STAGING/etc/resolv.conf" /target/etc/resolv.conf
mkdir -p /target/etc/ssh/sshd_config.d
cp "$STAGING/etc/ssh/sshd_config.d/10-axonwall-hardening.conf" \
	/target/etc/ssh/sshd_config.d/10-axonwall-hardening.conf
mkdir -p /target/etc/systemd/system/nftables.service.d
cp "$STAGING/etc/systemd/system/nftables.service.d/axonwall-banner.conf" \
	/target/etc/systemd/system/nftables.service.d/axonwall-banner.conf
cp "$STAGING/etc/systemd/system/axond.service" \
	/target/etc/systemd/system/axond.service

# --- services: same enable set as the live image --------------------------------
echo "AxonWall: configurator: enabling services"
in-target systemctl enable systemd-networkd.service
in-target systemctl enable nftables.service
in-target systemctl enable unbound.service
in-target systemctl enable serial-getty@ttyS0.service
in-target systemctl enable ssh.service
# axond: ConditionPathExists-gated on /usr/bin/axond (the appliance package lands
# with the axond integration) — enabling now is dormant until that package exists.
in-target systemctl enable axond.service
in-target systemctl disable dnsmasq.service 2>/dev/null || true

# --- deterministic identity ------------------------------------------------------
echo "AxonWall: configurator: identity"
printf 'axonwall\n' > /target/etc/hostname
cat > /target/etc/hosts <<'EOF'
127.0.0.1	localhost
127.0.1.1	axonwall

# The following lines are desirable for IPv6 capable hosts
::1     localhost ip6-localhost ip6-loopback
fe00::0 ip6-localnet
ff00::0 ip6-mcastprefix
ff02::1 ip6-allnodes
ff02::2 ip6-allrouters
EOF

# --- persistent /config: label + fstab wiring ------------------------------------
echo "AxonWall: configurator: /config wiring"
# The recipe formats /config with label AXONCONFIG and mounts it for the install;
# d-i's fstab line uses UUID. The appliance contract is the LABEL (device names
# differ per firmware, and the label is what the docs, backup tooling, and the
# reinstall path key on), so rewrite the line and re-assert the label.
CFG_DEV="$(awk '$2 == "/target/config" { print $1; exit }' /proc/mounts)"
if [ -z "$CFG_DEV" ]; then
	echo "FATAL: no partition mounted at /target/config — /config recipe entry missing" >&2
	exit 1
fi
e2label "$CFG_DEV" AXONCONFIG
awk '$2 != "/config"' /target/etc/fstab > /target/etc/fstab.axonwall
# nofail: a missing /config partition must not stop the appliance from booting
# into its static nftables baseline; axond re-inits an empty store (nofail is
# deliberate, not laziness).
printf 'LABEL=AXONCONFIG\t/config\text4\tdefaults,nofail\t0\t2\n' >> /target/etc/fstab.axonwall
mv /target/etc/fstab.axonwall /target/etc/fstab

# --- UEFI without NVRAM: removable-media fallback path on the ESP ----------------
echo "AxonWall: configurator: EFI fallback"
# efibootmgr entries live in board NVRAM — a fresh board (or OVMF with clean
# vars) has none, so the ESP must also expose the standard \EFI\BOOT\BOOTX64.EFI
# fallback. Keep the Secure Boot chain intact: BOOTX64.EFI is the shim when one
# is installed, with grub next to it (shim loads grubx64.efi from its own dir).
# Idempotent and skipped on BIOS-only installs (no ESP).
if [ -d /target/boot/efi/EFI ]; then
	SHIM_EFI="$(find /target/boot/efi/EFI -name 'shimx64.efi' 2>/dev/null | head -n1)"
	GRUB_EFI="$(find /target/boot/efi/EFI -name 'grubx64.efi' 2>/dev/null | head -n1)"
	mkdir -p /target/boot/efi/EFI/BOOT
	if [ -n "$SHIM_EFI" ] && [ -n "$GRUB_EFI" ]; then
		cp "$SHIM_EFI" /target/boot/efi/EFI/BOOT/BOOTX64.EFI
		cp "$GRUB_EFI" /target/boot/efi/EFI/BOOT/grubx64.efi
	elif [ -n "$GRUB_EFI" ]; then
		cp "$GRUB_EFI" /target/boot/efi/EFI/BOOT/BOOTX64.EFI
	fi
fi

# --- serial console persistence (installed GRUB: menu + kernel on ttyS0) --------
echo "AxonWall: configurator: grub persistence (update-grub next)"
sed -i 's|^GRUB_CMDLINE_LINUX=.*|GRUB_CMDLINE_LINUX="console=tty0 console=ttyS0,115200n8"|' \
	/target/etc/default/grub
grep -q '^GRUB_TERMINAL=' /target/etc/default/grub || \
	printf 'GRUB_TERMINAL="console serial"\n' >> /target/etc/default/grub
# An appliance never dual-boots: os-prober disk scans are wasted time (and can
# stall the generator in the installer chroot).
grep -q '^GRUB_DISABLE_OS_PROBER=' /target/etc/default/grub || \
	printf 'GRUB_DISABLE_OS_PROBER=true\n' >> /target/etc/default/grub
grep -q '^GRUB_SERIAL_COMMAND=' /target/etc/default/grub || \
	printf 'GRUB_SERIAL_COMMAND="serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1"\n' \
		>> /target/etc/default/grub
# in-target binds /proc, /sys and /dev before chrooting — update-grub needs them
# for device discovery. Bounded: if the generator wedges, keep grub-installer's
# working grub.cfg (d-i already wrote one with the serial console params) instead
# of hanging finish-install for the rest of the driver's timeout.
if ! in-target timeout 300 update-grub; then
	echo "AxonWall: WARNING: update-grub did not complete; using the grub.cfg from grub-installer"
fi
ls -la /target/boot/grub/grub.cfg
echo "AxonWall: configurator: update-grub done"

# Serial-console root login (recovery path; /etc/securetty may not exist on newer
# util-linux — absent means unrestricted).
if [ -f /target/etc/securetty ] && ! grep -q '^ttyS0$' /target/etc/securetty; then
	printf 'ttyS0\n' >> /target/etc/securetty
fi

echo "AxonWall: installed system configured"
