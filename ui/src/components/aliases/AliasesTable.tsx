import type { ReactElement } from 'react'
import type { Alias } from '../../api/types'
import { EmptyState } from '../ui/EmptyState'
import { Button } from '../ui/Button'

export interface AliasesTableProps {
  readonly aliases: Readonly<Record<string, Alias>>
  readonly onEdit?: (name: string) => void
  readonly onDelete?: (name: string) => void
  readonly disabled?: boolean
}

export function AliasesTable({
  aliases,
  onEdit,
  onDelete,
  disabled = false,
}: AliasesTableProps): ReactElement {
  const names = Object.keys(aliases)
  if (names.length === 0) {
    return <EmptyState title="No aliases defined" hint="Aliases group addresses for use in rules." />
  }
  return (
    <div className="axw-table-wrap">
      <table className="axw-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Entries</th>
            {(onEdit !== undefined || onDelete !== undefined) && <th aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {names.map((name) => {
            const alias = aliases[name]
            if (alias === undefined) return null
            return (
              <tr key={name}>
                <td>{name}</td>
                <td>{alias.type}</td>
                <td className="axw-mono">{alias.entries.join(', ')}</td>
                {(onEdit !== undefined || onDelete !== undefined) && (
                  <td>
                    <div className="axw-row-actions">
                      {onEdit !== undefined && (
                        <Button size="sm" variant="ghost" disabled={disabled} onClick={() => onEdit(name)}>
                          Edit
                        </Button>
                      )}
                      {onDelete !== undefined && (
                        <Button size="sm" variant="danger" disabled={disabled} onClick={() => onDelete(name)}>
                          Delete
                        </Button>
                      )}
                    </div>
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
