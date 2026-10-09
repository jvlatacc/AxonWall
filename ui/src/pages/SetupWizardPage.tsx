// First-boot setup wizard: rotate the admin token, assign WAN/LAN devices,
// apply through the normal config pipeline, then stamp setup complete.
// The wizard runs against the same client the console uses — every visible
// change flows through the config store (spec criterion 6).

import { useEffect, useMemo, useState } from "react";
import type { ReactElement } from "react";
import type { AxonWallClient } from "../api/client";
import type { ConfigRevision, NetDevice } from "../api/types";
import { replaceSessionToken } from "../auth/session";
import { generateToken } from "../auth/token";
import { Alert } from "../components/ui/Alert";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { SelectField } from "../components/forms/SelectField";
import { Spinner } from "../components/ui/Spinner";
import { applyDeviceAssignment } from "./setup/deviceAssignment";

export interface SetupWizardPageProps {
  readonly client: AxonWallClient;
  /** Invoked after setup is complete so the app can mount the console. */
  readonly onFinished: () => void;
}

type Phase = "token" | "devices" | "review" | "applying" | "done";

interface ApplyError {
  readonly step: string;
  readonly message: string;
}

export function SetupWizardPage({
  client,
  onFinished,
}: SetupWizardPageProps): ReactElement {
  const [phase, setPhase] = useState<Phase>("token");
  const [token, setToken] = useState(() => generateToken());
  const [storedConfirmed, setStoredConfirmed] = useState(false);
  const [devices, setDevices] = useState<readonly NetDevice[]>();
  const [wanDevice, setWanDevice] = useState("");
  const [lanDevice, setLanDevice] = useState("");
  const [configRev, setConfigRev] = useState<ConfigRevision>();
  const [error, setError] = useState<ApplyError>();

  useEffect(() => {
    let cancelled = false;
    client
      .listNetDevices()
      .then((list) => {
        if (cancelled) return;
        setDevices(list);
        const names = list.map((d) => d.name);
        setWanDevice(names[0] ?? "");
        setLanDevice(names[1] ?? "");
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError({
            step: "Load devices",
            message: err instanceof Error ? err.message : String(err),
          });
      });
    return () => {
      cancelled = true;
    };
  }, [client]);

  const deviceOptions = useMemo(
    () =>
      (devices ?? []).map((d) => ({
        value: d.name,
        label: `${d.name} (${d.mac})`,
      })),
    [devices],
  );

  const rotateToken = async (): Promise<void> => {
    setError(undefined);
    try {
      await client.rotateToken(token);
      // The new token is authoritative immediately — swap it into the
      // session before any further wizard call.
      replaceSessionToken(token);
      setPhase("devices");
    } catch (err) {
      setError({
        step: "Set admin token",
        message: err instanceof Error ? err.message : String(err),
      });
    }
  };

  const startApply = async (): Promise<void> => {
    setError(undefined);
    setPhase("applying");
    try {
      const rev = configRev ?? (await client.getConfig());
      setConfigRev(rev);
      const next = applyDeviceAssignment(rev.config, wanDevice, lanDevice);
      if (typeof next === "string") {
        setError({ step: "Assign devices", message: next });
        setPhase("devices");
        return;
      }
      const written = await client.putConfig(next, rev.revision);
      setConfigRev(written);
      await client.completeSetup();
      setPhase("done");
    } catch (err) {
      setError({
        step: "Apply",
        message: err instanceof Error ? err.message : String(err),
      });
      setPhase("review");
    }
  };

  const regenerate = (): void => {
    setToken(generateToken());
    setStoredConfirmed(false);
    setError(undefined);
  };

  return (
    <div className="axw-content">
      <h1 className="axw-page-title">First-boot setup</h1>
      {error !== undefined && (
        <Alert
          severity="error"
          title={`${error.step} failed`}
          message={error.message}
        />
      )}

      {phase === "token" && (
        <Card title="Step 1 of 3 — admin token">
          <p>
            This is the only time the admin token is shown. Store it in a
            password manager before continuing — it authorizes every console and
            API session.
          </p>
          <div className="axw-field">
            <span className="axw-label">Admin token</span>
            <div className="axw-token-row">
              <code className="axw-token-value">{token}</code>
              <Button variant="secondary" onClick={regenerate}>
                Regenerate
              </Button>
            </div>
          </div>
          <label className="axw-check-item">
            <input
              type="checkbox"
              checked={storedConfirmed}
              onChange={(e) => setStoredConfirmed(e.target.checked)}
            />
            <span>I have stored this token somewhere safe</span>
          </label>
          <div className="axw-form-actions">
            <Button
              variant="primary"
              disabled={!storedConfirmed}
              onClick={() => void rotateToken()}
            >
              Set admin token
            </Button>
          </div>
        </Card>
      )}

      {phase === "devices" && (
        <Card title="Step 2 of 3 — device assignment">
          {devices === undefined ? (
            <Spinner label="Detecting network devices…" />
          ) : (
            <>
              <p>
                Assign each detected kernel device a role. WAN typically faces
                your modem; LAN your switch.
              </p>
              <SelectField
                label="WAN device"
                value={wanDevice}
                options={deviceOptions}
                onChange={setWanDevice}
                disabled={deviceOptions.length === 0}
              />
              <SelectField
                label="LAN device"
                value={lanDevice}
                options={deviceOptions}
                onChange={setLanDevice}
                error={
                  wanDevice !== "" && wanDevice === lanDevice
                    ? "WAN and LAN must differ"
                    : undefined
                }
                disabled={deviceOptions.length === 0}
              />
              <div className="axw-form-actions">
                <Button variant="secondary" onClick={() => setPhase("token")}>
                  Back
                </Button>
                <Button
                  variant="primary"
                  disabled={
                    deviceOptions.length === 0 ||
                    wanDevice === "" ||
                    lanDevice === "" ||
                    wanDevice === lanDevice
                  }
                  onClick={() => {
                    setError(undefined);
                    setPhase("review");
                  }}
                >
                  Review
                </Button>
              </div>
            </>
          )}
        </Card>
      )}

      {phase === "review" && (
        <Card title="Step 3 of 3 — review and apply">
          <ul>
            <li>
              <span className="axw-mono">{wanDevice}</span> → WAN (
              <span className="axw-mono">wan0</span>, DHCP)
            </li>
            <li>
              <span className="axw-mono">{lanDevice}</span> → LAN (
              <span className="axw-mono">lan0</span>, 192.168.1.1/24)
            </li>
            <li>
              Firewall, DHCP, DNS, and WireGuard keep their default wave-1
              configuration.
            </li>
          </ul>
          <p>
            The change applies through the normal render → validate → apply
            pipeline with rollback protection.
          </p>
          <div className="axw-form-actions">
            <Button variant="secondary" onClick={() => setPhase("devices")}>
              Back
            </Button>
            <Button variant="primary" onClick={() => void startApply()}>
              Apply setup
            </Button>
          </div>
        </Card>
      )}

      {phase === "applying" && (
        <Spinner label="Applying setup — rendering, validating, and committing…" />
      )}

      {phase === "done" && (
        <Card title="Setup complete">
          <p>
            The admin token is set, WAN/LAN are assigned, and the configuration
            is committed. Welcome to your AxonWall appliance.
          </p>
          <div className="axw-form-actions">
            <Button variant="primary" onClick={onFinished}>
              Open the console
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}
