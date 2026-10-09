import { useId } from 'react'
import type { ChangeEvent, ReactElement } from 'react'

export interface TextFieldProps {
  readonly label: string
  readonly value: string
  readonly onChange: (value: string) => void
  readonly error?: string
  readonly hint?: string
  readonly placeholder?: string
  readonly disabled?: boolean
  readonly mono?: boolean
}

export function TextField({
  label,
  value,
  onChange,
  error,
  hint,
  placeholder,
  disabled = false,
  mono = false,
}: TextFieldProps): ReactElement {
  const id = useId()
  return (
    <div className="axw-field">
      <label className="axw-field-label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        className={`axw-input${mono ? ' axw-input-mono' : ''}`}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value)}
      />
      {error !== undefined && <span className="axw-field-error">{error}</span>}
      {error === undefined && hint !== undefined && <span className="axw-field-hint">{hint}</span>}
    </div>
  )
}
