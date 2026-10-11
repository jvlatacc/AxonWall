import { useRef, useState } from "react";
import type { ChangeEvent, ReactElement } from "react";
import type { AxonWallClient } from "../api/client";
import type { AxonWallConfig } from "../api/types";
import { configFromYaml, configToYaml } from "../api/yaml";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";

export interface BackupPageProps {
  readonly config: AxonWallConfig;
  readonly saving: boolean;
  readonly onChange: (next: AxonWallConfig) => void;
  /** Live client for archive transfer against the appliance. */
  readonly client?: AxonWallClient;
  /** Invoked after a live restore so the store reloads the new state. */
  readonly onRestored?: () => void;
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

/** Serialize and download the current config document. Pure client-side — no API round-trip. */
function downloadConfig(config: AxonWallConfig): void {
  const blob = new Blob([configToYaml(config)], { type: "application/yaml" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "axonwall-config.yaml";
  anchor.click();
  URL.revokeObjectURL(url);
}

export function BackupPage({
  config,
  saving,
  onChange,
  client,
  onRestored,
}: BackupPageProps): ReactElement {
  const [importError, setImportError] = useState<string | undefined>(undefined);
  const [archiveBusy, setArchiveBusy] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<File>();
  const [restoreError, setRestoreError] = useState<string | undefined>(
    undefined,
  );
  const fileInput = useRef<HTMLInputElement>(null);
  const archiveInput = useRef<HTMLInputElement>(null);

  const onFilePicked = (event: ChangeEvent<HTMLInputElement>): void => {
    const file = event.target.files?.[0];
    event.target.value = ""; // allow re-picking the same file after a failure
    if (file === undefined) return;
    file
      .text()
      .then((text) => {
        let parsed: AxonWallConfig;
        try {
          parsed = configFromYaml(text);
        } catch (e) {
          setImportError(
            e instanceof Error
              ? e.message
              : "The file is not a valid AxonWall config document.",
          );
          return;
        }
        setImportError(undefined);
        onChange(parsed); // routed through the store: validate → apply → commit or rollback
      })
      .catch(() => {
        setImportError("The file could not be read.");
      });
  };

  const exportArchive = async (): Promise<void> => {
    if (client === undefined) return;
    setArchiveBusy(true);
    setRestoreError(undefined);
    try {
      const archive = await client.exportBackup();
      downloadBlob(archive, "axonwall-backup.tar");
    } catch (err) {
      setRestoreError(err instanceof Error ? err.message : String(err));
    } finally {
      setArchiveBusy(false);
    }
  };

  const restoreArchive = async (file: File): Promise<void> => {
    if (client === undefined) return;
    setArchiveBusy(true);
    setRestoreError(undefined);
    try {
      await client.restoreBackup(file);
      setRestoreTarget(undefined);
      onRestored?.();
    } catch (err) {
      setRestoreError(err instanceof Error ? err.message : String(err));
    } finally {
      setArchiveBusy(false);
    }
  };

  return (
    <div>
      <h1 className="axw-page-title">Backup</h1>
      <p className="axw-hint">
        Configuration is exported as YAML with its revision. A restore replaces
        the store and is applied through the normal validate-apply-confirm
        pipeline.
      </p>

      {importError !== undefined && (
        <div className="axw-error-banner" role="alert">
          Restore rejected: {importError}
        </div>
      )}

      <Card title="Export">
        <p className="axw-hint">Download the current configuration document.</p>
        <div className="axw-form-actions">
          <Button
            variant="primary"
            disabled={saving}
            onClick={() => downloadConfig(config)}
          >
            Download backup
          </Button>
        </div>
      </Card>

      {client !== undefined && (
        <Card title="Appliance backup (full store archive)">
          <p className="axw-hint">
            Downloads the store archive — the config document, its manifest, and
            the revision history. A live restore replaces the whole store and
            re-applies through the normal pipeline.
          </p>
          {restoreError !== undefined && (
            <div className="axw-error-banner" role="alert">
              Appliance restore rejected: {restoreError}
            </div>
          )}
          <div className="axw-form-actions">
            <Button
              variant="primary"
              disabled={archiveBusy}
              onClick={() => void exportArchive()}
            >
              Download store archive
            </Button>
            <Button
              variant="secondary"
              disabled={archiveBusy}
              onClick={() => archiveInput.current?.click()}
            >
              Restore from archive…
            </Button>
          </div>
          <input
            ref={archiveInput}
            type="file"
            accept=".tar,application/x-tar"
            disabled={archiveBusy}
            style={{ display: "none" }}
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              if (file !== undefined) setRestoreTarget(file);
            }}
          />
          {restoreTarget !== undefined && (
            <ConfirmDialog
              title="Restore store archive"
              message={`Restore “${restoreTarget.name}”? This replaces the entire configuration store — current config and history are overwritten.`}
              confirmLabel="Restore archive"
              disabled={archiveBusy}
              onCancel={() => setRestoreTarget(undefined)}
              onConfirm={() => void restoreArchive(restoreTarget)}
            />
          )}
        </Card>
      )}

      <Card title="Restore">
        <p className="axw-hint">
          Pick an exported YAML document. It is validated before it is applied.
        </p>
        <input
          ref={fileInput}
          type="file"
          accept=".yaml,.yml,text/yaml"
          onChange={onFilePicked}
          disabled={saving}
        />
      </Card>
    </div>
  );
}
