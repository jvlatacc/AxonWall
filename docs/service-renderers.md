# Service renderers (systemd-networkd, dnsmasq, unbound, WireGuard)

The spec's management-plane rule is that daemons are render targets: the
config store is the single source of truth, and nothing edits daemon
configs by hand. PR #7 made the nftables ruleset a render target; this
document records how the four service daemons join it — what each renderer
emits, where the files land, and how each service is re-materialized on
apply. It also records the reload-semantics decisions and the honest gaps
that render-time knowledge cannot cover.

## What each renderer emits

All four renderers run inside `render.All` next to the nftables renderer;
their artifacts ride on `render.Rendered` and reach the kernel / daemons
only through the apply pipeline (`validate → render → nft -c check →
stage known-good → atomic apply → service reload → health → commit`).

| Renderer | Artifact | Installed path | Reload action |
| --- | --- | --- | --- |
| networkd | `10-axonwall-<name>.network` + `.link` per interface | `/etc/systemd/network/` | `networkctl reload` |
| dnsmasq | complete `/etc/dnsmasq.conf` | `/etc/dnsmasq.conf` | `systemctl restart dnsmasq` (on change) |
| unbound | complete `/etc/unbound/unbound.conf` | `/etc/unbound/unbound.conf` | `systemctl restart unbound` (on change) |
| WireGuard | rendered `wg0` config | `/etc/wireguard/wg0.conf` (public config); runtime copy with the private key under `/run` | link ensure, `wg syncconf`, link up |

Decisions that shaped each renderer:

- **dnsmasq owns DHCP only.** The rendered config sets `port=0`, which
  disables dnsmasq's DNS listener entirely — the appliance's resolver is
  unbound, and one DNS listener per appliance is the rule (two daemons
  bound to :53 is a race, not a feature). DHCP is served on the interfaces
  of the zones that carry pools (`interface=` lines); pools are tagged with
  `dhcp-range=set:<zone>,...` so `dhcp-option=tag:<zone>,...` delivers the
  pool's gateway and DNS server per network. The netmask is appended to a
  `dhcp-range` only when the zone's static subnets can derive it;
  dnsmasq infers it from the interface otherwise (verified against
  dnsmasq(8): the netmask is optional for directly connected networks).
  The whole `/etc/dnsmasq.conf` is owned rather than a drop-in under
  `/etc/dnsmasq.d/` — the renderer must not depend on a distro conffile's
  `conf-dir` line being present.
- **unbound listens on zone interfaces by name.** unbound.conf(5) accepts
  interface names in `interface:` and binds the interface's addresses
  ("If an interface name is used instead of an ip address, the list of ip
  addresses on that interface are used"). Access control is explicit: the
  static subnets of listened zones get `access-control: <subnet> allow`,
  and unbound's own default ("if none match, refuse is used") refuses
  everything else — the firewall's input chain is the outer gate, this is
  the inner one. Forward mode (`services.dns.mode: forward` with
  `services.dns.forwarders`) renders a `forward-zone: name: "."` block;
  recursive mode is the default.
- **unbound and dnsmasq restart on change; networkd reloads.** unbound
  manpage, verbatim: "The interfaces are not changed on a reload
  (kill -HUP) but only on restart." Since listening on zones is the
  renderer's whole job, a listen change must restart unbound — a reload
  would silently keep the old listeners. dnsmasq's DHCP configuration is
  likewise not fully re-read on SIGHUP, so both daemons restart when their
  rendered content changes (content-equal applies skip the restart).
  systemd-networkd's native `networkctl reload` is safe and complete for
  `.network`/`.link` units, so no restart is needed there.
- **`.link` units replicate Debian's default naming policy.** A `.link`
  file that matched and carried no directives would still *suppress*
  `99-default.link` — the first matching `.link` file wins and later files
  are ignored (systemd.link(5)) — so a comment-only unit would silently
  break predictable interface naming and with it every `match:` in the
  config store. Each AxonWall `.link` therefore matches on
  `OriginalName=` and carries the default policy's own directives
  (`NamePolicy=kernel database onboard slot path`,
  `AlternativeNamesPolicy=database onboard slot path`,
  `MACAddressPolicy=persistent`): it claims the device first without
  changing naming behavior. (`keep-name` was considered and dropped: no
  current systemd manpage documents it.)
- **WireGuard private keys never touch a rendered file.** The config
  store holds public keys only. The reloader ensures a private key exists
  at `/etc/wireguard/wg0.key` (0600, generated with `wg genkey` when
  absent), composes the runtime sync file — rendered public config plus
  the `PrivateKey` line — under `/run` (tmpfs, 0600), and applies it with
  `wg syncconf wg0`. `wg syncconf` is the idempotent entry point: it
  accepts the INI-style config with comments, and re-applying an
  unchanged config is a no-op on the tunnel.

## Reload orchestration (appliance/internal/services)

`services.Reloader.Sync` is the only code that installs rendered service
configs and restarts daemons; the apply pipeline calls it between the
atomic ruleset apply and the health check, so a failed reload restores the
known-good ruleset like any other apply failure.

The boundary of the rollback guarantee is deliberate and worth stating
plainly: the kernel ruleset is restored on failure, but files already
written for a service whose reload failed stay on disk. Service configs
are idempotent re-renders, the store remains the source of truth, and the
next successful apply re-renders and reloads — the drift window is
bounded and self-healing, whereas cross-service file rollback would add a
second failure surface to a path whose job is to be simple.

Stale-file collection is part of each service's sync: when a service
section disappears from the config (or loses units), the previously
rendered files are removed, and daemons that lost their config are
restarted or (for WireGuard) their interface is deleted, so the running
state follows the store in both directions.

## Honest gaps (render-time knowledge)

- **DHCP interfaces have no render-time subnet.** A zone served by a DHCP
  uplink contributes no `access-control` to unbound and no netmask to a
  dnsmasq range; unbound's refuse-by-default and dnsmasq's interface
  inference cover both. Zones that serve DNS or DHCP to clients must have
  at least one statically addressed interface — which is the wave-1
  default topology anyway (LAN static, WAN DHCP).
- **wg0 has no address in the wave-1 schema.** The renderer emits no
  address for the WireGuard interface; operators add one out of band or a
  later schema revision carries it. Zone membership (for firewall rules)
  and peer routing work regardless — the netns traffic test exercises the
  tunnel without interface addressing on the renderer's side.
- **Full `/etc/dnsmasq.conf` and `/etc/unbound/unbound.conf` ownership**
  replaces package conffiles. That is intended (daemons are render
  targets), but it means package upgrades of those daemons must never
  regenerate behavior we depend on without a matching renderer change —
  pinned appliance releases absorb this (the package set moves only with
  the ISO).

## Verification

- Golden-file tests per renderer pin every rendered byte; a diff is
  meaningful because it is what the daemons will run.
- Reloader unit tests use a fake command runner and a temp filesystem
  root: write/GC behavior, restart-only-on-change, WireGuard key and
  runtime-file handling, and interface deletion.
- The privileged netns test (`cmd/axond`, `unshare -n` re-exec) drives the
  real end-to-end path — a WireGuard peer added through `PUT /config`
  reaches a running pipeline, `wg syncconf` materializes `wg0`, and UDP
  traffic flows between two network namespaces through the tunnel.
