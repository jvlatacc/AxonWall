// Pure, immutable config transformations. Pages compose these to build the
// next config document from the current one; nothing mutates in place —
// the store is the single source of truth and every change is a new
// document handed to ConfigStore.save.

import type {
  Alias,
  AxonWallConfig,
  DefaultPolicies,
  DhcpPool,
  DnsService,
  FirewallRule,
  NatRule,
  WireGuardService,
} from '../api/types'

export function setRules(config: AxonWallConfig, rules: readonly FirewallRule[]): AxonWallConfig {
  return { ...config, firewall: { ...config.firewall, rules } }
}

/** Replace the rule at `index`, or append when `index` is null. */
export function setRule(
  config: AxonWallConfig,
  index: number | null,
  rule: FirewallRule,
): AxonWallConfig {
  const rules = [...config.firewall.rules]
  if (index === null) rules.push(rule)
  else if (index >= 0 && index < rules.length) rules[index] = rule
  return setRules(config, rules)
}

export function removeRule(config: AxonWallConfig, index: number): AxonWallConfig {
  return setRules(
    config,
    config.firewall.rules.filter((_, i) => i !== index),
  )
}

export function setDefaultPolicies(config: AxonWallConfig, next: DefaultPolicies): AxonWallConfig {
  return { ...config, firewall: { ...config.firewall, default: next } }
}

export function setAliases(
  config: AxonWallConfig,
  aliases: Readonly<Record<string, Alias>>,
): AxonWallConfig {
  return { ...config, firewall: { ...config.firewall, aliases } }
}

/**
 * Insert or rename an alias. Renames (previousName !== name) drop the
 * old key so renaming never leaves the old set behind.
 */
export function upsertAlias(
  config: AxonWallConfig,
  previousName: string | undefined,
  name: string,
  alias: Alias,
): AxonWallConfig {
  const aliases = { ...config.firewall.aliases }
  if (previousName !== undefined && previousName !== name) delete aliases[previousName]
  aliases[name] = alias
  return setAliases(config, aliases)
}

export function removeAlias(config: AxonWallConfig, name: string): AxonWallConfig {
  return setAliases(config, withoutKey(config.firewall.aliases, name))
}

export function setNat(config: AxonWallConfig, nat: readonly NatRule[]): AxonWallConfig {
  return { ...config, firewall: { ...config.firewall, nat } }
}

/** Replace the NAT rule at `index`, or append when `index` is null. */
export function setNatRule(
  config: AxonWallConfig,
  index: number | null,
  rule: NatRule,
): AxonWallConfig {
  const nat = [...config.firewall.nat]
  if (index === null) nat.push(rule)
  else if (index >= 0 && index < nat.length) nat[index] = rule
  return setNat(config, nat)
}

export function removeNatRule(config: AxonWallConfig, index: number): AxonWallConfig {
  return setNat(
    config,
    config.firewall.nat.filter((_, i) => i !== index),
  )
}

export function setDns(config: AxonWallConfig, dns: DnsService | undefined): AxonWallConfig {
  return { ...config, services: { ...config.services, dns } }
}

export function setDhcpPools(config: AxonWallConfig, pools: readonly DhcpPool[]): AxonWallConfig {
  return {
    ...config,
    services: { ...config.services, dhcp: { pools } },
  }
}

/** Replace the pool at `index`, or append when `index` is null. */
export function setDhcpPool(
  config: AxonWallConfig,
  index: number | null,
  pool: DhcpPool,
): AxonWallConfig {
  const pools = [...(config.services?.dhcp?.pools ?? [])]
  if (index === null) pools.push(pool)
  else if (index >= 0 && index < pools.length) pools[index] = pool
  return setDhcpPools(config, pools)
}

export function removeDhcpPool(config: AxonWallConfig, index: number): AxonWallConfig {
  return setDhcpPools(
    config,
    (config.services?.dhcp?.pools ?? []).filter((_, i) => i !== index),
  )
}

export function setWireGuard(
  config: AxonWallConfig,
  wireguard: WireGuardService | undefined,
): AxonWallConfig {
  return { ...config, services: { ...config.services, wireguard } }
}

function withoutKey<V>(
  record: Readonly<Record<string, V>>,
  key: string,
): Record<string, V> {
  const next: Record<string, V> = { ...record }
  delete next[key]
  return next
}
