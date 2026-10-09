import type { ReactElement } from 'react'
import type { DnsMode, DnsService } from '../../api/types'
import { SelectField } from '../forms/SelectField'
import { TextAreaField } from '../forms/TextAreaField'

export interface DnsPanelProps {
  readonly dns: DnsService
  readonly zoneNames: readonly string[]
  readonly onChange: (next: DnsService) => void
  readonly disabled?: boolean
}

function toggleListen(zone: string, current: readonly string[]): string[] {
  return current.includes(zone) ? current.filter((z) => z !== zone) : [...current, zone]
}

/** Forwarders as comma/newline separated text — one upstream resolver per entry. */
function forwardersToText(forwarders: readonly string[] | undefined): string {
  return (forwarders ?? []).join(', ')
}

function textToForwarders(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((f) => f.trim())
    .filter((f) => f !== '')
}

export function DnsPanel({ dns, zoneNames, onChange, disabled = false }: DnsPanelProps): ReactElement {
  const mode: DnsMode = dns.mode ?? 'recursive'
  const forwardersText = forwardersToText(dns.forwarders)
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
      <SelectField
        label="Mode"
        value={mode}
        options={[
          { value: 'recursive', label: 'recursive (resolve from the root)' },
          { value: 'forward', label: 'forward (use upstream resolvers)' },
        ]}
        onChange={(nextMode) => {
          const next = nextMode as DnsMode
          if (next === mode) return
          // Recursive mode never carries forwarders (validate.go: forwarders
          // are only allowed in forward mode).
          onChange(next === 'forward' ? { ...dns, mode: next } : { ...dns, mode: next, forwarders: undefined })
        }}
        hint="Forward mode sends queries to the upstream resolvers listed below"
        disabled={disabled}
      />
      {mode === 'forward' && (
        <TextAreaField
          label="Forwarders"
          value={forwardersText}
          rows={2}
          onChange={(text) => {
            const forwarders = textToForwarders(text)
            onChange({ ...dns, forwarders: forwarders.length > 0 ? forwarders : undefined })
          }}
          hint="Comma-separated upstream resolver IPs (at least one required)"
          placeholder="1.1.1.1, 8.8.8.8"
          mono
          disabled={disabled}
        />
      )}
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
