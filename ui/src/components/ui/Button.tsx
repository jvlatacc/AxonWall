import type { ReactElement, MouseEventHandler } from 'react'

export interface ButtonProps {
  readonly children: React.ReactNode
  readonly onClick?: MouseEventHandler<HTMLButtonElement>
  readonly variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  readonly size?: 'md' | 'sm'
  readonly type?: 'button' | 'submit'
  readonly disabled?: boolean
}

export function Button({
  children,
  onClick,
  variant = 'secondary',
  size = 'md',
  type = 'button',
  disabled = false,
}: ButtonProps): ReactElement {
  return (
    <button
      type={type}
      className={`axw-btn axw-btn-${variant}${size === 'sm' ? ' axw-btn-sm' : ''}`}
      onClick={onClick}
      disabled={disabled}
    >
      {children}
    </button>
  )
}
