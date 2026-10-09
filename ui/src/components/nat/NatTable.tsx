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

// Summary of one NAT rule for the table: what it matches and where it sends
// traffic, per mode.
function ruleDetail(rule: NatRule): string {
  if (rule.mode === 'masquerade') {
    return `${rule.source} → ${rule.out}`
  }
  const target = rule.to.includes(':') ? rule.to : `${rule.to}:${rule.dstPort}`
  return `${rule.in} :${rule.dstPort}/${rule.proto} → ${target}`
}

function ruleDetailHeader(rule: NatRule): string {
  return rule.mode === 'masquerade' ? 'Source zone → out interface' : 'Ingress interface, public port → internal target'
}

export function NatTable({ rules, onEdit, onDelete, disabled = false }: NatTableProps): ReactElement {
  if (rules.length === 0) {
    return (
      <EmptyState
        title="No NAT rules defined"
        hint="Add a masquerade rule for a zone to reach the internet, or a port-forward to expose a service."
      />
    )
  }
  const showActions = onEdit !== undefined || onDelete !== undefined
  return (
    <div className="axw-table-wrap">
      <table className="axw-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Mode</th>
            <th>Match → target</th>
            {showActions && <th aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {rules.map((rule, index) => (
            <tr key={`${rule.name}-${index}`}>
              <td>{rule.name}</td>
              <td>
                <Badge tone={rule.mode === 'masquerade' ? 'accent' : 'neutral'}>{rule.mode}</Badge>
              </td>
              <td className="axw-mono" title={ruleDetailHeader(rule)}>
                {ruleDetail(rule)}
              </td>
              {showActions && (
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
