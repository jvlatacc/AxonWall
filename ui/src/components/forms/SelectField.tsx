import { useId } from 'react'
import type { ChangeEvent, ReactElement } from 'react'

export interface SelectOption {
  readonly value: string
  readonly label: string
}

export interface SelectFieldProps {
  readonly label: string
  readonly value: string
  readonly options: readonly SelectOption[]
  readonly onChange: (value: string) => void
  readonly error?: string
  readonly hint?: string
  readonly disabled?: boolean
}

export function SelectField({
  label,
  value,
  options,
  onChange,
  error,
  hint,
  disabled = false,
}: SelectFieldProps): ReactElement {
  const id = useId()
  return (
    <div className="axw-field">
      <label className="axw-field-label" htmlFor={id}>
        {label}
      </label>
      <select
        id={id}
        className="axw-select"
        value={value}
        disabled={disabled}
        onChange={(e: ChangeEvent<HTMLSelectElement>) => onChange(e.target.value)}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      {error !== undefined && <span className="axw-field-error">{error}</span>}
      {error === undefined && hint !== undefined && <span className="axw-field-hint">{hint}</span>}
    </div>
  )
}
