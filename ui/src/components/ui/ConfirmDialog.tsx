import type { ReactElement } from 'react'
import { Button } from '../ui/Button'

export interface ConfirmDialogProps {
  readonly title: string
  readonly message: string
  readonly confirmLabel?: string
  readonly onConfirm: () => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel = 'Confirm',
  onConfirm,
  onCancel,
  disabled = false,
}: ConfirmDialogProps): ReactElement {
  return (
    <div className="axw-modal-backdrop" role="presentation">
      <div className="axw-modal" role="dialog" aria-modal="true" aria-label={title}>
        <h3 className="axw-modal-title">{title}</h3>
        <p className="axw-modal-message">{message}</p>
        <div className="axw-form-actions">
          <Button variant="ghost" disabled={disabled} onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="danger" disabled={disabled} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}
