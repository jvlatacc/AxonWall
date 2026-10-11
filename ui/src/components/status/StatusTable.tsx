import type { ReactElement } from 'react'
import type { InterfaceStatus, ServiceStatus } from '../../api/types'
import { EmptyState } from '../ui/EmptyState'
import { Badge } from '../ui/Badge'
import type { BadgeTone } from '../ui/Badge'

export interface StatusTableProps {
  readonly services: readonly ServiceStatus[]
  readonly interfaces: readonly InterfaceStatus[]
}

const SERVICE_TONE: Record<ServiceStatus['state'], BadgeTone> = {
  running: 'success',
  stopped: 'warning',
  degraded: 'error',
}

const IFACE_TONE: Record<InterfaceStatus['state'], BadgeTone> = {
  up: 'success',
  down: 'error',
}

export function StatusTable({ services, interfaces }: StatusTableProps): ReactElement {
  return (
    <div>
      <h2>Services</h2>
      {services.length === 0 ? (
        <EmptyState title="No services reported" hint="Service state appears once axond reports it." />
      ) : (
        <div className="axw-table-wrap">
          <table className="axw-table">
            <thead>
              <tr>
                <th>Service</th>
                <th>State</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              {services.map((svc) => (
                <tr key={svc.name}>
                  <td>{svc.name}</td>
                  <td>
                    <Badge tone={SERVICE_TONE[svc.state]}>{svc.state}</Badge>
                  </td>
                  <td>{svc.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h2>Interfaces</h2>
      {interfaces.length === 0 ? (
        <EmptyState title="No interfaces reported" hint="Interface state appears once axond reports it." />
      ) : (
        <div className="axw-table-wrap">
          <table className="axw-table">
            <thead>
              <tr>
                <th>Interface</th>
                <th>Zone</th>
                <th>Addresses</th>
                <th>State</th>
              </tr>
            </thead>
            <tbody>
              {interfaces.map((ifc) => (
                <tr key={ifc.name}>
                  <td className="axw-mono">{ifc.name}</td>
                  <td className="axw-mono">{ifc.zone}</td>
                  <td className="axw-mono">{ifc.addresses.join(', ')}</td>
                  <td>
                    <Badge tone={IFACE_TONE[ifc.state]}>{ifc.state}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
