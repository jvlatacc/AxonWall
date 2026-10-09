// YAML wire mapping for the backup/export surface. axond stores and serves
// the config document as canonical YAML with kebab-case field names (see
// appliance/internal/config); this module is the only place in the UI that
// speaks that shape. Parsing here is a *mapping* step, not the appliance's
// validator: semantic checks stay with axond, and a malformed import is
// rejected with a readable error before it can reach the store.

import { dump, load } from 'js-yaml'
import type {
  Addressing,
  AliasType,
  AxonWallConfig,
  DefaultPolicies,
  DhcpService,
  DnsService,
  FirewallRule,
  InterfaceConfig,
  NatRule,
  Services,
  WireGuardService,
} from './types'

// -- serialization (domain -> yaml) ----------------------------------------

interface YamlDoc {
  version: number
  zones: Record<string, { interfaces: string[] }>
  interfaces: {
    name: string
    match: string
    addressing: Addressing
    address?: string[]
  }[]
  services?: {
    dns?: {
      resolver: string
      listen: string[]
      mode?: string
      forwarders?: string[]
    }
    dhcp?: {
      pools: { zone: string; range: [string, string]; gateway: string; dns: string }[]
    }
    wireguard?: {
      'listen-port': number
      peers: { name: string; 'public-key': string; 'allowed-ips': string[] }[]
    }
  }
  firewall: {
    default: { input: string; forward: string; output: string }
    aliases: Record<string, { type: string; url?: string; entries: string[] }>
    nat: (MasqueradeYaml | PortForwardYaml)[]
    rules: {
      name: string
      from: string
      to: string
      service?: string
      'source-alias'?: string
      verdict: string
    }[]
  }
}

interface MasqueradeYaml {
  name: string
  mode: 'masquerade'
  out: string
  source: string
}

interface PortForwardYaml {
  name: string
  mode: 'port-forward'
  in: string
  proto: string
  'dst-port': number
  to: string
}

export function configToYaml(config: AxonWallConfig): string {
  const doc: YamlDoc = {
    version: config.version,
    zones: mapValues(config.zones, (z) => ({ interfaces: [...z.interfaces] })),
    interfaces: config.interfaces.map((ifc) => ({
      name: ifc.name,
      match: ifc.match,
      addressing: ifc.addressing,
      ...(ifc.address !== undefined && ifc.address.length > 0 ? { address: [...ifc.address] } : {}),
    })),
    firewall: {
      default: { ...config.firewall.default },
      aliases: mapValues(config.firewall.aliases, (a) => ({
        type: a.type,
        ...(a.url !== undefined && a.url !== '' ? { url: a.url } : {}),
        entries: [...a.entries],
      })),
      nat: config.firewall.nat.map((n) =>
        n.mode === 'masquerade'
          ? { name: n.name, mode: n.mode, out: n.out, source: n.source }
          : {
              name: n.name,
              mode: n.mode,
              in: n.in,
              proto: n.proto,
              'dst-port': n.dstPort,
              to: n.to,
            },
      ),
      rules: config.firewall.rules.map((r) => ({
        name: r.name,
        from: r.from,
        to: r.to,
        ...(r.service !== undefined && r.service !== '' ? { service: r.service } : {}),
        ...(r.sourceAlias !== undefined && r.sourceAlias !== ''
          ? { 'source-alias': r.sourceAlias }
          : {}),
        verdict: r.verdict,
      })),
    },
  }
  if (config.services?.dns !== undefined) {
    const dns = config.services.dns
    doc.services = {
      ...doc.services,
      dns: {
        resolver: dns.resolver,
        listen: [...dns.listen],
        ...(dns.mode !== undefined ? { mode: dns.mode } : {}),
        ...(dns.forwarders !== undefined && dns.forwarders.length > 0
          ? { forwarders: [...dns.forwarders] }
          : {}),
      },
    }
  }
  if (config.services?.dhcp !== undefined) {
    doc.services = {
      ...doc.services,
      dhcp: {
        pools: config.services.dhcp.pools.map((p) => ({
          zone: p.zone,
          range: [p.range[0], p.range[1]],
          gateway: p.gateway,
          dns: p.dns,
        })),
      },
    }
  }
  if (config.services?.wireguard !== undefined) {
    doc.services = {
      ...doc.services,
      wireguard: {
        'listen-port': config.services.wireguard.listenPort,
        peers: config.services.wireguard.peers.map((p) => ({
          name: p.name,
          'public-key': p.publicKey,
          'allowed-ips': [...p.allowedIps],
        })),
      },
    }
  }
  return dump(doc, { lineWidth: 120 })
}

// -- parsing (yaml -> domain) --------------------------------------------------

/**
 * Parse an exported config document. Throws Error with a readable message on
 * malformed input; deeper schema/semantic problems are the appliance's to
 * reject on save (the store rolls the change back and surfaces the error).
 */
export function configFromYaml(text: string): AxonWallConfig {
  let raw: unknown
  try {
    raw = load(text)
  } catch (err) {
    throw new Error(`not a valid YAML document: ${err instanceof Error ? err.message : String(err)}`)
  }
  const doc = asRecord(raw, 'document')
  const version = doc['version']
  if (version !== 1) throw new Error(`unsupported schema version ${String(version)} (supported: 1)`)

  const zonesRaw = asRecord(doc['zones'] ?? {}, 'zones')
  const zones: Record<string, { interfaces: string[] }> = {}
  for (const [name, value] of Object.entries(zonesRaw)) {
    const zone = asRecord(value, `zones.${name}`)
    zones[name] = { interfaces: stringList(zone['interfaces'], `zones.${name}.interfaces`) }
  }

  const interfacesRaw = Array.isArray(doc['interfaces']) ? doc['interfaces'] : []
  const interfaces: InterfaceConfig[] = interfacesRaw.map((entry, i) => {
    const ifc = asRecord(entry, `interfaces[${i}]`)
    const addressing = ifc['addressing']
    if (addressing !== 'dhcp' && addressing !== 'static') {
      throw new Error(`interfaces[${i}]: addressing must be dhcp or static`)
    }
    const addresses = ifc['address'] === undefined ? undefined : stringList(ifc['address'], `interfaces[${i}].address`)
    return {
      name: expectString(ifc['name'], `interfaces[${i}].name`),
      match: expectString(ifc['match'], `interfaces[${i}].match`),
      addressing: addressing as Addressing,
      ...(addresses !== undefined ? { address: addresses } : {}),
    }
  })

  const firewallRaw = asRecord(doc['firewall'] ?? {}, 'firewall')
  const aliasesRaw = asRecord(firewallRaw['aliases'] ?? {}, 'firewall.aliases')
  const aliases: Record<string, { type: AliasType; url?: string; entries: string[] }> = {}
  for (const [name, value] of Object.entries(aliasesRaw)) {
    const alias = asRecord(value, `firewall.aliases.${name}`)
    const type = alias['type']
    if (type !== 'ipv4' && type !== 'ipv6' && type !== 'url-table') {
      throw new Error(`firewall.aliases.${name}: type must be ipv4, ipv6, or url-table`)
    }
    const url = alias['url'] === undefined ? undefined : expectString(alias['url'], `firewall.aliases.${name}.url`)
    aliases[name] = {
      type: type as AliasType,
      ...(url !== undefined ? { url } : {}),
      entries: stringList(alias['entries'], `firewall.aliases.${name}.entries`),
    }
  }

  return {
    version: 1,
    zones,
    interfaces,
    services: servicesFromYaml(doc['services']),
    firewall: {
      default: defaultsFromYaml(firewallRaw['default']),
      aliases,
      nat: natFromYaml(firewallRaw['nat']),
      rules: rulesFromYaml(firewallRaw['rules']),
    },
  }
}

function servicesFromYaml(raw: unknown): Services | undefined {
  if (raw === undefined || raw === null) return undefined
  const s = asRecord(raw, 'services')
  const dns = dnsFromYaml(s['dns'])
  const dhcp = dhcpFromYaml(s['dhcp'])
  const wireguard = wireguardFromYaml(s['wireguard'])
  if (dns === undefined && dhcp === undefined && wireguard === undefined) return undefined
  return {
    ...(dns === undefined ? {} : { dns }),
    ...(dhcp === undefined ? {} : { dhcp }),
    ...(wireguard === undefined ? {} : { wireguard }),
  }
}

function dnsFromYaml(raw: unknown): DnsService | undefined {
  if (raw === undefined || raw === null) return undefined
  const d = asRecord(raw, 'services.dns')
  const mode = d['mode']
  if (mode !== undefined && mode !== 'recursive' && mode !== 'forward') {
    throw new Error('services.dns.mode: must be recursive or forward')
  }
  const forwarders = d['forwarders'] === undefined ? undefined : stringList(d['forwarders'], 'services.dns.forwarders')
  return {
    resolver: 'unbound',
    listen: stringList(d['listen'], 'services.dns.listen'),
    ...(mode !== undefined ? { mode } : {}),
    ...(forwarders !== undefined ? { forwarders } : {}),
  }
}

function dhcpFromYaml(raw: unknown): DhcpService | undefined {
  if (raw === undefined || raw === null) return undefined
  const d = asRecord(raw, 'services.dhcp')
  const pools = Array.isArray(d['pools']) ? d['pools'] : []
  return {
    pools: pools.map((entry, i) => {
      const p = asRecord(entry, `services.dhcp.pools[${i}]`)
      const range = p['range']
      if (!Array.isArray(range) || range.length !== 2) {
        throw new Error(`services.dhcp.pools[${i}].range must be [start, end]`)
      }
      return {
        zone: expectString(p['zone'], `services.dhcp.pools[${i}].zone`),
        range: [expectString(range[0], 'range[0]'), expectString(range[1], 'range[1]')],
        gateway: expectString(p['gateway'], `services.dhcp.pools[${i}].gateway`),
        dns: expectString(p['dns'], `services.dhcp.pools[${i}].dns`),
      }
    }),
  }
}

function wireguardFromYaml(raw: unknown): WireGuardService | undefined {
  if (raw === undefined || raw === null) return undefined
  const w = asRecord(raw, 'services.wireguard')
  const peers = Array.isArray(w['peers']) ? w['peers'] : []
  return {
    listenPort: expectNumber(w['listen-port'], 'services.wireguard.listen-port'),
    peers: peers.map((entry, i) => {
      const p = asRecord(entry, `services.wireguard.peers[${i}]`)
      return {
        name: expectString(p['name'], `services.wireguard.peers[${i}].name`),
        publicKey: expectString(p['public-key'], `services.wireguard.peers[${i}].public-key`),
        allowedIps: stringList(p['allowed-ips'], `services.wireguard.peers[${i}].allowed-ips`),
      }
    }),
  }
}

function defaultsFromYaml(raw: unknown): DefaultPolicies {
  const d = asRecord(raw ?? {}, 'firewall.default')
  const isPolicy = (v: unknown): v is 'accept' | 'drop' => v === 'accept' || v === 'drop'
  if (!isPolicy(d['input']) || !isPolicy(d['forward']) || !isPolicy(d['output'])) {
    throw new Error('firewall.default: input/forward/output must be accept or drop')
  }
  return { input: d['input'], forward: d['forward'], output: d['output'] }
}

function natFromYaml(raw: unknown): NatRule[] {
  if (!Array.isArray(raw)) return []
  return raw.map((entry, i) => {
    const n = asRecord(entry, `firewall.nat[${i}]`)
    const field = `firewall.nat[${i}]`
    switch (n['mode']) {
      case 'masquerade':
        return {
          name: expectString(n['name'], `${field}.name`),
          mode: 'masquerade' as const,
          out: expectString(n['out'], `${field}.out`),
          source: expectString(n['source'], `${field}.source`),
        }
      case 'port-forward': {
        const proto = n['proto']
        if (proto !== 'tcp' && proto !== 'udp') {
          throw new Error(`${field}.proto: must be tcp or udp`)
        }
        return {
          name: expectString(n['name'], `${field}.name`),
          mode: 'port-forward' as const,
          in: expectString(n['in'], `${field}.in`),
          proto,
          dstPort: expectNumber(n['dst-port'], `${field}.dst-port`),
          to: expectString(n['to'], `${field}.to`),
        }
      }
      default:
        throw new Error(`${field}.mode: must be masquerade or port-forward`)
    }
  })
}

function rulesFromYaml(raw: unknown): FirewallRule[] {
  if (!Array.isArray(raw)) return []
  return raw.map((entry, i) => {
    const r = asRecord(entry, `firewall.rules[${i}]`)
    const service = r['service']
    const sourceAlias = r['source-alias']
    return {
      name: expectString(r['name'], `firewall.rules[${i}].name`),
      from: expectString(r['from'], `firewall.rules[${i}].from`),
      to: expectString(r['to'], `firewall.rules[${i}].to`),
      ...(service !== undefined ? { service: expectString(service, `firewall.rules[${i}].service`) } : {}),
      ...(sourceAlias !== undefined
        ? { sourceAlias: expectString(sourceAlias, `firewall.rules[${i}].source-alias`) }
        : {}),
      verdict: expectVerdict(r['verdict'], `firewall.rules[${i}].verdict`),
    }
  })
}

function expectVerdict(value: unknown, field: string): 'accept' | 'drop' | 'reject' {
  if (value === 'accept' || value === 'drop' || value === 'reject') return value
  throw new Error(`${field} must be accept, drop, or reject`)
}

// -- shape helpers --------------------------------------------------------

function asRecord(value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be a mapping`)
  }
  return value as Record<string, unknown>
}

function expectString(value: unknown, field: string): string {
  if (typeof value !== 'string' || value === '') throw new Error(`${field} must be a non-empty string`)
  return value
}

function expectNumber(value: unknown, field: string): number {
  if (typeof value !== 'number' || Number.isNaN(value)) throw new Error(`${field} must be a number`)
  return value
}

function stringList(value: unknown, field: string): string[] {
  if (!Array.isArray(value)) throw new Error(`${field} must be a list`)
  return value.map((entry, i) => expectString(entry, `${field}[${i}]`))
}

function mapValues<V, R>(record: Readonly<Record<string, V>>, fn: (value: V) => R): Record<string, R> {
  const out: Record<string, R> = {}
  for (const [key, value] of Object.entries(record)) out[key] = fn(value)
  return out
}
