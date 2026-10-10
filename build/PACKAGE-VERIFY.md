# Trixie package verification (wave-1 spike)

Date: 2026-10-09 · Method: direct fetch of the real trixie binary-amd64 package indexes
(`dists/trixie/{main,contrib,non-free,non-free-firmware}/Packages.xz` from deb.debian.org)
with per-package resolution. The same check re-runs inside the pinned build container on
every ISO build (`build/package-verify.sh`) against the pinned snapshot.debian.org
timestamp and fails the build if a required package becomes unresolvable.

Point release verified this session: trixie = **13.7** (`dists/trixie/Release`:
`Version: 13.7`, dated 2026-09-12 — still current on 2026-10-09). The build container
pins the matching `debian:trixie` digest (the `trixie-20261005` date tag).

## Wave-1 runtime packages — all present

| Package | Version in trixie | Area | Note |
|---|---|---|---|
| nftables | 1.1.3-1 | main | firewall engine |
| systemd | 257.13-1~deb13u1 | main | systemd-networkd ships inside this package |
| dnsmasq | 2.91-1+deb13u2 | main | wave-1 DHCP |
| unbound | 1.22.0-2+deb13u3 | main | resolver |
| wireguard-tools | 1.0.20210914-3 | main | in-kernel WireGuard |
| openssh-server | 1:10.0p1-7+deb13u4 | main | key-only hardening baked in |
| live-build | 1:20250505+deb13u1 | main | build container toolchain |
| live-boot / live-config / live-tools | 1:20250815~deb13u1 / 11.0.5 / 1:20240525 | main | live runtime |

## Wave-2 targets (informational) — present

| Package | Version | Note |
|---|---|---|
| kea | 2.6.3-1+deb13u1 | meta package |
| kea-dhcp4-server | 2.6.3-1+deb13u1 | parity/HA DHCP target |
| kea-ctrl-agent | 2.6.3-1+deb13u1 | control API |

## Curated firmware subset — all in non-free-firmware (20250410-2)

| Package | Covers |
|---|---|
| firmware-realtek | Realtek wired + WiFi NICs |
| firmware-iwlwifi | Intel WiFi NICs |
| firmware-misc-nonfree | Intel ethernet blobs (e100/igb/igc/ixgbe/i40e/ice) — Debian does not split these into a NIC-only package, so this is the minimal Intel-NIC set |

## Secure Boot chain — present

| Package | Version |
|---|---|
| shim-signed | 1.51~1+deb13u1+16.1-2~deb13u1 |
| grub-efi-amd64-signed | 1+2.12+9+deb13u2 |
| grub-efi-amd64-bin | 2.12-9+deb13u2 |

## Captive-portal re-check — one correction to the research doc

The parity research (art_fC7h13nL §3/§8) recorded wifidog / coova-chilli / opennds as
"absent from trixie (prima facie)" and required re-verification on a real trixie system.
Re-verified this session against the real trixie indexes:

- wifidog, coova-chilli, chillispot — **confirmed absent** from trixie (all areas checked).
- **opennds 10.3.1+dfsg-1 IS present in trixie main** — the research's absence claim does
  not hold. This does not change wave-1 scope (captive portal is wave 3), but the wave-3
  spec should evaluate packaging opennds as the substrate instead of a fully custom build.

**Action for the wave-3 spec:** re-run the captive-portal gap analysis with opennds as a
candidate before speccing the custom build.
