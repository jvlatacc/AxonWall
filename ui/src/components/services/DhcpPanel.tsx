import type { ReactElement } from 'react'
import type { DhcpPool } from '../../api/types'
import { EmptyState } from '../ui/EmptyState'
import { Button } from '../ui/Button'

export interface DhcpPanelProps {
  readonly pools: readonly DhcpPool[]
  readonly onEdit?: (index: number) => void
  readonly onDelete?: (index: number) => void
  readonly onAdd?: () => void
  readonly disabled?: boolean
}

function fmtIp(ip: string | undefined): string {
  return ip === undefined ? '—' : ip.replace('/32', '')
}

export function DhcpPanel({ pools, onEdit, onDelete, onAdd, disabled = false }: DhcpPanelProps): ReactElement {
  const hasActions = onEdit !== undefined || onDelete !== undefined || onAdd !== undefined
  return (
    <div>
      {pools.length === 0 ? (
        <EmptyState title="No DHCP pools" hint="Add a pool to hand out addresses on a zone." />
      ) : (
        <div className="axw-table-wrap">
          <table className="axw-table">
            <thead>
              <tr>
                <th>Zone</th>
                <th>Range</th>
                <th>Gateway</th>
                <th>DNS</th>
                {hasActions && <th aria-label="Actions" />}
              </tr>
            </thead>
            <tbody>
              {pools.map((pool, index) => (
                <tr key={`${pool.zone}-${index}`}>
                  <td className="axw-mono">{pool.zone}</td>
                  <td className="axw-mono">
                    {fmtIp(pool.range[0])} – {fmtIp(pool.range[1])}
                  </td>
                  <td className="axw-mono">{fmtIp(pool.gateway)}</td>
                  <td className="axw-mono">{fmtIp(pool.dns)}</td>
                  {hasActions && (
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
      )}
      {onAdd !== undefined && (
        <div className="axw-form-actions">
          <Button variant="secondary" disabled={disabled} onClick={onAdd}>
            Add pool
          </Button>
        </div>
      )}
    </div>
  )
}
