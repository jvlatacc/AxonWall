import type { ReactElement } from 'react'
import type { DefaultPolicies } from '../../api/types'
import { SelectField } from '../forms/SelectField'

export interface DefaultPoliciesCardProps {
  readonly policies: DefaultPolicies
  readonly onChange: (next: DefaultPolicies) => void
  readonly disabled?: boolean
}

const POLICY_OPTIONS = [
  { value: 'drop', label: 'drop' },
  { value: 'accept', label: 'accept' },
]

export function DefaultPoliciesCard({
  policies,
  onChange,
  disabled = false,
}: DefaultPoliciesCardProps): ReactElement {
  const option =
    (v: 'input' | 'forward' | 'output') =>
    (value: string): void => {
      if (value === 'accept' || value === 'drop') onChange({ ...policies, [v]: value })
    }
  return (
    <div className="axw-form-grid">
      <SelectField
        label="Input policy"
        value={policies.input}
        options={POLICY_OPTIONS}
        onChange={option('input')}
        hint="Traffic to the appliance itself"
        disabled={disabled}
      />
      <SelectField
        label="Forward policy"
        value={policies.forward}
        options={POLICY_OPTIONS}
        onChange={option('forward')}
        hint="Traffic routed through the appliance"
        disabled={disabled}
      />
      <SelectField
        label="Output policy"
        value={policies.output}
        options={POLICY_OPTIONS}
        onChange={option('output')}
        hint="Traffic leaving the appliance"
        disabled={disabled}
      />
    </div>
  )
}
