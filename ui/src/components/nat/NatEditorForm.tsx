import { useState } from 'react'
import type { ReactElement } from 'react'
import { validateNat } from '../../validation/validate'
import type { NatErrors, NatMode, NatValues, ValidationContext } from '../../validation/validate'
import { Button } from '../ui/Button'
import { SelectField } from '../forms/SelectField'
import { TextField } from '../forms/TextField'

export interface NatEditorFormProps {
  readonly initial?: NatValues
  readonly ctx: ValidationContext
  readonly takenNames: readonly string[]
  readonly onSave: (values: NatValues & { mode: NatMode }) => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

const BLANK: NatValues = {
  name: '',
  mode: '',
  out: '',
  source: '',
  in: '',
  proto: '',
  dstPort: '',
  to: '',
}

export function NatEditorForm({
  initial = BLANK,
  ctx,
  takenNames,
  onSave,
  onCancel,
  disabled = false,
}: NatEditorFormProps): ReactElement {
  const [values, setValues] = useState<NatValues>(initial)
  const [errors, setErrors] = useState<NatErrors>({})
  const [submitted, setSubmitted] = useState(false)

  const update = (patch: Partial<NatValues>): void => {
    const next = { ...values, ...patch }
    setValues(next)
    if (submitted) setErrors(validateNat(next, ctx, takenNames))
  }

  const submit = (): void => {
    setSubmitted(true)
    const found = validateNat(values, ctx, takenNames)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    if (values.mode === 'masquerade') {
      onSave({ ...values, mode: 'masquerade' })
    } else if (values.mode === 'port-forward') {
      onSave({ ...values, mode: 'port-forward' })
    }
  }

  return (
    <form
      className="axw-field"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <div className="axw-form-grid">
        <TextField
          label="Name"
          value={values.name}
          onChange={(name) => update({ name })}
          error={errors.name}
          placeholder="lan-masq"
        />
        <SelectField
          label="Mode"
          value={values.mode}
          options={[
            { value: '', label: '—' },
            { value: 'masquerade', label: 'masquerade (source NAT)' },
            { value: 'port-forward', label: 'port-forward (DNAT)' },
          ]}
          onChange={(mode) => update({ mode: mode as NatValues['mode'] })}
          error={errors.mode}
        />
        {values.mode === 'masquerade' && (
          <>
            <SelectField
              label="Out interface"
              value={values.out}
              options={[{ value: '', label: '—' }, ...ctx.interfaceNames.map((i) => ({ value: i, label: i }))]}
              onChange={(out) => update({ out })}
              error={errors.out}
            />
            <SelectField
              label="Source zone"
              value={values.source}
              options={[{ value: '', label: '—' }, ...ctx.zoneNames.map((z) => ({ value: z, label: z }))]}
              onChange={(source) => update({ source })}
              error={errors.source}
            />
          </>
        )}
        {values.mode === 'port-forward' && (
          <>
            <SelectField
              label="In interface"
              value={values.in}
              options={[{ value: '', label: '—' }, ...ctx.interfaceNames.map((i) => ({ value: i, label: i }))]}
              onChange={(inIf) => update({ in: inIf })}
              error={errors.in}
            />
            <SelectField
              label="Protocol"
              value={values.proto}
              options={[
                { value: '', label: '—' },
                { value: 'tcp', label: 'tcp' },
                { value: 'udp', label: 'udp' },
              ]}
              onChange={(proto) => update({ proto: proto as NatValues['proto'] })}
              error={errors.proto}
            />
            <TextField
              label="Destination port"
              value={values.dstPort}
              onChange={(dstPort) => update({ dstPort })}
              error={errors.dstPort}
              placeholder="8080"
            />
            <TextField
              label="Internal target"
              value={values.to}
              onChange={(to) => update({ to })}
              error={errors.to}
              placeholder="192.168.1.50 or 192.168.1.50:80"
            />
          </>
        )}
        {errors.form !== undefined && <p className="axw-error">{errors.form}</p>}
      </div>
      <div className="axw-form-actions">
        <Button variant="ghost" disabled={disabled} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={disabled}>
          Save NAT rule
        </Button>
      </div>
    </form>
  )
}
