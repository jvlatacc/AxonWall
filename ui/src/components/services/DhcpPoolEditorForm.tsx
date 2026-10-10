import { useState } from 'react'
import type { ReactElement } from 'react'
import type { DhcpPool } from '../../api/types'
import { validateDhcpPool } from '../../validation/validate'
import type { DhcpPoolErrors, DhcpPoolValues, ValidationContext } from '../../validation/validate'
import { Button } from '../ui/Button'
import { SelectField } from '../forms/SelectField'
import { TextField } from '../forms/TextField'

export interface DhcpPoolEditorFormProps {
  readonly initial?: DhcpPoolValues
  readonly ctx: ValidationContext
  /** The other pools (excluding the one being edited) for the overlap check. */
  readonly others: readonly DhcpPool[]
  readonly onSave: (values: DhcpPoolValues) => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

const BLANK: DhcpPoolValues = { zone: '', start: '', end: '', gateway: '', dns: '' }

export function DhcpPoolEditorForm({
  initial = BLANK,
  ctx,
  others,
  onSave,
  onCancel,
  disabled = false,
}: DhcpPoolEditorFormProps): ReactElement {
  const [values, setValues] = useState<DhcpPoolValues>(initial)
  const [errors, setErrors] = useState<DhcpPoolErrors>({})
  const [submitted, setSubmitted] = useState(false)

  const update = (patch: Partial<DhcpPoolValues>): void => {
    const next = { ...values, ...patch }
    setValues(next)
    if (submitted) setErrors(validateDhcpPool(next, ctx, others))
  }

  const submit = (): void => {
    setSubmitted(true)
    const found = validateDhcpPool(values, ctx, others)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    onSave(values)
  }

  return (
    <form
      className="axw-field"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <SelectField
        label="Zone"
        value={values.zone}
        options={ctx.zoneNames.map((z) => ({ value: z, label: z }))}
        onChange={(zone) => update({ zone })}
        error={errors.zone}
        disabled={disabled}
      />
      <div className="axw-form-grid">
        <TextField
          label="Range start"
          value={values.start}
          onChange={(start) => update({ start })}
          error={errors.start}
          placeholder="192.168.1.100"
          mono
        />
        <TextField
          label="Range end"
          value={values.end}
          onChange={(end) => update({ end })}
          error={errors.end}
          placeholder="192.168.1.199"
          mono
        />
      </div>
      <div className="axw-form-grid">
        <TextField
          label="Gateway"
          value={values.gateway}
          onChange={(gateway) => update({ gateway })}
          error={errors.gateway}
          placeholder="192.168.1.1"
          mono
        />
        <TextField
          label="DNS server"
          value={values.dns}
          onChange={(dns) => update({ dns })}
          error={errors.dns}
          placeholder="192.168.1.1"
          mono
        />
      </div>
      {errors.form !== undefined && (
        <div className="axw-error-banner" role="alert">
          {errors.form}
        </div>
      )}
      <div className="axw-form-actions">
        <Button variant="ghost" disabled={disabled} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={disabled}>
          Save pool
        </Button>
      </div>
    </form>
  )
}
