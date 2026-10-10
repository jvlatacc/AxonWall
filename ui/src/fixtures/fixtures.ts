// Fixture data for the mock client and stories. sampleConfig mirrors the
// canonical wave-1 document (appliance/internal/config/testdata/sample.yaml)
// one-to-one; sampleStatus is plausible runtime data for the status
// surfaces until axond exposes status endpoints.

import type { AxonWallConfig, SystemStatus } from '../api/types'

export function sampleConfig(): AxonWallConfig {
  return {
    version: 1,
    zones: {
      wan: { interfaces: ['wan0'] },
      lan: { interfaces: ['lan0'] },
      wg0: { interfaces: ['wg0'] },
    },
    interfaces: [
      { name: 'wan0', match: 'enp1s0', addressing: 'dhcp' },
      { name: 'lan0', match: 'enp2s0', addressing: 'static', address: ['192.168.1.1/24'] },
    ],
    services: {
      dns: { resolver: 'unbound', listen: ['lan', 'wg0'] },
      dhcp: {
        pools: [
          {
            zone: 'lan',
            range: ['192.168.1.100', '192.168.1.199'],
            gateway: '192.168.1.1',
            dns: '192.168.1.1',
          },
        ],
      },
      wireguard: {
        listenPort: 51820,
        peers: [
          {
            name: 'laptop',
            publicKey: 'QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=',
            allowedIps: ['10.10.0.2/32'],
          },
        ],
      },
    },
    firewall: {
      default: { input: 'drop', forward: 'drop', output: 'accept' },
      aliases: {
        'admin-hosts': { type: 'ipv4', entries: ['192.168.1.10', '192.168.1.11'] },
      },
      nat: [{ name: 'lan-masq', out: 'wan0', source: 'lan', mode: 'masquerade' }],
      rules: [
        { name: 'lan-to-wan', from: 'lan', to: 'wan', verdict: 'accept' },
        {
          name: 'admin-ssh',
          from: 'lan',
          to: 'firewall',
          service: 'ssh',
          sourceAlias: 'admin-hosts',
          verdict: 'accept',
        },
        { name: 'wg-handshake', from: 'wan', to: 'firewall', service: 'udp/51820', verdict: 'accept' },
      ],
    },
  }
}

export function sampleStatus(): SystemStatus {
  return {
    hostname: 'axonwall',
    version: '0.1.0',
    uptimeSeconds: 273600, // 3d 4h
    revision: 'rev-000042',
    interfaces: [
      {
        name: 'wan0',
        zone: 'wan',
        addressing: 'dhcp',
        addresses: ['203.0.113.7/24'],
        state: 'up',
        rxBytes: 1_874_338_201,
        txBytes: 214_502_113,
      },
      {
        name: 'lan0',
        zone: 'lan',
        addressing: 'static',
        addresses: ['192.168.1.1/24'],
        state: 'up',
        rxBytes: 2_873_114_664,
        txBytes: 6_120_883_507,
      },
      {
        name: 'wg0',
        zone: 'wg0',
        addressing: 'static',
        addresses: ['10.10.0.1/24'],
        state: 'up',
        rxBytes: 84_223_120,
        txBytes: 103_942_005,
      },
    ],
    services: [
      { name: 'nftables', state: 'running', detail: 'ruleset applied, rev-000042' },
      { name: 'unbound', state: 'running', detail: 'resolving for lan, wg0' },
      { name: 'dnsmasq', state: 'running', detail: '1 pool on lan' },
      { name: 'wireguard', state: 'running', detail: '1 peer, port 51820' },
      { name: 'axond', state: 'running', detail: 'api on :8443' },
    ],
    states: [
      {
        protocol: 'tcp',
        source: '192.168.1.10:51344',
        destination: '203.0.113.7:443',
        state: 'ESTABLISHED',
        timeoutSeconds: 7140,
      },
      {
        protocol: 'tcp',
        source: '192.168.1.24:49110',
        destination: '198.51.100.23:80',
        state: 'TIME_WAIT',
        timeoutSeconds: 42,
      },
      {
        protocol: 'udp',
        source: '10.10.0.2:51820',
        destination: '10.10.0.1:53',
        state: 'ESTABLISHED',
        timeoutSeconds: 118,
      },
      {
        protocol: 'tcp',
        source: '192.168.1.15:39211',
        destination: '192.168.1.1:22',
        state: 'ESTABLISHED',
        timeoutSeconds: 86310,
      },
    ],
    log: [
      {
        timestamp: '2026-10-09T20:41:03Z',
        severity: 'warning',
        interface: 'wan0',
        message: 'DROP wan0 tcp 203.0.113.7:22 -> 203.0.113.7:22 (port scan signature)',
      },
      {
        timestamp: '2026-10-09T20:40:11Z',
        severity: 'info',
        interface: 'wg0',
        message: 'peer laptop handshake completed from 198.51.100.9:41234',
      },
      {
        timestamp: '2026-10-09T20:39:48Z',
        severity: 'info',
        message: 'config rev-000042 committed (rule change: wg-handshake)',
      },
      {
        timestamp: '2026-10-09T20:38:59Z',
        severity: 'error',
        interface: 'wan0',
        message: 'DHCP renewal on wan0 failed, retrying in 30s (lease still valid)',
      },
    ],
  }
}
