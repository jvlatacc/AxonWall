import { describe, expect, it } from 'vitest'
import { configToYaml, configFromYaml } from './yaml'
import type { AxonWallConfig } from './types'
import { sampleConfig } from '../fixtures/fixtures'

// The canonical wave-1 document, extended with every optional field the UI
// can produce: forwarded DNS, a url-table alias, and a port-forward NAT rule.
function extendedConfig(): AxonWallConfig {
  const base = sampleConfig()
  return {
    ...base,
    services: {
      ...base.services,
      dns: { resolver: 'unbound', listen: ['lan', 'wg0'], mode: 'forward', forwarders: ['1.1.1.1', '8.8.8.8'] },
    },
    firewall: {
      ...base.firewall,
      aliases: {
        ...base.firewall.aliases,
        blocklist: { type: 'url-table', url: 'https://example.com/feed.txt', entries: [] },
      },
      nat: [
        ...base.firewall.nat,
        { name: 'web-fwd', mode: 'port-forward', in: 'wan0', proto: 'tcp', dstPort: 8080, to: '192.168.1.50:80' },
      ],
    },
  }
}

describe('yaml round-trip', () => {
  it('round-trips the canonical sample config', () => {
    const config = sampleConfig()
    expect(configFromYaml(configToYaml(config))).toEqual(config)
  })

  it('round-trips every extended field', () => {
    const config = extendedConfig()
    expect(configFromYaml(configToYaml(config))).toEqual(config)
  })

  it('omits DNS mode and forwarders when unset', () => {
    const yaml = configToYaml(sampleConfig())
    const dnsBlock = yaml.slice(yaml.indexOf('dns:'), yaml.indexOf('dhcp:'))
    expect(dnsBlock).not.toContain('mode')
    expect(dnsBlock).not.toContain('forwarders')
  })

  it('omits alias url for non-feed aliases', () => {
    const yaml = configToYaml(sampleConfig())
    expect(yaml).not.toContain('url:')
  })
})

describe('configFromYaml rejects documents the appliance would refuse', () => {
  it('rejects an unknown NAT mode', () => {
    const doc = configToYaml(extendedConfig()).replace('mode: port-forward', 'mode: redirect')
    expect(() => configFromYaml(doc)).toThrow('must be masquerade or port-forward')
  })

  it('rejects an unknown alias type', () => {
    const doc = configToYaml(extendedConfig()).replace('type: url-table', 'type: geoip')
    expect(() => configFromYaml(doc)).toThrow('type must be ipv4, ipv6, or url-table')
  })

  it('rejects an unknown DNS mode', () => {
    const doc = configToYaml(extendedConfig()).replace('mode: forward', 'mode: hybrid')
    expect(() => configFromYaml(doc)).toThrow('services.dns.mode: must be recursive or forward')
  })
})
