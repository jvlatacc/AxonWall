import { useState } from 'react'
import type { ReactElement } from 'react'
import type { AxonWallConfig, NatRule } from '../api/types'
import { NatEditorForm } from '../components/nat/NatEditorForm'
import { NatTable } from '../components/nat/NatTable'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { removeNatRule, setNatRule } from '../model/updaters'
import { validationContext } from '../validation/validate'

export interface NatPageProps {
  readonly config: AxonWallConfig
  readonly saving: boolean
  readonly onChange: (next: AxonWallConfig) => void
}

type EditorState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'new' }
  | { readonly kind: 'edit'; readonly index: number }
  | { readonly kind: 'delete'; readonly index: number }

export function NatPage({ config, saving, onChange }: NatPageProps): ReactElement {
  const [editor, setEditor] = useState<EditorState>({ kind: 'closed' })
  const ctx = validationContext(config)
  const nat = config.firewall.nat

  const editIndex = editor.kind === 'edit' ? editor.index : undefined
  const editRule = editIndex === undefined ? undefined : nat[editIndex]
  const deleteIndex = editor.kind === 'delete' ? editor.index : undefined
  const deleteRule = deleteIndex === undefined ? undefined : nat[deleteIndex]
  const takenNames = nat.filter((_, i) => i !== editIndex).map((r) => r.name)

  const save = (index: number | null) => (values: { name: string; out: string; source: string; mode: 'masquerade' }): void => {
    const rule: NatRule = { name: values.name.trim(), out: values.out, source: values.source, mode: values.mode }
    onChange(setNatRule(config, index, rule))
    setEditor({ kind: 'closed' })
  }

  return (
    <div>
      <h1 className="axw-page-title">NAT</h1>
      <Card title="NAT rules">
        <NatTable
          rules={nat}
          disabled={saving}
          onEdit={(index) => setEditor({ kind: 'edit', index })}
          onDelete={(index) => setEditor({ kind: 'delete', index })}
        />
        <div className="axw-form-actions">
          <Button variant="primary" disabled={saving} onClick={() => setEditor({ kind: 'new' })}>
            Add NAT rule
          </Button>
        </div>
      </Card>

      {editor.kind === 'new' && (
        <Card title="New NAT rule">
          <NatEditorForm
            ctx={ctx}
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(null)}
          />
        </Card>
      )}

      {editor.kind === 'edit' && editRule !== undefined && (
        <Card title={`Edit NAT rule: ${editRule.name}`}>
          <NatEditorForm
            initial={{ name: editRule.name, out: editRule.out, source: editRule.source, mode: editRule.mode }}
            ctx={ctx}
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(editor.index)}
          />
        </Card>
      )}

      {deleteRule !== undefined && deleteIndex !== undefined && (
        <ConfirmDialog
          title="Delete NAT rule"
          message={`Delete NAT rule "${deleteRule.name}"? Applied on the next config save.`}
          confirmLabel="Delete"
          disabled={saving}
          onCancel={() => setEditor({ kind: 'closed' })}
          onConfirm={() => {
            onChange(removeNatRule(config, deleteIndex))
            setEditor({ kind: 'closed' })
          }}
        />
      )}
    </div>
  )
}
