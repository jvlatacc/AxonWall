// Form-level validation mirroring the wave-1 semantic rules in
// appliance/internal/config/validate.go. These are pure functions: they take
// raw form values plus a ValidationContext built from the current config and
// return per-field error strings (undefined = valid). The appliance re-runs
// the full check server-side; client checks exist to give instant feedback
// before a change is ever sent.

import type {
  AliasType,
  AxonWallConfig,
  DhcpPool,
  DnsMode,
  NatRule,
  PolicyAction,
  Verdict,
} from '../api/types'
import { FIREWALL_ZONE } from '../api/types'
import { inSubnets, parseCidr, parseIp } from './ip'
import type { IpRange } from './ip'

/** Name pattern for zones, logical interfaces, and aliases (validate.go: nameRE). */
export const NAME_PATTERN = /^[a-z][a-z0-9-]{0,31}$/

/** Kernel device names: eth0, enp1s0, wlp3s0, eth0.100 (validate.go: matchRE). */
export const MATCH_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9.:@_-]{0,30}$/

export const VALID_POLICIES: readonly PolicyAction[] = ['accept', 'drop']
export const VALID_VERDICTS: readonly Verdict[] = ['accept', 'drop', 'reject']
export const VALID_ALIAS_TYPES: readonly AliasType[] = ['ipv4', 'ipv6', 'url-table']
export const VALID_ADDRESSING: readonly ('dhcp' | 'static')[] = ['dhcp', 'static']
export const VALID_NAT_MODES: readonly ('masquerade' | 'port-forward')[] = ['masquerade', 'port-forward']
export const VALID_DNS_MODES: readonly DnsMode[] = ['recursive', 'forward']

/** Named services wave 1 understands (validate.go: namedServices). */
const NAMED_SERVICES: readonly string[] = ['ssh', 'http', 'https', 'dns']

export function isValidConfigName(name: string): boolean {
  return NAME_PATTERN.test(name)
}

export function isValidKernelDevice(name: string): boolean {
  return MATCH_PATTERN.test(name)
}

/** An absolute http(s) URL for a url-table alias feed (validate.go alias URL check). */
export function isValidFeedUrl(value: string): boolean {
  try {
    const u = new URL(value)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname !== ''
  } catch {
    return false
  }
}

/**
 * A port-forward destination: an IPv4 address or ip:port, mirroring
 * config.go ParsePortForwardTarget (IPv6 targets are later work).
 */
export function isValidPortForwardTarget(value: string): boolean {
  const trimmed = value.trim()
  const lastColon = trimmed.lastIndexOf(':')
  if (lastColon < 0) {
    return parseIp(trimmed)?.family === 4
  }
  const host = trimmed.slice(0, lastColon)
  const port = trimmed.slice(lastColon + 1)
  if (!/^\d{1,5}$/.test(port)) return false
  const n = Number(port)
  if (n < 1 || n > 65535) return false
  return parseIp(host)?.family === 4
}

/**
 * A service expression: a named service or `proto/port` with proto in
 * {tcp, udp} and port 1-65535 (validate.go: ValidateService).
 */
export function isValidServiceExpr(value: string): boolean {
  if (NAMED_SERVICES.includes(value)) return true
  const idx = value.indexOf('/')
  if (idx < 0) return false
  const proto = value.slice(0, idx)
  const port = value.slice(idx + 1)
  if (proto !== 'tcp' && proto !== 'udp') return false
  if (!/^\d{1,5}$/.test(port)) return false
  const n = Number(port)
  return n >= 1 && n <= 65535
}

/**
 * A WireGuard public key: base64 that decodes to exactly 32 bytes
 * (validate.go: ParseWGKey). The regex pre-check keeps atob from throwing
 * on arbitrary text; the try/catch is a deliberate fallback for malformed
 * base64 padding, treated the same as any other invalid key.
 */
export function isValidWGPublicKey(value: string): boolean {
  const trimmed = value.trim()
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(trimmed)) return false
  try {
    return atob(trimmed).length === 32
  } catch {
    return false
  }
}

/** Cross-reference context for one config document. */
export interface ValidationContext {
  readonly zoneNames: readonly string[]
  readonly interfaceNames: readonly string[]
  readonly aliasNames: readonly string[]
  /** Static subnets per zone, used for DHCP range/gateway membership. */
  readonly zoneSubnets: Readonly<Record<string, readonly IpRange[]>>
}

/** Build the cross-reference context from a config document. */
export function validationContext(config: AxonWallConfig): ValidationContext {
  const zoneSubnets: Record<string, IpRange[]> = {}
  for (const zoneName of Object.keys(config.zones)) {
    const zone = config.zones[zoneName]
    const subnets: IpRange[] = []
    for (const ifName of zone?.interfaces ?? []) {
      const ifc = config.interfaces.find((i) => i.name === ifName)
      for (const addr of ifc?.address ?? []) {
        const cidr = parseCidr(addr)
        if (cidr !== null) subnets.push(cidr)
      }
    }
    zoneSubnets[zoneName] = subnets
  }
  return {
    zoneNames: Object.keys(config.zones),
    interfaceNames: config.interfaces.map((i) => i.name),
    aliasNames: Object.keys(config.firewall.aliases),
    zoneSubnets,
  }
}

// -- Firewall rule form ----------------------------------------------------

export interface RuleValues {
  readonly name: string
  readonly from: string
  readonly to: string
  readonly service: string
  readonly sourceAlias: string
  readonly verdict: Verdict | ''
}

export type RuleErrors = Partial<Record<keyof RuleValues, string>>

export function validateRule(
  values: RuleValues,
  ctx: ValidationContext,
  takenNames: readonly string[],
): RuleErrors {
  const errors: RuleErrors = {}
  const name = values.name.trim()
  if (name === '') errors.name = 'name must be set'
  else if (!isValidConfigName(name)) errors.name = 'must be lowercase letters, digits, dashes (max 32)'
  else if (takenNames.includes(name)) errors.name = `duplicate rule name "${name}"`

  for (const [label, value] of [['from', values.from], ['to', values.to]] as const) {
    if (value === FIREWALL_ZONE) continue
    if (value === '') errors[label] = `must be a zone name or "${FIREWALL_ZONE}"`
    else if (!ctx.zoneNames.includes(value)) errors[label] = `unknown zone "${value}"`
  }

  if (values.service !== '' && !isValidServiceExpr(values.service)) {
    errors.service = 'use a named service (ssh, http, https, dns) or tcp|udp/port'
  }
  if (values.sourceAlias !== '' && !ctx.aliasNames.includes(values.sourceAlias)) {
    errors.sourceAlias = `alias "${values.sourceAlias}" is not defined`
  }
  if (!VALID_VERDICTS.includes(values.verdict as Verdict)) {
    errors.verdict = 'verdict must be accept, drop, or reject'
  }
  return errors
}

// -- Alias form ------------------------------------------------------------

export interface AliasValues {
  readonly name: string
  readonly type: AliasType
  /** Feed source, url-table only (yaml: url). */
  readonly url: string
  readonly entries: readonly string[]
}

export type AliasErrors = Partial<Record<'name' | 'type' | 'url' | 'entries', string>>

export function validateAlias(
  values: AliasValues,
  takenNames: readonly string[],
): AliasErrors {
  const errors: AliasErrors = {}
  const name = values.name.trim()
  if (name === '') errors.name = 'name must be set'
  else if (!isValidConfigName(name)) errors.name = 'must be lowercase letters, digits, dashes (max 32)'
  else if (takenNames.includes(name)) errors.name = `duplicate alias name "${name}"`

  if (!VALID_ALIAS_TYPES.includes(values.type)) errors.type = 'type must be ipv4, ipv6, or url-table'

  if (values.type === 'url-table') {
    // The feed refresh populates the set; entries are an optional seed
    // (validate.go: url-table skips the at-least-one-entry rule).
    if (values.url.trim() === '') errors.url = 'feed URL must be set'
    else if (!isValidFeedUrl(values.url.trim())) errors.url = 'must be an absolute http(s) URL'
    if (values.entries.length === 0) return errors
  }

  if (values.entries.length === 0 && values.type !== 'url-table') {
    errors.entries = 'at least one entry is required'
  } else {
    const entryFamily = values.type === 'url-table' ? 'ipv4' : values.type
    const problems = values.entries
      .map((entry, i) => aliasEntryProblem(entryFamily, entry.trim(), i))
      .filter((problem): problem is string => problem !== null)
    if (problems.length > 0) errors.entries = problems.join('; ')
  }
  return errors
}

function aliasEntryProblem(type: 'ipv4' | 'ipv6', entry: string, index: number): string | null {
  if (entry === '') return `entry ${index + 1} is empty`
  const ip = parseIp(entry)
  if (ip !== null) {
    if (type === 'ipv4' && ip.family !== 4) return `entry ${index + 1} is not an IPv4 address`
    if (type === 'ipv6' && ip.family !== 6) return `entry ${index + 1} is not an IPv6 address`
    return null
  }
  const cidr = parseCidr(entry)
  if (cidr === null) return `entry ${index + 1} is not a valid ${type} address or CIDR`
  if (type === 'ipv4' && cidr.family !== 4) return `entry ${index + 1} is not an IPv4 CIDR`
  if (type === 'ipv6' && cidr.family !== 6) return `entry ${index + 1} is not an IPv6 CIDR`
  return null
}

// -- NAT form ----------------------------------------------------------------

export interface NatValues {
  readonly name: string
  readonly mode: NatMode | ''
  // masquerade fields
  readonly out: string
  readonly source: string
  // port-forward fields
  readonly in: string
  readonly proto: 'tcp' | 'udp' | ''
  readonly dstPort: string
  readonly to: string
}

export type NatMode = (typeof VALID_NAT_MODES)[number]

export type NatErrors = Partial<Record<keyof NatValues | 'form', string>>

/**
 * Validate one NAT rule, mirroring checkNAT: the two modes have disjoint
 * field sets, so a typo lands as an error instead of a silently ignored
 * field.
 */
export function validateNat(
  values: NatValues,
  ctx: ValidationContext,
  takenNames: readonly string[],
): NatErrors {
  const errors: NatErrors = {}
  const name = values.name.trim()
  if (name === '') errors.name = 'name must be set'
  else if (!isValidConfigName(name)) errors.name = 'must be lowercase letters, digits, dashes (max 32)'
  else if (takenNames.includes(name)) errors.name = `duplicate NAT rule name "${name}"`

  if (!VALID_NAT_MODES.includes(values.mode as NatMode)) errors.mode = 'mode must be masquerade or port-forward'

  if (values.mode === 'port-forward') {
    if (values.out !== '' || values.source !== '') {
      errors.form = 'out and source belong to masquerade mode'
    }
    if (values.in === '') errors.in = 'must be set'
    else if (!ctx.interfaceNames.includes(values.in)) errors.in = `unknown interface "${values.in}"`

    if (values.proto === '') errors.proto = 'must be tcp or udp'
    const port = Number(values.dstPort)
    if (!/^\d{1,5}$/.test(values.dstPort) || port < 1 || port > 65535) {
      errors.dstPort = 'must be between 1 and 65535'
    }
    if (values.to.trim() === '') errors.to = 'must be set'
    else if (!isValidPortForwardTarget(values.to)) errors.to = 'must be an IPv4 address or ip:port'
  } else if (values.mode === 'masquerade') {
    if (values.in !== '' || values.proto !== '' || values.dstPort !== '' || values.to !== '') {
      errors.form = 'port-forward fields belong to port-forward mode'
    }
    if (values.out === '') errors.out = 'must be set'
    else if (!ctx.interfaceNames.includes(values.out)) errors.out = `unknown interface "${values.out}"`

    if (values.source === '') errors.source = 'must be set'
    else if (!ctx.zoneNames.includes(values.source)) errors.source = `unknown zone "${values.source}"`
  }
  return errors
}

/**
 * Construct the typed NAT rule from form values; null when the mode is
 * unset or the port-forward fields are malformed. validateNat reports the
 * errors, this builds the rule the store accepts.
 */
export function natRuleFromValues(values: NatValues): NatRule | null {
  const name = values.name.trim()
  if (values.mode === 'masquerade') {
    return { name, mode: 'masquerade', out: values.out, source: values.source }
  }
  if (values.mode === 'port-forward') {
    const port = Number(values.dstPort)
    if (values.proto === '' || !Number.isInteger(port) || port < 1 || port > 65535) return null
    return {
      name,
      mode: 'port-forward',
      in: values.in,
      proto: values.proto,
      dstPort: port,
      to: values.to.trim(),
    }
  }
  return null
}

// -- DHCP pool form -----------------------------------------------------------

export interface DhcpPoolValues {
  readonly zone: string
  readonly start: string
  readonly end: string
  readonly gateway: string
  readonly dns: string
}

export type DhcpPoolErrors = Partial<Record<'zone' | 'start' | 'end' | 'gateway' | 'dns' | 'form', string>>

/**
 * Validate one DHCP pool against the context and the other already-defined
 * pools (`others`), mirroring checkDHCP: known zone, ordered range inside
 * the zone's subnets, gateway/dns addresses, non-overlapping ranges.
 */
export function validateDhcpPool(
  values: DhcpPoolValues,
  ctx: ValidationContext,
  others: readonly DhcpPool[],
): DhcpPoolErrors {
  const errors: DhcpPoolErrors = {}
  if (!ctx.zoneNames.includes(values.zone)) errors.zone = 'pick a zone'

  const start = parseIp(values.start)
  const end = parseIp(values.end)
  if (start === null) errors.start = 'must be a valid IP address'
  if (end === null) errors.end = 'must be a valid IP address'
  if (start !== null && end !== null) {
    if (start.family !== end.family) errors.form = 'range start and end must be the same address family'
    else if (start.value >= end.value) errors.form = 'range start must be below end'
  }

  const gateway = parseIp(values.gateway)
  if (gateway === null) errors.gateway = 'must be a valid IP address'
  const dns = parseIp(values.dns)
  if (dns === null) errors.dns = 'must be a valid IP address'

  if (ctx.zoneNames.includes(values.zone)) {
    const subnets = ctx.zoneSubnets[values.zone] ?? []
    if (subnets.length > 0) {
      if (start !== null && !inSubnets(start, subnets)) errors.start = `outside the subnets of zone "${values.zone}"`
      if (end !== null && !inSubnets(end, subnets)) errors.end = `outside the subnets of zone "${values.zone}"`
      if (gateway !== null && !inSubnets(gateway, subnets)) errors.gateway = `outside the subnets of zone "${values.zone}"`
    }
  }

  // Pool ranges may not overlap (validate.go compares start/end pairwise).
  if (start !== null && end !== null && start.family === end.family) {
    for (const pool of others) {
      const otherStart = parseIp(pool.range[0])
      const otherEnd = parseIp(pool.range[1])
      if (otherStart === null || otherEnd === null) continue
      if (otherStart.family !== start.family) continue
      if (start.value <= otherEnd.value && otherStart.value <= end.value) {
        errors.form = `range overlaps pool ${pool.range[0]}-${pool.range[1]}`
        break
      }
    }
  }
  return errors
}

// -- WireGuard peer form --------------------------------------------------------

export interface WgPeerValues {
  readonly name: string
  readonly publicKey: string
  readonly allowedIps: readonly string[]
}

export type WgPeerErrors = Partial<Record<'name' | 'publicKey' | 'allowedIps', string>>

/** Validate one WireGuard peer; `takenNames`/`takenKeys` exclude the peer being edited. */
export function validateWgPeer(
  values: WgPeerValues,
  takenNames: readonly string[],
  takenKeys: readonly string[],
): WgPeerErrors {
  const errors: WgPeerErrors = {}
  const name = values.name.trim()
  if (name === '') errors.name = 'name must be set'
  else if (takenNames.includes(name)) errors.name = `duplicate peer name "${name}"`

  const key = values.publicKey.trim()
  if (key === '') errors.publicKey = 'public key must be set'
  else if (!isValidWGPublicKey(key)) errors.publicKey = 'must be a base64 32-byte WireGuard public key'
  else if (takenKeys.includes(key)) errors.publicKey = 'duplicate public key'

  if (values.allowedIps.length === 0) {
    errors.allowedIps = 'at least one CIDR is required'
  } else {
    const problems = values.allowedIps
      .map((cidr, i) => (parseCidr(cidr.trim()) === null ? `entry ${i + 1} is not a valid CIDR` : null))
      .filter((problem): problem is string => problem !== null)
    if (problems.length > 0) errors.allowedIps = problems.join('; ')
  }
  return errors
}

/** WireGuard listen port: 1-65535 (validate.go checkWireGuard). */
export function validateListenPort(port: number): string | undefined {
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return 'listen port must be between 1 and 65535'
  }
  return undefined
}

/** DNS listen zones: at least one (validate.go checkServices). */
export function validateDnsListen(listen: readonly string[], zoneNames: readonly string[]): string | undefined {
  if (listen.length === 0) return 'the resolver must listen on at least one zone'
  const unknown = listen.filter((z) => !zoneNames.includes(z))
  if (unknown.length > 0) return `unknown zone "${unknown[0]}"`
  return undefined
}
