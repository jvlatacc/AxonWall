import { useId } from 'react'
import type { ChangeEvent, ReactElement } from 'react'

export interface CheckboxFieldProps {
  readonly label: string
  readonly checked: boolean
  readonly onChange: (checked: boolean) => void
  readonly hint?: string
  readonly disabled?: boolean
}

export function CheckboxField({
  label,
  checked,
  onChange,
  hint,
  disabled = false,
}: CheckboxFieldProps): ReactElement {
  const id = useId()
  return (
    <div className="axw-field">
      <label className="axw-checkbox" htmlFor={id}>
        <input
          id={id}
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.checked)}
        />
        <span>{label}</span>
      </label>
      {hint !== undefined && <span className="axw-field-hint">{hint}</span>}
    </div>
  )
}
