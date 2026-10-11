import type { ReactElement, ReactNode } from 'react'

export interface AlertProps {
  readonly severity: 'error' | 'info'
  readonly title: string
  readonly message?: string
  readonly onDismiss?: () => void
  readonly action?: ReactNode
}

export function Alert({ severity, title, message, onDismiss, action }: AlertProps): ReactElement {
  return (
    <div className={`axw-alert axw-alert-${severity}`} role={severity === 'error' ? 'alert' : 'status'}>
      <div className="axw-alert-body">
        <div className="axw-alert-title">{title}</div>
        {message !== undefined && message !== '' && <div className="axw-alert-message">{message}</div>}
      </div>
      {action}
      {onDismiss !== undefined && (
        <button type="button" className="axw-btn axw-btn-ghost axw-btn-sm" onClick={onDismiss}>
          Dismiss
        </button>
      )}
    </div>
  )
}
