import type { ReactElement, ReactNode } from 'react'

export interface CardProps {
  readonly title?: string
  /** Buttons rendered on the right of the card header. */
  readonly actions?: ReactNode
  readonly children: ReactNode
}

export function Card({ title, actions, children }: CardProps): ReactElement {
  return (
    <section className="axw-card">
      {(title !== undefined || actions !== undefined) && (
        <div className="axw-card-header">
          {title !== undefined && <h2 className="axw-card-title">{title}</h2>}
          {actions !== undefined && <div className="axw-card-actions">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  )
}
