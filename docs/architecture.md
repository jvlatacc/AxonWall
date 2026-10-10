# Architecture

AxonWall is three layers: an **image factory** that builds the ISO, a
**runtime appliance** that firewalls, and a **management plane** that makes it
operable without SSH. One declarative config store — git-backed at `/config` —
is the single source of truth. Everything else is either *rendered from it*
(daemons and the firewall ruleset) or *read from it* (the REST API and web
console). Nothing edits a daemon's config file by hand; the renderers are
idempotent and the store is the only writer.

```
                        ┌─────────────────────────────────────────┐
                        │              CONFIG STORE               │
                        │   /config · axonwall.yaml · git-backed  │
                        │        (single source of truth)         │
                        └───▲─────────────────────▲───────────────┘
     UI / ax / REST API ────┘                     │ git commit after
     (management plane)                           │ health passes
                        │                         │
                        │   ┌─────────────────────┴───────────┐
                        │   │           APPLY PIPELINE        │
                        │   │ validate → render → nft -c      │
                        │   │ → stage known-good → atomic     │
                        │   │ apply → reload → health →       │
                        │   │ commit → confirm-or-rollback    │
                        │   └─────────────────────┬───────────┘
                        │                         │ rendered files
              ┌─────────┴─────────┐    ┌──────────▼──────────────────┐
              │   RUNTIME KERNEL  │    │  RENDER TARGETS (daemons)   │
              │  nftables ruleset │    │ networkd · dnsmasq ·        │
              │  (atomic nft -f)  │    │ unbound · wireguard         │
              └───────────────────┘    └─────────────────────────────┘
```

The program spec is the
[AxonWall blueprint](https://app.obvious.ai/p/axonwall-debian-firewall-iso-gIZ0rIkv)
(art_mxHM10hO). This document describes the shape of the system as it exists in
the tree; the two deep-dives beside it cover the parts where AxonWall had to
make an explicit decision: [rules-semantics.md](rules-semantics.md) (pf →
nftables evaluation) and [service-renderers.md](service-renderers.md) (what
each renderer emits and reloads).

## Layer 1 — image factory

The factory turns a clean checkout into a bootable hybrid ISO with one command.
It wraps [Debian live-build](https://packages.debian.org/trixie/live-build) —
media mechanics come from live-build; product decisions (package set, pinning,
bootloaders, hooks) come from this repo. This is the VyOS "wrap, don't fork"
model: live-build is never modified.

- **Entry point:** [`build/build-iso.sh`](../build/build-iso.sh), meant to run
  as root inside the digest-pinned build container
  ([build/container/Dockerfile](../build/container/Dockerfile) — `debian:trixie`
  at the 13.7 point release).
- **Image definition:** [build/livebuild/auto/config](../build/livebuild/auto/config)
  is the single source of truth for live-build options — hybrid BIOS (isolinux)
  + UEFI (shim/GRUB) media, `--uefi-secure-boot enable`, serial console
  (`console=ttyS0,115200n8`), archive areas `main non-free-firmware`.
- **Reproducibility:** build-time packages resolve from `snapshot.debian.org`
  at `AXONWALL_SNAPSHOT` (default `20261005T000000Z`, after the 13.7 release);
  the *installed* appliance's apt sources point at `deb.debian.org` so it
  updates normally.
- **Package set:** explicit lists in
  [build/livebuild/config/package-lists/](../build/livebuild/config/package-lists/)
  — the runtime set (nftables, systemd, dnsmasq, unbound, wireguard-tools,
  openssh-server, conntrack, iproute2, git, …) plus a curated firmware subset
  (Realtek + Intel NICs). Every required package is re-verified against the
  pinned snapshot on every build by `build/package-verify.sh`; the findings
  live in [build/PACKAGE-VERIFY.md](../build/PACKAGE-VERIFY.md).
- **Baked-in files:** the safe-by-default nftables baseline, key-only SSH
  hardening, and the service baseline hook live under
  [build/livebuild/config/includes.chroot_after_packages/](../build/livebuild/config/includes.chroot_after_packages/)
  and [build/livebuild/config/hooks/](../build/livebuild/config/hooks/).
- **Outputs:** `dist/axonwall-<git-sha>-amd64.iso`, `SHA256SUMS`, the dpkg
  package manifest, and the El Torito boot-catalog report. Every build gates on
  package verification, hybrid-media evidence, and the 2 GiB release-asset cap.

Full factory documentation: [build/README.md](../build/README.md).

## Layer 2 — runtime appliance

The ISO boots into a live Debian system that is already a working firewall, and
the same packages run the installed system.

**Boot and recovery.** isolinux on BIOS, Debian's signed shim/GRUB chain on
UEFI (Secure Boot verified). The serial console is deterministic — boot menu
and `serial-getty@ttyS0` login both appear on `ttyS0,115200n8` — because a
headless appliance with no recovery path is a locked door.

**Interfaces.** systemd-networkd owns every appliance interface; it is the only
link manager. The
[service baseline hook](../build/livebuild/config/hooks/normal/0100-axonwall-enable-services.chroot)
enables networkd, the nftables baseline, Unbound, key-only SSH, and the serial
console — and deliberately leaves dnsmasq disabled until axond renders and
enables it with a real config (a blind default would fight Unbound for port 53).

**Firewall.** Raw nftables, single owner, one complete ruleset. The bare image
ships a
[baseline ruleset](../build/livebuild/config/includes.chroot_after_packages/etc/nftables.conf)
with input and forward `policy drop`, established/related and loopback
accepted, and management SSH (key-only) allowed — enough to be safe and
reachable; axond is the runtime owner and re-renders the real ruleset from the
store on every config change. Rule-evaluation semantics (one base chain per
hook, config order, first terminal verdict wins — the pf first-match model) are
specified and empirically verified in [rules-semantics.md](rules-semantics.md).

**Services.** Unbound is the DNS resolver from day one (DNSSEC-validating,
recursive, optional forwarding); dnsmasq serves DHCP only (`port=0` — no second
DNS listener); WireGuard is in-kernel with `wireguard-tools` userland. Each is
a render target — see [service-renderers.md](service-renderers.md) for what
each renderer writes and how each service is re-materialized on apply.

**Config store.** `/config` — git-backed. The store ([appliance/internal/store](../appliance/internal/store))
is the single source of truth; git history is the audit log, and backup/restore
round-trips through it. Because the store is the only writer, a restored
backup and a fresh apply converge on exactly the same rendered state.

## Layer 3 — management plane

**axond** ([appliance/cmd/axond](../appliance/cmd/axond)) is the Go daemon at
the center: it renders and applies the committed config at boot, serves the
REST API over TLS with bearer-token auth, and refuses to start without a token
source — there is no insecure mode. Endpoints (see
[server.go](../appliance/cmd/axond/server.go)):

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | health probe (unauthenticated) |
| `GET /config` · `PUT /config` | read / replace the config store (schema-validated) |
| `GET /backup/export` · `POST /backup/restore` | config backup and restore |
| `POST /confirm` | confirm a recent apply, canceling the rollback timer |

**ax** ([appliance/cmd/ax](../appliance/cmd/ax)) is the CLI — a thin client for
the same API, local or remote (`ax config get|put`, `ax backup export|restore`,
`ax confirm`; token via `--token` or `AX_TOKEN`).

**Web console** ([ui/](../ui)) — React 19 + TypeScript + Vite, token
authenticated, served by axond. Every visible change in the console flows
through the config store; the UI never touches nftables or daemon configs
directly. The store schema ([sample config](../appliance/internal/config/testdata/sample.yaml))
covers zones, interfaces, DNS, DHCP, WireGuard, firewall defaults/aliases/NAT/rules.

## The apply pipeline

Every change — UI, `ax`, API — takes the same path. This is the appliance's
safety property: there is exactly one mutation path from config intent to
kernel state, and it cannot half-apply.

1. **Validate** — the transaction is schema-validated before anything renders
   ([appliance/internal/config](../appliance/internal/config)); invalid config
   is rejected before the kernel ever sees it.
2. **Render** — `render.All` emits the complete nftables ruleset plus the four
   service configs ([appliance/internal/render](../appliance/internal/render)).
3. **Check** — `nft -c -f` validates the candidate ruleset without loading it
   ([appliance/internal/apply/nft.go](../appliance/internal/apply/nft.go)).
4. **Stage known-good** — the running ruleset is saved so failure paths can
   restore it.
5. **Atomic apply** — `nft -f` submits the whole ruleset as one kernel
   transaction; a partial ruleset is unrepresentable.
6. **Reload services** — rendered service configs are installed and each
   service is re-materialized (restart or reload per
   [service-renderers.md](service-renderers.md)).
7. **Health check** — self-check from an independent path (management
   reachability, daemon state).
8. **Commit** — only after health passes does the store's git commit land.
9. **Confirm-or-rollback** — atomic apply is *not* automatic rollback: a valid
   but locking ruleset would apply atomically and stay. So a confirmation
   window (`DefaultConfirmWindow`, 5 minutes, in
   [appliance/internal/apply/pipeline.go](../appliance/internal/apply/pipeline.go))
   runs after commit; `POST /confirm` (or `ax confirm`) cancels the timer, and
   an expired window restores the previous ruleset and config
   ([appliance/internal/rollback](../appliance/internal/rollback),
   [appliance/internal/backup](../appliance/internal/backup)).

The same path serves restores: a restored backup is validated, rendered, and
applied exactly like a config change, so restore and apply cannot drift apart.

## Where things live

| Area | Path |
|---|---|
| ISO factory | [`build/`](../build) — [build-iso.sh](../build/build-iso.sh), [container](../build/container/Dockerfile), [livebuild config](../build/livebuild/auto/config) |
| Apply pipeline | [`appliance/internal/apply/`](../appliance/internal/apply), [rollback](../appliance/internal/rollback/timer.go), [health](../appliance/internal/health/health.go) |
| Renderers | [`appliance/internal/render/`](../appliance/internal/render) — design notes in [service-renderers.md](service-renderers.md) |
| Config store | [`appliance/internal/store/`](../appliance/internal/store), schema in [config](../appliance/internal/config/config.go) + [sample.yaml](../appliance/internal/config/testdata/sample.yaml) |
| API + CLI | [`appliance/cmd/axond/`](../appliance/cmd/axond), [`appliance/cmd/ax/`](../appliance/cmd/ax) |
| Web console | [`ui/`](../ui) |
| Backup / restore | [`appliance/internal/backup/`](../appliance/internal/backup) |
| CI + release | [`.github/workflows/ci.yml`](../.github/workflows/ci.yml), [`.github/workflows/release.yml`](../.github/workflows/release.yml), [boot-test driver](../build/ci/boot-test.py) |

## Verification

CI ([.github/workflows/ci.yml](../.github/workflows/ci.yml)) enforces commit
hygiene, runs the Go test/lint suite, builds the ISO in the pinned container,
and boots it under QEMU TCG on BIOS and UEFI, asserting a deterministic
firewall-active marker on the serial transcript. Tagged (`v*`) pushes rebuild
the ISO the same way and publish it as a release with checksums and the dpkg
manifest ([.github/workflows/release.yml](../.github/workflows/release.yml)).
