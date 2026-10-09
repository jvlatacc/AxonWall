import { useRef, useState } from 'react'
import type { ChangeEvent, ReactElement } from 'react'
import type { AxonWallConfig } from '../api/types'
import { configFromYaml, configToYaml } from '../api/yaml'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'

export interface BackupPageProps {
  readonly config: AxonWallConfig
  readonly saving: boolean
  readonly onChange: (next: AxonWallConfig) => void
}

/** Serialize and download the current config document. Pure client-side — no API round-trip. */
function downloadConfig(config: AxonWallConfig): void {
  const blob = new Blob([configToYaml(config)], { type: 'application/yaml' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = 'axonwall-config.yaml'
  anchor.click()
  URL.revokeObjectURL(url)
}

export function BackupPage({ config, saving, onChange }: BackupPageProps): ReactElement {
  const [importError, setImportError] = useState<string | undefined>(undefined)
  const fileInput = useRef<HTMLInputElement>(null)

  const onFilePicked = (event: ChangeEvent<HTMLInputElement>): void => {
    const file = event.target.files?.[0]
    event.target.value = '' // allow re-picking the same file after a failure
    if (file === undefined) return
    file
      .text()
      .then((text) => {
        let parsed: AxonWallConfig
        try {
          parsed = configFromYaml(text)
        } catch (e) {
          setImportError(e instanceof Error ? e.message : 'The file is not a valid AxonWall config document.')
          return
        }
        setImportError(undefined)
        onChange(parsed) // routed through the store: validate → apply → commit or rollback
      })
      .catch(() => {
        setImportError('The file could not be read.')
      })
  }

  return (
    <div>
      <h1 className="axw-page-title">Backup</h1>
      <p className="axw-hint">
        Configuration is exported as YAML with its revision. A restore replaces the store and is applied
        through the normal validate-apply-confirm pipeline.
      </p>

      {importError !== undefined && (
        <div className="axw-error-banner" role="alert">
          Restore rejected: {importError}
        </div>
      )}

      <Card title="Export">
        <p className="axw-hint">Download the current configuration document.</p>
        <div className="axw-form-actions">
          <Button variant="primary" disabled={saving} onClick={() => downloadConfig(config)}>
            Download backup
          </Button>
        </div>
      </Card>

      <Card title="Restore">
        <p className="axw-hint">Pick an exported YAML document. It is validated before it is applied.</p>
        <input ref={fileInput} type="file" accept=".yaml,.yml,text/yaml" onChange={onFilePicked} disabled={saving} />
      </Card>
    </div>
  )
}
