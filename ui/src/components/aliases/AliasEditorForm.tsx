import { useState } from 'react'
import type { ReactElement } from 'react'
import type { AliasType } from '../../api/types'
import { validateAlias } from '../../validation/validate'
import type { AliasErrors, AliasValues } from '../../validation/validate'
import { Button } from '../ui/Button'
import { SelectField } from '../forms/SelectField'
import { TextAreaField } from '../forms/TextAreaField'
import { TextField } from '../forms/TextField'

export interface AliasEditorFormProps {
  readonly initial?: AliasValues
  readonly takenNames: readonly string[]
  readonly onSave: (values: AliasValues) => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

/** Entries as one-per-line text — the list shape the appliance stores. */
function entriesToText(entries: readonly string[]): string {
  return entries.join('\n')
}

function textToEntries(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '')
}

export function AliasEditorForm({
  initial = { name: '', type: 'ipv4', entries: [] },
  takenNames,
  onSave,
  onCancel,
  disabled = false,
}: AliasEditorFormProps): ReactElement {
  const [values, setValues] = useState<AliasValues>(initial)
  const [entriesText, setEntriesText] = useState(() => entriesToText(initial.entries))
  const [errors, setErrors] = useState<AliasErrors>({})
  const [submitted, setSubmitted] = useState(false)

  const validateWith = (next: AliasValues): AliasErrors => validateAlias(next, takenNames)

  const update = (patch: Partial<AliasValues>): void => {
    const next = { ...values, ...patch }
    setValues(next)
    if (submitted) setErrors(validateWith(next))
  }

  const submit = (): void => {
    setSubmitted(true)
    const next: AliasValues = { ...values, entries: textToEntries(entriesText) }
    const found = validateWith(next)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    onSave(next)
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
          placeholder="admin-hosts"
        />
        <SelectField
          label="Type"
          value={values.type}
          options={[
            { value: 'ipv4', label: 'ipv4' },
            { value: 'ipv6', label: 'ipv6' },
          ]}
          onChange={(type) => update({ type: type as AliasType })}
          error={errors.type}
        />
        <div className="axw-field-span">
          <TextAreaField
            label="Entries"
            value={entriesText}
            rows={4}
            onChange={(text) => {
              setEntriesText(text)
              if (submitted) {
                setErrors(validateWith({ ...values, entries: textToEntries(text) }))
              }
            }}
            error={errors.entries}
            hint="One address or CIDR per line, matching the selected type"
            placeholder={'192.168.1.10\n192.168.1.0/24'}
            mono
          />
        </div>
      </div>
      <div className="axw-form-actions">
        <Button variant="ghost" disabled={disabled} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={disabled}>
          Save alias
        </Button>
      </div>
    </form>
  )
}
