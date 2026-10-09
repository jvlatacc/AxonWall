import type { ReactElement } from 'react'
import type { DnsService } from '../../api/types'
import { SelectField } from '../forms/SelectField'

export interface DnsPanelProps {
  readonly dns: DnsService
  readonly zoneNames: readonly string[]
  readonly onChange: (next: DnsService) => void
  readonly disabled?: boolean
}

function toggleListen(zone: string, current: readonly string[]): string[] {
  return current.includes(zone) ? current.filter((z) => z !== zone) : [...current, zone]
}

export function DnsPanel({ dns, zoneNames, onChange, disabled = false }: DnsPanelProps): ReactElement {
  return (
    <div className="axw-field">
      <SelectField
        label="Resolver"
        value={dns.resolver}
        options={[{ value: 'unbound', label: 'unbound' }]}
        onChange={(resolver) => {
          if (resolver === 'unbound') onChange({ ...dns, resolver })
        }}
        hint="Recursive, DNSSEC-validating resolver (Unbound)"
        disabled={disabled}
      />
      <fieldset className="axw-fieldset">
        <legend>Listen on zones</legend>
        <div className="axw-check-list">
          {zoneNames.map((zone) => (
            <label key={zone} className="axw-check-item">
              <input
                type="checkbox"
                checked={dns.listen.includes(zone)}
                disabled={disabled}
                onChange={() => onChange({ ...dns, listen: toggleListen(zone, dns.listen) })}
              />
              <span className="axw-mono">{zone}</span>
            </label>
          ))}
        </div>
      </fieldset>
    </div>
  )
}
