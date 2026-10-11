import type { ReactElement } from 'react'
import type { FirewallRule } from '../../api/types'
import { Badge } from '../ui/Badge'
import { EmptyState } from '../ui/EmptyState'
import { Button } from '../ui/Button'

export interface RulesTableProps {
  readonly rules: readonly FirewallRule[]
  readonly onEdit?: (index: number) => void
  readonly onDelete?: (index: number) => void
  readonly disabled?: boolean
}

function verdictTone(verdict: FirewallRule['verdict']): 'success' | 'error' | 'warning' {
  if (verdict === 'accept') return 'success'
  if (verdict === 'reject') return 'warning'
  return 'error'
}

export function RulesTable({ rules, onEdit, onDelete, disabled = false }: RulesTableProps): ReactElement {
  if (rules.length === 0) {
    return <EmptyState title="No rules defined" hint="Add a rule to allow traffic between zones." />
  }
  return (
    <div className="axw-table-wrap">
      <table className="axw-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>From</th>
            <th>To</th>
            <th>Service</th>
            <th>Source alias</th>
            <th>Verdict</th>
            {(onEdit !== undefined || onDelete !== undefined) && <th aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {rules.map((rule, index) => (
            <tr key={`${rule.name}-${index}`}>
              <td>{rule.name}</td>
              <td className="axw-mono">{rule.from}</td>
              <td className="axw-mono">{rule.to}</td>
              <td className="axw-mono">
                {rule.service === undefined || rule.service === '' ? 'any' : rule.service}
              </td>
              <td className="axw-mono">
                {rule.sourceAlias === undefined || rule.sourceAlias === '' ? '—' : rule.sourceAlias}
              </td>
              <td>
                <Badge tone={verdictTone(rule.verdict)}>{rule.verdict}</Badge>
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
