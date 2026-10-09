# AxonWall image factory

Builds the bootable AxonWall ISO: Debian 13 trixie (pinned point release **13.7**),
hybrid BIOS+UEFI media with Secure Boot, serial console, curated non-free-firmware
(Realtek/Intel NICs), key-only SSH, and a safe-by-default nftables baseline.
Spec: art_mxHM10hO (wave 1, acceptance criterion 1).

## One command (inside the pinned container)

```sh
docker build -t axonwall/iso-builder build/container
docker run --rm --privileged -v "$PWD:/repo" axonwall/iso-builder build/build-iso.sh
```

`--privileged` is required: live-build debootstraps and chroots inside the container.
No KVM is involved — this job builds and structurally verifies the media; boot tests
run separately.

## Outputs (`dist/`)

| File | Contents |
|---|---|
| `axonwall-<git-sha>-amd64.iso` | hybrid ISO (BIOS isolinux + UEFI shim/grub) |
| `axonwall-<git-sha>-amd64.iso.manifest.txt` | every package in the image, from the build chroot's dpkg database |
| `SHA256SUMS` | checksums for the ISO + manifest |
| `package-verify.txt` | trixie package verification at the pinned snapshot |
| `el-torito-report.txt` | boot-catalog evidence for the hybrid-media assertion |

## Gates baked into `build-iso.sh` (each fails the build)

1. **Package verification** — wave-1 required packages resolvable at the pinned snapshot.
2. **Hybrid media** — BIOS (isolinux) and UEFI (`efi.img` containing shim/grub) El Torito
   entries present.
3. **Size** — ISO ≤ 2 GiB (GitHub release asset limit).

## Pinning

- **Base image:** `debian:trixie` pinned by digest (the `trixie-20261005` date tag,
  point release 13.7) — see `build/container/Dockerfile`.
- **Build-time packages:** `snapshot.debian.org` at `AXONWALL_SNAPSHOT`
  (default `20261005T000000Z`, i.e. after the 13.7 release of 2026-09-12).
- **Runtime apt sources in the image:** `deb.debian.org` — the installed appliance
  updates normally.
- **Override:** `AXONWALL_MIRROR_MODE=live` builds against deb.debian.org
  (faster, not pinned — for iteration only).

## Layout

| Path | Purpose |
|---|---|
| `livebuild/auto/config` | live-build options — the single source of truth for the image definition |
| `livebuild/config/package-lists/` | explicit package sets (runtime + curated firmware) |
| `livebuild/config/hooks/` | chroot-stage service baseline (see the no-op pairing note under `live/`) |
| `livebuild/config/includes.chroot_after_packages/` | baked-in files: sshd hardening, nftables baseline, resolv.conf |
| `livebuild/config/bootloaders/grub-pc/` | grub.cfg override adding the serial console menu |
| `container/` | digest-pinned build container |
| `build-iso.sh` | the one-command wrapper |
| `package-verify.sh` | trixie package-verification spike (CI gate) |
| `PACKAGE-VERIFY.md` | spike findings (including the opennds correction) |

## Known limitation (deliberate, wave-1 scope)

live-config's default `user` account still exists in the live system (not
auto-logged-in — `noautologin` is set; password auth is not accepted over SSH).
Console credential policy is first-boot-setup work that lands with axond.
