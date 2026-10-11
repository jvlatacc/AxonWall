import type { ReactElement } from 'react'

export interface EmptyStateProps {
  readonly title: string
  readonly hint?: string
}

export function EmptyState({ title, hint }: EmptyStateProps): ReactElement {
  return (
    <div className="axw-empty">
      <div className="axw-empty-title">{title}</div>
      {hint !== undefined && <div>{hint}</div>}
    </div>
  )
}
