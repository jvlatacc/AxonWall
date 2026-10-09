import type { ReactElement } from 'react'
import type { AxonWallConfig, SystemStatus } from '../api/types'
import { Badge } from '../components/ui/Badge'
import { EmptyState } from '../components/ui/EmptyState'

export interface DashboardPageProps {
  readonly config: AxonWallConfig
  readonly status: SystemStatus | undefined
}

function ifaceCount(config: AxonWallConfig): number {
  return config.interfaces.length
}

function peerCount(config: AxonWallConfig): number {
  return config.services?.wireguard === undefined ? 0 : config.services.wireguard.peers.length
}

function poolCount(config: AxonWallConfig): number {
  return config.services?.dhcp === undefined ? 0 : config.services.dhcp.pools.length
}

export function DashboardPage({ config, status }: DashboardPageProps): ReactElement {
  const policy = config.firewall.default
  return (
    <div>
      <h1 className="axw-page-title">Dashboard</h1>
      <div className="axw-stat-grid">
        <div className="axw-stat-card">
          <div className="axw-stat-label">Interfaces</div>
          <div className="axw-stat-value">{ifaceCount(config)}</div>
        </div>
        <div className="axw-stat-card">
          <div className="axw-stat-label">Firewall rules</div>
          <div className="axw-stat-value">{config.firewall.rules.length}</div>
        </div>
        <div className="axw-stat-card">
          <div className="axw-stat-label">NAT rules</div>
          <div className="axw-stat-value">{config.firewall.nat.length}</div>
        </div>
        <div className="axw-stat-card">
          <div className="axw-stat-label">Aliases</div>
          <div className="axw-stat-value">{Object.keys(config.firewall.aliases).length}</div>
        </div>
        <div className="axw-stat-card">
          <div className="axw-stat-label">DHCP pools</div>
          <div className="axw-stat-value">{poolCount(config)}</div>
        </div>
        <div className="axw-stat-card">
          <div className="axw-stat-label">WireGuard peers</div>
          <div className="axw-stat-value">{peerCount(config)}</div>
        </div>
      </div>

      <div className="axw-status-grid">
        <section>
          <h3 className="axw-sub-heading">Zones</h3>
          <div className="axw-table-wrap">
            <table className="axw-table">
              <thead>
                <tr>
                  <th>Zone</th>
                  <th>Interfaces</th>
                  <th>Role</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(config.zones).map(([name, zone]) => (
                  <tr key={name}>
                    <td className="axw-mono">{name}</td>
                    <td className="axw-mono">{zone.interfaces.join(', ')}</td>
                    <td>
                      {name === 'wan' ? (
                        <Badge tone="warning">wan</Badge>
                      ) : (
                        <Badge tone="neutral">{name === 'lan' ? 'lan' : '—'}</Badge>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="axw-hint">
            Default policies: input <span className="axw-mono">{policy.input}</span>, forward{' '}
            <span className="axw-mono">{policy.forward}</span>, output{' '}
            <span className="axw-mono">{policy.output}</span>
          </p>
        </section>
        <section>
          <h3 className="axw-sub-heading">System</h3>
          {status === undefined ? (
            <EmptyState title="Status unavailable" hint="System status appears when the appliance reports it." />
          ) : (
            <div className="axw-table-wrap">
              <table className="axw-table">
                <tbody>
                  <tr>
                    <td>Appliance</td>
                    <td>{status.version}</td>
                  </tr>
                  <tr>
                    <td>Hostname</td>
                    <td>{status.hostname}</td>
                  </tr>
                  <tr>
                    <td>Uptime</td>
                    <td>{status.uptimeSeconds}s</td>
                  </tr>
                  <tr>
                    <td>Configuration</td>
                    <td className="axw-mono">{status.revision}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
