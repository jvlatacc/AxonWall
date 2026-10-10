// Domain types mirroring the wave-1 configuration schema in
// appliance/internal/config/config.go. YAML wire names (kebab-case) are
// noted per field; the client layer owns the YAML <-> domain mapping, so
// components only ever see these types.
//
// Every surface is a render target of this shape: a rule table, a DHCP
// panel, a backup export — all read or produce pieces of AxonWallConfig.

/** Chain policy for base chains (nftables policies are accept or drop only). */
export type PolicyAction = 'accept' | 'drop'

/** Rule verdicts additionally allow reject. */
export type Verdict = PolicyAction | 'reject'

export type Addressing = 'dhcp' | 'static'

/** Alias kinds; url-table names a feed URL the appliance refreshes. */
export type AliasType = 'ipv4' | 'ipv6' | 'url-table'

/** DNS resolver mode: full recursion or forwarding to upstream resolvers. */
export type DnsMode = 'recursive' | 'forward'

/** Service schema version this UI understands (config.go: Version). */
export const SCHEMA_VERSION = 1

/** Reserved pseudo-zone for traffic to and from the appliance itself. */
export const FIREWALL_ZONE = 'firewall'

/** Interface name materialized by the WireGuard service (config.go). */
export const WIREGUARD_INTERFACE = 'wg0'

// -- Zones and interfaces ------------------------------------------------

/** Zone groups interfaces for rule addressing (yaml: zones.<name>). */
export interface Zone {
  readonly interfaces: readonly string[]
}

/**
 * Logical interface (yaml: interfaces[]). `name` is the appliance-logical
 * name (wan0, lan0); `match` is the kernel device it binds to (enp1s0).
 */
export interface InterfaceConfig {
  readonly name: string
  readonly match: string
  readonly addressing: Addressing
  /** CIDRs; static only (yaml: address). */
  readonly address?: readonly string[]
}

// -- Services ------------------------------------------------------------

export interface Services {
  /** Section presence matters; an absent section means the service is off. */
  readonly dns?: DnsService
  readonly dhcp?: DhcpService
  readonly wireguard?: WireGuardService
}

/** Unbound resolver and the zones it listens on (yaml: services.dns). */
export interface DnsService {
  readonly resolver: 'unbound'
  readonly listen: readonly string[]
  /** Defaults to recursive when absent (yaml: mode). */
  readonly mode?: DnsMode
  /** Upstream resolver IPs; forward mode only (yaml: forwarders). */
  readonly forwarders?: readonly string[]
}

/** DHCP pools (yaml: services.dhcp). */
export interface DhcpService {
  readonly pools: readonly DhcpPool[]
}

export interface DhcpPool {
  readonly zone: string
  /** Inclusive [start, end] address range (yaml: range). */
  readonly range: readonly [string, string]
  readonly gateway: string
  readonly dns: string
}

/** The single wave-1 WireGuard interface (yaml: services.wireguard). */
export interface WireGuardService {
  readonly listenPort: number
  readonly peers: readonly WgPeer[]
}

export interface WgPeer {
  readonly name: string
  /** Base64 public key, 32 bytes (yaml: public-key). */
  readonly publicKey: string
  readonly allowedIps: readonly string[]
}

// -- Firewall ------------------------------------------------------------

export interface Firewall {
  readonly default: DefaultPolicies
  /** Named address sets (yaml: firewall.aliases). */
  readonly aliases: Readonly<Record<string, Alias>>
  readonly nat: readonly NatRule[]
  readonly rules: readonly FirewallRule[]
}

export interface DefaultPolicies {
  readonly input: PolicyAction
  readonly forward: PolicyAction
  readonly output: PolicyAction
}

export interface Alias {
  readonly type: AliasType
  /** Feed source, url-table only (yaml: url). */
  readonly url?: string
  readonly entries: readonly string[]
}

/** Source-NAT rule (yaml: firewall.nat) — masquerade mode. */
export interface MasqueradeRule {
  readonly name: string
  readonly mode: 'masquerade'
  /** Logical egress interface name (yaml: out). */
  readonly out: string
  /** Source zone (yaml: source). */
  readonly source: string
}

/** DNAT rule forwarding a public port to an internal host — port-forward mode. */
export interface PortForwardRule {
  readonly name: string
  readonly mode: 'port-forward'
  /** Logical ingress interface name (yaml: in). */
  readonly in: string
  readonly proto: 'tcp' | 'udp'
  /** Public port, 1-65535 (yaml: dst-port). */
  readonly dstPort: number
  /** Internal `ip` or `ip:port` (yaml: to). */
  readonly to: string
}

/** NAT rule; the two modes have disjoint fields (config.go: NATRule). */
export type NatRule = MasqueradeRule | PortForwardRule

/** Firewall rule between zones, or the `firewall` pseudo-zone (yaml: firewall.rules). */
export interface FirewallRule {
  readonly name: string
  readonly from: string
  readonly to: string
  /** Named service or `proto/port` (e.g. `tcp/443`); empty = any (yaml: service). */
  readonly service?: string
  /** Optional alias refining `from` (yaml: source-alias). */
  readonly sourceAlias?: string
  readonly verdict: Verdict
}

// -- Root document ---------------------------------------------------------

/** The wave-1 configuration document (config.go: Config). */
export interface AxonWallConfig {
  readonly version: typeof SCHEMA_VERSION
  readonly zones: Readonly<Record<string, Zone>>
  readonly interfaces: readonly InterfaceConfig[]
  readonly services?: Services
  readonly firewall: Firewall
}

/** Config plus the store revision it was read at (X-AxonWall-Revision). */
export interface ConfigRevision {
  readonly config: AxonWallConfig
  readonly revision: string
}

// -- Runtime status (UI contract; axond status endpoints land later) -------

export type InterfaceState = 'up' | 'down'
export type ServiceHealth = 'running' | 'stopped' | 'degraded'
export type ConnectionState = 'ESTABLISHED' | 'NEW' | 'TIME_WAIT'
export type LogSeverity = 'info' | 'warning' | 'error'

export interface InterfaceStatus {
  readonly name: string
  readonly zone: string
  readonly addressing: Addressing
  readonly addresses: readonly string[]
  readonly state: InterfaceState
  readonly rxBytes: number
  readonly txBytes: number
}

export interface ServiceStatus {
  readonly name: string
  readonly state: ServiceHealth
  readonly detail: string
}

export interface FirewallStateEntry {
  readonly protocol: 'tcp' | 'udp' | 'icmp'
  readonly source: string
  readonly destination: string
  readonly state: ConnectionState
  readonly timeoutSeconds: number
}

export interface LogEntry {
  /** ISO 8601 timestamp. */
  readonly timestamp: string
  readonly severity: LogSeverity
  readonly interface?: string
  readonly message: string
}

export interface SystemStatus {
  readonly hostname: string
  readonly version: string
  readonly uptimeSeconds: number
  readonly revision: string
  readonly interfaces: readonly InterfaceStatus[]
  readonly services: readonly ServiceStatus[]
  readonly states: readonly FirewallStateEntry[]
  readonly log: readonly LogEntry[]
}

/** Kernel network device offered to the first-boot wizard (GET /net/devices). */
export interface NetDevice {
  readonly name: string
  readonly mac: string
  readonly up: boolean
}

/** First-boot setup marker (GET /setup, POST /setup/complete). */
export interface SetupState {
  readonly required: boolean
}
