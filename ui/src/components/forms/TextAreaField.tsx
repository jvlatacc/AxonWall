import { useId } from 'react'
import type { ChangeEvent, ReactElement } from 'react'

export interface TextAreaFieldProps {
  readonly label: string
  readonly value: string
  readonly onChange: (value: string) => void
  readonly error?: string
  readonly hint?: string
  readonly placeholder?: string
  readonly rows?: number
  readonly mono?: boolean
  readonly disabled?: boolean
}

export function TextAreaField({
  label,
  value,
  onChange,
  error,
  hint,
  placeholder,
  rows = 4,
  mono = false,
  disabled = false,
}: TextAreaFieldProps): ReactElement {
  const id = useId()
  return (
    <div className="axw-field">
      <label className="axw-field-label" htmlFor={id}>
        {label}
      </label>
      <textarea
        id={id}
        className={mono ? 'axw-textarea axw-mono' : 'axw-textarea'}
        value={value}
        rows={rows}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e: ChangeEvent<HTMLTextAreaElement>) => onChange(e.target.value)}
      />
      {error !== undefined && <span className="axw-field-error">{error}</span>}
      {error === undefined && hint !== undefined && <span className="axw-field-hint">{hint}</span>}
    </div>
  )
}
