import type { ReactElement } from 'react'
import type { WgPeer } from '../../api/types'
import { EmptyState } from '../ui/EmptyState'
import { Button } from '../ui/Button'

export interface WireGuardPeersTableProps {
  readonly peers: readonly WgPeer[]
  readonly onEdit?: (index: number) => void
  readonly onDelete?: (index: number) => void
  readonly disabled?: boolean
}

export function WireGuardPeersTable({
  peers,
  onEdit,
  onDelete,
  disabled = false,
}: WireGuardPeersTableProps): ReactElement {
  if (peers.length === 0) {
    return <EmptyState title="No WireGuard peers" hint="Add a peer to grant VPN access." />
  }
  return (
    <div className="axw-table-wrap">
      <table className="axw-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Public key</th>
            <th>Allowed IPs</th>
            {(onEdit !== undefined || onDelete !== undefined) && <th aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {peers.map((peer, index) => (
            <tr key={`${peer.name}-${index}`}>
              <td>{peer.name}</td>
              <td className="axw-mono">{peer.publicKey.slice(0, 12)}…</td>
              <td className="axw-mono">{peer.allowedIps.join(', ')}</td>
              {(onEdit !== undefined || onDelete !== undefined) && (
                <td>
                  <div className="axw-row-actions">
                    {onEdit !== undefined && (
                      <Button size="sm" variant="ghost" disabled={disabled} onClick={() => onEdit(index)}>
                        Edit
                      </Button>
                    )}
                    {onDelete !== undefined && (
                      <Button size="sm" variant="danger" disabled={disabled} onClick={() => onDelete(index)}>
                        Delete
                      </Button>
                    )}
                  </div>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
