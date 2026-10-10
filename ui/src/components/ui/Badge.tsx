import type { ReactElement } from 'react'

export type BadgeTone = 'neutral' | 'success' | 'error' | 'warning' | 'accent'

export interface BadgeProps {
  readonly children: React.ReactNode
  readonly tone?: BadgeTone
}

export function Badge({ children, tone = 'neutral' }: BadgeProps): ReactElement {
  return <span className={`axw-badge${tone === 'neutral' ? '' : ` axw-badge-${tone}`}`}>{children}</span>
}
