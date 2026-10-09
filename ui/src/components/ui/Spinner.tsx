import type { ReactElement } from 'react'

export interface SpinnerProps {
  readonly label: string
}

export function Spinner({ label }: SpinnerProps): ReactElement {
  return (
    <div className="axw-page-state" role="status">
      <div className="axw-spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}
