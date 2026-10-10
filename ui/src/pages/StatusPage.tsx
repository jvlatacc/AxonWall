import { useEffect } from "react";
import type { ReactElement } from "react";
import type { SystemStatus } from "../api/types";
import { Alert } from "../components/ui/Alert";
import { Button } from "../components/ui/Button";
import { StatusTable } from "../components/status/StatusTable";

export interface StatusPageProps {
  readonly status: SystemStatus | undefined;
  /** Refresh the runtime status in place; wired to the store when live. */
  readonly onRefresh?: () => void;
  readonly refreshError?: string;
}

const REFRESH_INTERVAL_MS = 5_000;

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function StatusPage({
  status,
  onRefresh,
  refreshError,
}: StatusPageProps): ReactElement {
  // Live view: refresh on an interval while the page is mounted.
  useEffect(() => {
    if (onRefresh === undefined) return;
    onRefresh();
    const timer = window.setInterval(onRefresh, REFRESH_INTERVAL_MS);
    return () => {
      window.clearInterval(timer);
    };
  }, [onRefresh]);

  if (status === undefined) {
    return (
      <div>
        <h1 className="axw-page-title">Status</h1>
        <p className="axw-hint">Status has not been reported yet.</p>
      </div>
    );
  }
  return (
    <div>
      <h1 className="axw-page-title">Status</h1>
      {refreshError !== undefined && (
        <Alert severity="error" title="Refresh failed" message={refreshError} />
      )}
      <div className="axw-form-actions">
        {onRefresh !== undefined && (
          <Button variant="secondary" onClick={onRefresh}>
            Refresh now
          </Button>
        )}
      </div>
      <div className="axw-table-wrap">
        <table className="axw-table">
          <tbody>
            <tr>
              <td>Hostname</td>
              <td>{status.hostname}</td>
            </tr>
            <tr>
              <td>Appliance version</td>
              <td>{status.version}</td>
            </tr>
            <tr>
              <td>Uptime</td>
              <td>{formatUptime(status.uptimeSeconds)}</td>
            </tr>
            <tr>
              <td>Config revision</td>
              <td className="axw-mono">{status.revision}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <StatusTable services={status.services} interfaces={status.interfaces} />
    </div>
  );
}
