import { useState } from 'react'
import type { ReactElement } from 'react'
import type { AxonWallConfig } from '../api/types'
import { AliasEditorForm } from '../components/aliases/AliasEditorForm'
import { AliasesTable } from '../components/aliases/AliasesTable'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { removeAlias, upsertAlias } from '../model/updaters'
import type { AliasValues } from '../validation/validate'

export interface AliasesPageProps {
  readonly config: AxonWallConfig
  readonly saving: boolean
  readonly onChange: (next: AxonWallConfig) => void
}

type EditorState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'new' }
  | { readonly kind: 'edit'; readonly name: string }
  | { readonly kind: 'delete'; readonly name: string }

export function AliasesPage({ config, saving, onChange }: AliasesPageProps): ReactElement {
  const [editor, setEditor] = useState<EditorState>({ kind: 'closed' })
  const aliases = config.firewall.aliases
  const names = Object.keys(aliases)

  const editName = editor.kind === 'edit' ? editor.name : undefined
  const editAlias = editName === undefined ? undefined : aliases[editName]
  const takenNames = names.filter((n) => n !== editName)

  const save = (previousName: string | undefined) => (values: AliasValues): void => {
    onChange(
      upsertAlias(config, previousName, values.name.trim(), {
        type: values.type,
        entries: values.entries,
      }),
    )
    setEditor({ kind: 'closed' })
  }

  return (
    <div>
      <h1 className="axw-page-title">Aliases</h1>
      <Card title="Aliases">
        <AliasesTable
          aliases={aliases}
          disabled={saving}
          onEdit={(name) => setEditor({ kind: 'edit', name })}
          onDelete={(name) => setEditor({ kind: 'delete', name })}
        />
        <div className="axw-form-actions">
          <Button variant="primary" disabled={saving} onClick={() => setEditor({ kind: 'new' })}>
            Add alias
          </Button>
        </div>
      </Card>

      {editor.kind === 'new' && (
        <Card title="New alias">
          <AliasEditorForm
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(undefined)}
          />
        </Card>
      )}

      {editor.kind === 'edit' && editAlias !== undefined && (
        <Card title={`Edit alias: ${editName}`}>
          <AliasEditorForm
            initial={{ name: editName ?? '', type: editAlias.type, entries: editAlias.entries }}
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(editor.name)}
          />
        </Card>
      )}

      {editor.kind === 'delete' && aliases[editor.name] !== undefined && (
        <ConfirmDialog
          title="Delete alias"
          message={`Delete alias "${editor.name}"? Rules referencing it will fail validation on the next save.`}
          confirmLabel="Delete"
          disabled={saving}
          onCancel={() => setEditor({ kind: 'closed' })}
          onConfirm={() => {
            onChange(removeAlias(config, editor.name))
            setEditor({ kind: 'closed' })
          }}
        />
      )}
    </div>
  )
}
