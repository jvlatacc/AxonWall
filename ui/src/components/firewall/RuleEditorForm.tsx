import { useState } from 'react'
import type { ReactElement } from 'react'
import type { Verdict } from '../../api/types'
import { validateRule } from '../../validation/validate'
import type { RuleErrors, RuleValues, ValidationContext } from '../../validation/validate'
import { Button } from '../ui/Button'
import { SelectField } from '../forms/SelectField'
import { TextField } from '../forms/TextField'

const EMPTY_VALUES: RuleValues = {
  name: '',
  from: '',
  to: '',
  service: '',
  sourceAlias: '',
  verdict: '',
}

export interface RuleEditorFormProps {
  /** Values to prefill (edit mode); omit for a blank new-rule form. */
  readonly initial?: RuleValues
  /** Zone names from the current config (the firewall pseudo-zone is added for `to`). */
  readonly zones: readonly string[]
  readonly aliases: readonly string[]
  readonly takenNames: readonly string[]
  readonly onSave: (values: RuleValues & { verdict: Verdict }) => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

export function RuleEditorForm({
  initial = EMPTY_VALUES,
  zones,
  aliases,
  takenNames,
  onSave,
  onCancel,
  disabled = false,
}: RuleEditorFormProps): ReactElement {
  const [values, setValues] = useState<RuleValues>(initial)
  const [errors, setErrors] = useState<RuleErrors>({})
  const [submitted, setSubmitted] = useState(false)

  const ctx: ValidationContext = {
    zoneNames: zones,
    interfaceNames: [],
    aliasNames: aliases,
    zoneSubnets: {},
  }

  const update = (patch: Partial<RuleValues>): void => {
    const next = { ...values, ...patch }
    setValues(next)
    // After a failed submit, re-validate live so errors clear as they fix.
    if (submitted) setErrors(validateRule(next, ctx, takenNames))
  }

  const submit = (): void => {
    setSubmitted(true)
    const found = validateRule(values, ctx, takenNames)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    onSave({ ...values, verdict: values.verdict as Verdict })
  }

  const zoneOptions = zones.map((z) => ({ value: z, label: z }))

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
          placeholder="lan-to-wan"
        />
        <SelectField
          label="From zone"
          value={values.from}
          options={[{ value: '', label: '—' }, ...zoneOptions]}
          onChange={(from) => update({ from })}
          error={errors.from}
        />
        <SelectField
          label="To zone"
          value={values.to}
          options={[{ value: '', label: '—' }, { value: 'firewall', label: 'firewall' }, ...zoneOptions]}
          onChange={(to) => update({ to })}
          error={errors.to}
        />
        <TextField
          label="Service"
          value={values.service}
          onChange={(service) => update({ service })}
          error={errors.service}
          hint="ssh, http, https, dns, tcp/443, udp/51820 — empty means any"
          mono
        />
        <SelectField
          label="Source alias"
          value={values.sourceAlias}
          options={[{ value: '', label: '—' }, ...aliases.map((a) => ({ value: a, label: a }))]}
          onChange={(sourceAlias) => update({ sourceAlias })}
          error={errors.sourceAlias}
          hint="Optional source refinement"
        />
        <SelectField
          label="Verdict"
          value={values.verdict}
          options={[
            { value: '', label: '—' },
            { value: 'accept', label: 'accept' },
            { value: 'drop', label: 'drop' },
            { value: 'reject', label: 'reject' },
          ]}
          onChange={(verdict) => update({ verdict: verdict as RuleValues['verdict'] })}
          error={errors.verdict}
        />
      </div>
      <div className="axw-form-actions">
        <Button variant="ghost" disabled={disabled} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={disabled}>
          Save rule
        </Button>
      </div>
    </form>
  )
}
