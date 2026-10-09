import type { ReactElement } from 'react'
import type { NatRule } from '../../api/types'
import { Badge } from '../ui/Badge'
import { EmptyState } from '../ui/EmptyState'
import { Button } from '../ui/Button'

export interface NatTableProps {
  readonly rules: readonly NatRule[]
  readonly onEdit?: (index: number) => void
  readonly onDelete?: (index: number) => void
  readonly disabled?: boolean
}

export function NatTable({ rules, onEdit, onDelete, disabled = false }: NatTableProps): ReactElement {
  if (rules.length === 0) {
    return (
      <EmptyState
        title="No NAT rules defined"
        hint="Add a masquerade rule for a zone to reach the internet."
      />
    )
  }
  return (
    <div className="axw-table-wrap">
      <table className="axw-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Out interface</th>
            <th>Source zone</th>
            <th>Mode</th>
            {(onEdit !== undefined || onDelete !== undefined) && <th aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {rules.map((rule, index) => (
            <tr key={`${rule.name}-${index}`}>
              <td>{rule.name}</td>
              <td className="axw-mono">{rule.out}</td>
              <td className="axw-mono">{rule.source}</td>
              <td>
                <Badge tone="accent">{rule.mode}</Badge>
              </td>
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
