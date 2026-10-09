# AxonWall

AxonWall is a standalone Debian firewall appliance that replaces OPNsense — the
same class of bootable, self-contained firewall/router, built on Debian instead
of FreeBSD, delivered from a single ISO. Full OPNsense feature parity is the
program goal, delivered in waves: the first ISO carries the core gateway
(stateful nftables firewall, DHCP, Unbound DNS resolver, WireGuard, config
backup/restore, a REST API and web console), and later waves stack on it. There
is no BSD anywhere in the deployment path, and AxonWall has no dependency on any
other project — it builds and ships from this repository alone.

The program spec is
[AxonWall — Debian firewall appliance spec](https://app.obvious.ai/p/axonwall-debian-firewall-iso-gIZ0rIkv)
(art_mxHM10hO); the in-repo architecture overview is
[docs/architecture.md](docs/architecture.md).

## Status

Wave 1 is in progress on the
`release/axonwall-bootable-debian-firewall-iso-replacing-opnsense-20261009-203645`
branch. Merged so far: the ISO image factory, the axond core (config schema,
git-backed `/config` store, TLS token API), backup/restore, the nftables engine
with confirm-or-rollback, the service renderers (systemd-networkd, dnsmasq,
Unbound, WireGuard), and the QEMU boot-test/release pipeline. Landing on the
same branch: install-to-disk with unattended preseed
([PR #8](https://github.com/jvlatacc/AxonWall/pull/8)) and token auth, live
axond wiring, and the first-boot setup wizard
([PR #11](https://github.com/jvlatacc/AxonWall/pull/11)).

## ISO quickstart

### 1. Download

Releases are pinned appliance images published on version tags. Once the first
`v*` tag is pushed, grab the ISO (and its `SHA256SUMS` and the dpkg package
manifest) from
[Releases](https://github.com/jvlatacc/AxonWall/releases). Until then, build it
yourself — one command, see
[Build from source](#build-from-source).

Verify what you downloaded:

```sh
sha256sum --check SHA256SUMS
```

### 2. Boot

The ISO is hybrid media: one image, BIOS (isolinux) and UEFI (signed
shim + GRUB, Secure Boot enabled) bootloaders, serial console on `ttyS0`
(115200n8) alongside the VGA console.

| Target | How |
|---|---|
| Proxmox VM | Attach the ISO to a VM with the default OVMF (UEFI) or SeaBIOS (BIOS) firmware; virtio NICs are supported out of the box |
| Bare metal, UEFI | Write the ISO to a USB stick and boot it; Secure Boot works via Debian's signed shim/GRUB chain |
| Bare metal, BIOS | Same USB stick; the isolinux entry boots on legacy BIOS |
| Headless | Use the serial console (`ttyS0,115200n8`) — the boot menu and the live system both appear on serial |

To write a USB stick (replace `sdX` with your device — this destroys its
contents):

```sh
sudo dd if=axonwall-<sha>-amd64.iso of=/dev/sdX bs=4M conv=fsync status=progress
```

### 3. First-boot setup

The live system boots with a safe-by-default nftables baseline (input and
forward drop, established/related and loopback accepted), Unbound as the DNS
resolver, and systemd-networkd owning all interfaces. Management access is
key-only SSH over the LAN plus the axond REST API (HTTPS with a bearer token —
there is no insecure mode).

The guided first-boot setup — admin token provisioning and the setup wizard in
the web console — lands with
[PR #11](https://github.com/jvlatacc/AxonWall/pull/11). Until it merges, use
the `ax` CLI against axond's API (`ax config get`, `ax config put`, `ax
confirm`, `ax backup export|restore`); every configuration change flows through
the same validate → render → atomic apply → health-check → confirm-or-rollback
pipeline described in [docs/architecture.md](docs/architecture.md).

### 4. Install to disk

Install-to-disk (unattended, preseed-driven, with `/config` on its own
persistent partition so configuration survives image replacement) lands with
[PR #8](https://github.com/jvlatacc/AxonWall/pull/8).

## Boot matrix

| Boot path | Bootloader | Verified by |
|---|---|---|
| BIOS / legacy | isolinux (syslinux) | CI boot test, QEMU TCG |
| UEFI | shim + GRUB (signed chain) | CI boot test, QEMU TCG + OVMF |
| Secure Boot | Debian's signed shim/GRUB (`--uefi-secure-boot enable`) | media verification in every ISO build |
| Proxmox / KVM | SeaBIOS (BIOS) or OVMF (UEFI), virtio NICs | both firmware paths tested |

Every CI run boots the freshly built ISO under QEMU (software emulation —
hosted runners have no KVM) on **both** firmware paths and asserts a
deterministic firewall-active marker on the serial console transcript before it
is allowed to pass.

## Build from source

One command from a clean checkout, inside the digest-pinned Debian trixie
build container (details in [build/README.md](build/README.md)):

```sh
docker build -t axonwall/iso-builder build/container
docker run --rm --privileged -v "$PWD:/repo" axonwall/iso-builder build/build-iso.sh
```

Outputs land in `dist/`: the hybrid ISO, `SHA256SUMS`, the dpkg package
manifest, and the boot-catalog verification report. The build itself gates on
trixie package verification at the pinned snapshot, hybrid-media evidence, and
a 2 GiB size limit (the GitHub release-asset cap).

## Repository layout

| Path | Contents |
|---|---|
| `build/` | ISO image factory — `build-iso.sh` wrapper, live-build config, pinned container, CI boot-test driver ([build/README.md](build/README.md)) |
| `appliance/` | Go module — `axond` (API + apply pipeline), `ax` (CLI), internal packages: config, render, apply, services, health, rollback, store, backup |
| `ui/` | React 19 + TypeScript + Vite web console, served by axond |
| `docs/` | Architecture and design records ([architecture.md](docs/architecture.md), [rules-semantics.md](docs/rules-semantics.md), [service-renderers.md](docs/service-renderers.md)) |
| `.github/workflows/` | CI (Go tests/lint, ISO build, QEMU boot tests, commit hygiene) and the tag-driven release pipeline |

## Documentation

- [docs/architecture.md](docs/architecture.md) — the three-layer shape (image
  factory, runtime appliance, management plane) and the apply pipeline.
- [docs/rules-semantics.md](docs/rules-semantics.md) — pf → nftables rule
  evaluation semantics (the core OPNsense translation decision).
- [docs/service-renderers.md](docs/service-renderers.md) — what each renderer
  emits, where files land, and how each service reloads.
- [build/PACKAGE-VERIFY.md](build/PACKAGE-VERIFY.md) — trixie package
  verification at the pinned snapshot.

## Credits and licenses

AxonWall runs on Debian and ships upstream free-software components — the
Debian base, live-build, and every shipped daemon (nftables, systemd-networkd,
dnsmasq, Unbound, WireGuard, OpenSSH, and the rest of the curated package set).
Component versions and licenses are recorded in [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES). AxonWall itself does not yet carry
a project license file.
