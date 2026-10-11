# nftables rule-evaluation semantics (pf → nftables)

The AxonWall spec names this the single most important translation decision:
OPNsense evaluates rules under pf's first-match semantics with three ordered
priority tiers; nftables evaluates base chains by hook priority. This document
records the semantics AxonWall chose, why, and how they are verified.

## What OPNsense/pf does

- pf is **first-match**: the first rule whose match set matches a packet and
  carries a `quick` (or terminal) verdict decides its fate.
- Rule ordering has three tiers: **floating** rules (priority 200000), then
  interface-group rules (300000), then interface rules (400000).
- NAT is processed **before** filter rules, so filter rules see translated
  addresses.

## What nftables does natively

- Base chains attach to hooks (input/forward/output) at priorities. **All
  base chains on the same hook evaluate** — a packet `accept`ed by one chain
  can still be `drop`ped by a later one.
- Within a single chain, the first rule that matches **and carries a terminal
  verdict** (`accept`/`drop`/`reject`) stops evaluation of that chain.

Both behaviors were verified empirically with isolated network namespaces and
live packet probes: first-matching-verdict-wins inside one chain, and
cross-chain drops overriding earlier accepts.

## AxonWall's decision

**One base chain per hook, rules in config order, first terminal verdict
wins.**

1. AxonWall renders exactly one base chain per hook (`input`, `forward`,
   `output`) inside its single owned `table inet axonwall`. Because there is
   only one base chain per hook, the cross-chain hazard above cannot occur,
   and first-verdict-wins-within-a-chain becomes first-match semantics for
   the whole firewall — the pf model users expect.
2. Rules render in **config order**, top to bottom. That order is the only
   precedence: there are no floating/group/interface tiers. Migrating users
   read the ruleset exactly like a pf floating list.
3. Default policies: `drop` on input and forward, `accept` on output, with
   loopback, established/related, and ICMP/ICMPv6 accepted early — the
   canonical appliance posture.
4. There is no `quick` equivalent and none is needed: in a single chain the
   first terminal verdict already ends evaluation.

### Chain priorities

| Chain   | Hook        | Priority (named) |
| ------- | ----------- | ---------------- |
| filter  | input       | `filter` (0)     |
| filter  | forward     | `filter` (0)     |
| filter  | output      | `filter` (0)     |
| dstnat  | prerouting  | `dstnat` (-100)  |
| srcnat  | postrouting | `srcnat` (100)   |

AxonWall owns its table exclusively (the rendered ruleset starts with
`flush ruleset`). Other software must not add chains to this table —
single-owner rulesets are the control-plane decision behind atomic apply.

### NAT before filter

pf's NAT-before-filter ordering maps onto hook ordering:

- **Port-forward (DNAT)** renders into the `dstnat` chain at prerouting
  priority -100. Translation happens before the forward filter chain
  evaluates, and conntrack tracks it — so the forward-chain allowance the
  renderer emits for every port forward matches the **translated**
  destination (verified with `nft -c` and live probes).
- **Masquerade** renders into the `srcnat` chain at postrouting, matching
  the out-interface and source zone.

### Aliases

Aliases render as named nftables sets (`ipv4_addr`/`ipv6_addr`), interval-
flagged when they carry CIDR prefixes (url-table sets are always interval-
flagged — feeds legitimately mix addresses and prefixes). Rules reference
sets, so alias membership changes never require rule changes.

URL-table alias sets refresh from their feed through scoped atomic
transactions (`flush set` + `add element` in one `nft -f` fragment applied
by the axond alias refresher); a failed fetch or apply keeps the last-good
set. Feed updates deliberately bypass the full render/apply pipeline — they
are additive set updates, not config changes.

## What this model gives up

- **Floating vs interface rule precedence** — config order is the only
  precedence. OPNsense's tier ordering (floating before interface rules) has
  no AxonWall equivalent; users must order rules explicitly.
- **`quick`** — unnecessary under single-chain evaluation.

## Verification

- Golden-file renderer tests pin the complete ruleset byte-for-byte; each
  golden is validated with real `nft -c`, and the loaded-in-namespace
  behavior was probed live.
- Privileged integration tests (netns, `internal/apply/netns_test.go`) cover
  the behavioral contract: invalid configs rejected before the kernel, valid
  applies loading, forced rollback restoring the known-good ruleset, healthy
  applies committing, and alias-feed fragments replacing set elements
  atomically.
- Unprivileged environments (CI containers) run the same test binary without
  privileges: unit tests and `nft -c`-validated fixtures carry the gate,
  privileged tests skip.
