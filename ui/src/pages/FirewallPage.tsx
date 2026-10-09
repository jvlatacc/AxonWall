import { useState } from 'react'
import type { ReactElement } from 'react'
import type { AxonWallConfig, FirewallRule, Verdict } from '../api/types'
import { DefaultPoliciesCard } from '../components/firewall/DefaultPoliciesCard'
import { RuleEditorForm } from '../components/firewall/RuleEditorForm'
import { RulesTable } from '../components/firewall/RulesTable'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { removeRule, setDefaultPolicies, setRule } from '../model/updaters'
import { validationContext } from '../validation/validate'
import type { RuleValues } from '../validation/validate'

export interface FirewallPageProps {
  readonly config: AxonWallConfig
  readonly saving: boolean
  readonly onChange: (next: AxonWallConfig) => void
}

type EditorState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'new' }
  | { readonly kind: 'edit'; readonly index: number }
  | { readonly kind: 'delete'; readonly index: number }

function toRule(values: RuleValues & { verdict: Verdict }): FirewallRule {
  return {
    name: values.name.trim(),
    from: values.from,
    to: values.to,
    verdict: values.verdict,
    ...(values.service === '' ? {} : { service: values.service }),
    ...(values.sourceAlias === '' ? {} : { sourceAlias: values.sourceAlias }),
  }
}

export function FirewallPage({ config, saving, onChange }: FirewallPageProps): ReactElement {
  const [editor, setEditor] = useState<EditorState>({ kind: 'closed' })
  const ctx = validationContext(config)
  const rules = config.firewall.rules

  const editIndex = editor.kind === 'edit' ? editor.index : undefined
  const editRule = editIndex === undefined ? undefined : rules[editIndex]
  const deleteIndex = editor.kind === 'delete' ? editor.index : undefined
  const deleteRule = deleteIndex === undefined ? undefined : rules[deleteIndex]
  const takenNames = rules
    .filter((_, i) => i !== editIndex)
    .map((r) => r.name)

  const save = (index: number | null) => (values: RuleValues & { verdict: Verdict }): void => {
    onChange(setRule(config, index, toRule(values)))
    setEditor({ kind: 'closed' })
  }

  return (
    <div>
      <h1 className="axw-page-title">Firewall</h1>

      <Card title="Default policies">
        <DefaultPoliciesCard
          policies={config.firewall.default}
          onChange={(defaultPolicies) => onChange(setDefaultPolicies(config, defaultPolicies))}
          disabled={saving}
        />
      </Card>

      <Card title="Rules">
        <RulesTable
          rules={rules}
          disabled={saving}
          onEdit={(index) => setEditor({ kind: 'edit', index })}
          onDelete={(index) => setEditor({ kind: 'delete', index })}
        />
        <div className="axw-form-actions">
          <Button variant="primary" disabled={saving} onClick={() => setEditor({ kind: 'new' })}>
            Add rule
          </Button>
        </div>
      </Card>

      {editor.kind === 'new' && (
        <Card title="New rule">
          <RuleEditorForm
            zones={ctx.zoneNames}
            aliases={ctx.aliasNames}
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(null)}
          />
        </Card>
      )}

      {editor.kind === 'edit' && editRule !== undefined && (
        <Card title={`Edit rule: ${editRule.name}`}>
          <RuleEditorForm
            initial={{
              name: editRule.name,
              from: editRule.from,
              to: editRule.to,
              service: editRule.service ?? '',
              sourceAlias: editRule.sourceAlias ?? '',
              verdict: editRule.verdict,
            }}
            zones={ctx.zoneNames}
            aliases={ctx.aliasNames}
            takenNames={takenNames}
            disabled={saving}
            onCancel={() => setEditor({ kind: 'closed' })}
            onSave={save(editor.index)}
          />
        </Card>
      )}

      {deleteRule !== undefined && deleteIndex !== undefined && (
        <ConfirmDialog
          title="Delete rule"
          message={`Delete rule "${deleteRule.name}"? Applied on the next config save.`}
          confirmLabel="Delete"
          disabled={saving}
          onCancel={() => setEditor({ kind: 'closed' })}
          onConfirm={() => {
            onChange(removeRule(config, deleteIndex))
            setEditor({ kind: 'closed' })
          }}
        />
      )}
    </div>
  )
}
