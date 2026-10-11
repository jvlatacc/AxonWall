import { useState } from 'react'
import type { ReactElement } from 'react'
import type { WgPeer } from '../../api/types'
import { validateWgPeer } from '../../validation/validate'
import type { WgPeerErrors } from '../../validation/validate'
import { Button } from '../ui/Button'
import { TextField } from '../forms/TextField'
import { TextAreaField } from '../forms/TextAreaField'

export interface WireGuardPeerEditorFormProps {
  readonly initial?: WgPeer
  /** Names already used by other peers (the edited peer's own name is excluded by the caller). */
  readonly takenNames: readonly string[]
  readonly takenKeys: readonly string[]
  readonly onSave: (peer: WgPeer) => void
  readonly onCancel: () => void
  readonly disabled?: boolean
}

const EMPTY_PEER: WgPeer = { name: '', publicKey: '', allowedIps: [] }

/** Editable text state; allowedIps is one CIDR per line. */
interface FormState {
  readonly name: string
  readonly publicKey: string
  readonly allowedIpsText: string
}

function toWgPeer(state: FormState): WgPeer {
  return {
    name: state.name,
    publicKey: state.publicKey,
    allowedIps: state.allowedIpsText
      .split('\n')
      .map((s) => s.trim())
      .filter((s) => s !== ''),
  }
}

function toFormState(peer: WgPeer): FormState {
  return { name: peer.name, publicKey: peer.publicKey, allowedIpsText: peer.allowedIps.join('\n') }
}

export function WireGuardPeerEditorForm({
  initial = EMPTY_PEER,
  takenNames,
  takenKeys,
  onSave,
  onCancel,
  disabled = false,
}: WireGuardPeerEditorFormProps): ReactElement {
  const [values, setValues] = useState<FormState>(() => toFormState(initial))
  const [errors, setErrors] = useState<WgPeerErrors>({})
  const [submitted, setSubmitted] = useState(false)

  const update = (patch: Partial<FormState>): void => {
    const next = { ...values, ...patch }
    setValues(next)
    if (submitted) setErrors(validateWgPeer(toWgPeer(next), takenNames, takenKeys))
  }

  const submit = (): void => {
    setSubmitted(true)
    const found = validateWgPeer(toWgPeer(values), takenNames, takenKeys)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    onSave(toWgPeer(values))
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
          label="Peer name"
          value={values.name}
          onChange={(name) => update({ name })}
          error={errors.name}
          placeholder="laptop"
        />
        <TextField
          label="Public key"
          value={values.publicKey}
          onChange={(publicKey) => update({ publicKey })}
          error={errors.publicKey}
          placeholder="base64 32-byte WireGuard public key"
          mono
        />
      </div>
      <TextAreaField
        label="Allowed IPs"
        value={values.allowedIpsText}
        onChange={(allowedIpsText) => update({ allowedIpsText })}
        error={errors.allowedIps}
        placeholder={'10.10.0.2/32\n10.10.0.0/24'}
        hint="One CIDR per line"
        mono
        rows={3}
      />
      <div className="axw-form-actions">
        <Button variant="ghost" disabled={disabled} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={disabled}>
          Save peer
        </Button>
      </div>
    </form>
  )
}
