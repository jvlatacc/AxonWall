// Unit tests for the form validators — the client-side mirror of
// appliance/internal/config/validate.go. The appliance re-validates
// server-side; these tests pin the client behavior.
import { describe, expect, it } from 'vitest'
import { FIREWALL_ZONE } from '../api/types'
import type { AxonWallConfig } from '../api/types'
import {
  isValidConfigName,
  isValidServiceExpr,
  isValidWGPublicKey,
  validateAlias,
  validateDhcpPool,
  validateNat,
  validateRule,
  validateWgPeer,
  validationContext,
} from './validate'

const config: AxonWallConfig = {
  version: 1,
  zones: {
    wan: { interfaces: ['wan0'] },
    lan: { interfaces: ['lan0'] },
  },
  interfaces: [
    { name: 'wan0', match: 'enp1s0', addressing: 'dhcp' },
    { name: 'lan0', match: 'enp2s0', addressing: 'static', address: ['192.168.1.1/24'] },
  ],
  firewall: {
    default: { input: 'drop', forward: 'drop', output: 'accept' },
    aliases: { 'admin-hosts': { type: 'ipv4', entries: ['192.168.1.10'] } },
    nat: [],
    rules: [],
  },
}

const ctx = validationContext(config)

describe('validationContext', () => {
  it('collects zones, interfaces, aliases, and static subnets', () => {
    expect(ctx.zoneNames).toEqual(['wan', 'lan'])
    expect(ctx.interfaceNames).toEqual(['wan0', 'lan0'])
    expect(ctx.aliasNames).toEqual(['admin-hosts'])
    expect(ctx.zoneSubnets['lan']?.length).toBe(1)
    expect(ctx.zoneSubnets['wan']?.length).toBe(0)
  })
})

describe('validateRule', () => {
  const valid = {
    name: 'lan-to-firewall',
    from: 'lan',
    to: FIREWALL_ZONE,
    service: 'ssh',
    sourceAlias: 'admin-hosts',
    verdict: 'accept',
  } as const

  it('accepts a valid rule', () => {
    expect(validateRule({ ...valid }, ctx, [])).toEqual({})
  })

  it('rejects an empty name', () => {
    expect(validateRule({ ...valid, name: ' ' }, ctx, [])).toMatchObject({ name: 'name must be set' })
  })

  it('rejects an invalid name pattern', () => {
    expect(validateRule({ ...valid, name: 'Bad Name' }, ctx, [])).toMatchObject({
      name: 'must be lowercase letters, digits, dashes (max 32)',
    })
  })

  it('rejects a duplicate name', () => {
    expect(validateRule({ ...valid }, ctx, ['lan-to-firewall'])).toMatchObject({
      name: 'duplicate rule name "lan-to-firewall"',
    })
  })

  it('rejects unknown zones', () => {
    expect(validateRule({ ...valid, from: 'guest' }, ctx, [])).toMatchObject({ from: 'unknown zone "guest"' })
  })

  it('rejects a bad service expression', () => {
    expect(validateRule({ ...valid, service: 'tcp/notaport' }, ctx, [])).toMatchObject({ service: expect.any(String) })
  })

  it('accepts proto/port services', () => {
    expect(validateRule({ ...valid, service: 'udp/5353' }, ctx, [])).toEqual({})
  })

  it('rejects an unknown source alias', () => {
    expect(validateRule({ ...valid, sourceAlias: 'ghosts' }, ctx, [])).toMatchObject({
      sourceAlias: 'alias "ghosts" is not defined',
    })
  })

  it('rejects a missing verdict', () => {
    expect(validateRule({ ...valid, verdict: '' }, ctx, [])).toMatchObject({
      verdict: 'verdict must be accept, drop, or reject',
    })
  })
})

describe('validateAlias', () => {
  it('accepts a valid ipv4 alias', () => {
    expect(validateAlias({ name: 'web-hosts', type: 'ipv4', url: '', entries: ['192.168.1.5', '10.0.0.0/8'] }, [])).toEqual({})
  })

  it('rejects an empty entries list', () => {
    expect(validateAlias({ name: 'web-hosts', type: 'ipv4', url: '', entries: [] }, [])).toMatchObject({
      entries: 'at least one entry is required',
    })
  })

  it('rejects an ipv6 entry in an ipv4 alias', () => {
    expect(validateAlias({ name: 'web-hosts', type: 'ipv4', url: '', entries: ['::1'] }, [])).toMatchObject({
      entries: expect.stringContaining('not an IPv4'),
    })
  })

  it('rejects duplicate names', () => {
    expect(
      validateAlias({ name: 'admin-hosts', type: 'ipv4', url: '', entries: ['192.168.1.5'] }, ['admin-hosts']),
    ).toMatchObject({
      name: 'duplicate alias name "admin-hosts"',
    })
  })

  it('accepts a url-table alias with a feed URL and no entries', () => {
    expect(validateAlias({ name: 'blocklist', type: 'url-table', url: 'https://example.com/feed.txt', entries: [] }, [])).toEqual({})
  })

  it('rejects a url-table alias without a feed URL', () => {
    expect(validateAlias({ name: 'blocklist', type: 'url-table', url: '', entries: [] }, [])).toMatchObject({
      url: 'feed URL must be set',
    })
  })

  it('rejects a non-http feed URL', () => {
    expect(validateAlias({ name: 'blocklist', type: 'url-table', url: 'ftp://example.com/feed.txt', entries: [] }, [])).toMatchObject({
      url: 'must be an absolute http(s) URL',
    })
  })
})

describe('validateNat', () => {
  const valid = { name: 'lan-masq', out: 'wan0', source: 'lan', mode: 'masquerade', in: '', proto: '', dstPort: '', to: '' } as const

  it('accepts a valid masquerade rule', () => {
    expect(validateNat({ ...valid }, ctx, [])).toEqual({})
  })

  it('rejects an unknown outbound interface', () => {
    expect(validateNat({ ...valid, out: 'wan9' }, ctx, [])).toMatchObject({ out: 'unknown interface "wan9"' })
  })

  it('rejects an unknown source zone', () => {
    expect(validateNat({ ...valid, source: 'guest' }, ctx, [])).toMatchObject({ source: 'unknown zone "guest"' })
  })

  it('rejects a missing mode', () => {
    expect(validateNat({ ...valid, mode: '' }, ctx, [])).toMatchObject({ mode: 'mode must be masquerade or port-forward' })
  })

  it('accepts a valid port-forward rule', () => {
    expect(
      validateNat(
        { name: 'web-fwd', mode: 'port-forward', out: '', source: '', in: 'wan0', proto: 'tcp', dstPort: '8080', to: '192.168.1.50:80' },
        ctx,
        [],
      ),
    ).toEqual({})
  })

  it('rejects a port-forward with an unknown ingress interface', () => {
    expect(
      validateNat(
        { name: 'web-fwd', mode: 'port-forward', out: '', source: '', in: 'wan9', proto: 'tcp', dstPort: '8080', to: '192.168.1.50' },
        ctx,
        [],
      ),
    ).toMatchObject({ in: 'unknown interface "wan9"' })
  })

  it('rejects a port-forward with an invalid target', () => {
    expect(
      validateNat(
        { name: 'web-fwd', mode: 'port-forward', out: '', source: '', in: 'wan0', proto: 'tcp', dstPort: '8080', to: 'example.com' },
        ctx,
        [],
      ),
    ).toMatchObject({ to: 'must be an IPv4 address or ip:port' })
  })

  it('rejects a port-forward with a bare port range value', () => {
    expect(
      validateNat(
        { name: 'web-fwd', mode: 'port-forward', out: '', source: '', in: 'wan0', proto: 'udp', dstPort: '0', to: '192.168.1.50' },
        ctx,
        [],
      ),
    ).toMatchObject({ dstPort: 'must be between 1 and 65535' })
  })

  it('rejects mode-crossed field sets', () => {
    expect(validateNat({ ...valid, in: 'wan0' }, ctx, [])).toMatchObject({ form: 'port-forward fields belong to port-forward mode' })
  })
})

describe('validateDhcpPool', () => {
  const valid = { zone: 'lan', start: '192.168.1.100', end: '192.168.1.199', gateway: '192.168.1.1', dns: '192.168.1.1' }

  it('accepts a valid pool inside the zone subnets', () => {
    expect(validateDhcpPool({ ...valid }, ctx, [])).toEqual({})
  })

  it('rejects a range start above the end', () => {
    expect(validateDhcpPool({ ...valid, start: '192.168.1.200', end: '192.168.1.100' }, ctx, [])).toMatchObject({
      form: 'range start must be below end',
    })
  })

  it('rejects addresses outside the zone subnets', () => {
    expect(validateDhcpPool({ ...valid, start: '10.0.0.5' }, ctx, [])).toMatchObject({
      start: 'outside the subnets of zone "lan"',
    })
  })

  it('rejects an unknown zone', () => {
    expect(validateDhcpPool({ ...valid, zone: 'guest' }, ctx, [])).toMatchObject({ zone: 'pick a zone' })
  })

  it('rejects overlapping ranges with another pool', () => {
    const other = { zone: 'lan', range: ['192.168.1.150', '192.168.1.160'] as [string, string], gateway: '192.168.1.1', dns: '192.168.1.1' }
    expect(validateDhcpPool({ ...valid }, ctx, [other])).toMatchObject({ form: expect.stringContaining('overlaps') })
  })
})

describe('validateWgPeer', () => {
  const key = btoa('a'.repeat(32))
  const valid = { name: 'laptop', publicKey: key, allowedIps: ['10.10.0.2/32'] }

  it('accepts a valid peer', () => {
    expect(validateWgPeer({ ...valid }, [], [])).toEqual({})
  })

  it('rejects a short public key', () => {
    expect(validateWgPeer({ ...valid, publicKey: btoa('a'.repeat(16)) }, [], [])).toMatchObject({
      publicKey: 'must be a base64 32-byte WireGuard public key',
    })
  })

  it('rejects duplicate names and keys', () => {
    expect(validateWgPeer({ ...valid }, ['laptop'], [key])).toMatchObject({
      name: 'duplicate peer name "laptop"',
      publicKey: 'duplicate public key',
    })
  })

  it('rejects an empty allowedIps list', () => {
    expect(validateWgPeer({ ...valid, allowedIps: [] }, [], [])).toMatchObject({
      allowedIps: 'at least one CIDR is required',
    })
  })

  it('rejects a non-CIDR allowedIps entry', () => {
    expect(validateWgPeer({ ...valid, allowedIps: ['not-a-cidr'] }, [], [])).toMatchObject({
      allowedIps: expect.stringContaining('not a valid CIDR'),
    })
  })
})

describe('shared predicate helpers', () => {
  it('name pattern mirrors validate.go nameRE', () => {
    expect(isValidConfigName('lan-to-wan')).toBe(true)
    expect(isValidConfigName('LanToWan')).toBe(false)
    expect(isValidConfigName('9starts-with-digit')).toBe(false)
  })

  it('service expressions accept named services and proto/port', () => {
    expect(isValidServiceExpr('ssh')).toBe(true)
    expect(isValidServiceExpr('tcp/443')).toBe(true)
    expect(isValidServiceExpr('tcp/99999')).toBe(false)
    expect(isValidServiceExpr('icmp/7')).toBe(false)
  })

  it('WireGuard keys must decode to exactly 32 bytes', () => {
    expect(isValidWGPublicKey(btoa('a'.repeat(32)))).toBe(true)
    expect(isValidWGPublicKey(btoa('a'.repeat(31)))).toBe(false)
    expect(isValidWGPublicKey('!!!not base64!!!')).toBe(false)
  })
})
