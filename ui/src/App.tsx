import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactElement } from "react";
import { AppShell } from "./components/layout/AppShell";
import { Alert } from "./components/ui/Alert";
import { Button } from "./components/ui/Button";
import { Spinner } from "./components/ui/Spinner";
import { AliasesPage } from "./pages/AliasesPage";
import { BackupPage } from "./pages/BackupPage";
import { DashboardPage } from "./pages/DashboardPage";
import { FirewallPage } from "./pages/FirewallPage";
import { LoginPage } from "./pages/LoginPage";
import { NatPage } from "./pages/NatPage";
import { ServicesPage } from "./pages/ServicesPage";
import { StatusPage } from "./pages/StatusPage";
import { SetupWizardPage } from "./pages/SetupWizardPage";
import type { PageId } from "./nav";
import {
  clearSessionToken,
  getSessionToken,
  setSessionToken,
  subscribeSession,
} from "./auth/session";
import { HttpAxonWallClient } from "./api/httpClient";
import { useStore } from "./store/useStore";
import { applyTheme, loadTheme, storeTheme, type Theme } from "./theme";

// The app container is the only stateful wiring point: it owns the session
// (login/logout) and subscribes to the ConfigStore, handing pages pure
// props. Pages hold only local editor state; no component body fetches or
// reads global state.

interface Session {
  readonly token: string | undefined;
  /** Bumps on every login so the console remounts with a fresh store. */
  readonly epoch: number;
}

function currentSession(previous: Session): Session {
  const token = getSessionToken();
  if (token === previous.token) return previous;
  return {
    token,
    // Rotation (token → token) keeps the epoch: the console stays mounted
    // and the client picks the new token up on its next call.
    epoch:
      previous.token === undefined && token !== undefined
        ? previous.epoch + 1
        : previous.epoch,
  };
}

export function App(): ReactElement {
  const [session, setSession] = useState<Session>(() =>
    currentSession({ token: undefined, epoch: 0 }),
  );
  const [page, setPage] = useState<PageId>("dashboard");
  const [theme, setTheme] = useState(loadTheme);

  useEffect(
    () =>
      subscribeSession(() => {
        setSession((prev) => currentSession(prev));
      }),
    [],
  );

  const clearSession = useCallback(() => clearSessionToken(), []);

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = current === "dark" ? "light" : "dark";
      storeTheme(next);
      return next;
    });
  }, []);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  if (session.token === undefined) {
    return (
      <div className="axw-login-wrap">
        <LoginPage onLogin={setSessionToken} />
      </div>
    );
  }

  return (
    <SetupGate>
      <Console
        key={session.epoch}
        page={page}
        onNavigate={setPage}
        theme={theme}
        onToggleTheme={toggleTheme}
        onSignOut={clearSession}
      />
    </SetupGate>
  );
}

/**
 * First-boot gate: while setup is pending on the appliance, the wizard
 * replaces the console. The client reads the session token per call, so
 * the wizard's token rotation is picked up mid-flow without a remount.
 */
function SetupGate({
  children,
}: {
  readonly children: ReactElement;
}): ReactElement {
  const client = useMemo(
    () => new HttpAxonWallClient({ getToken: getSessionToken }),
    [],
  );
  const [setup, setSetup] = useState<"checking" | "required" | "ready">(
    "checking",
  );
  const [error, setError] = useState<string>();

  useEffect(() => {
    if (setup !== "checking") return;
    let cancelled = false;
    client
      .getSetupState()
      .then((state) => {
        if (!cancelled) setSetup(state.required ? "required" : "ready");
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [client, setup]);

  if (setup === "checking") {
    return (
      <div className="axw-content">
        {error !== undefined ? (
          <>
            <Alert
              severity="error"
              title="Failed to reach the appliance"
              message={error}
            />
            <div>
              <Button variant="primary" onClick={() => setSetup("checking")}>
                Retry
              </Button>
            </div>
          </>
        ) : (
          <Spinner label="Checking appliance state…" />
        )}
      </div>
    );
  }
  if (setup === "required") {
    return (
      <SetupWizardPage client={client} onFinished={() => setSetup("ready")} />
    );
  }
  return children;
}

interface ConsoleProps {
  readonly page: PageId;
  readonly onNavigate: (page: PageId) => void;
  readonly theme: Theme;
  readonly onToggleTheme: () => void;
  readonly onSignOut: () => void;
}

function Console({
  page,
  onNavigate,
  theme,
  onToggleTheme,
  onSignOut,
}: ConsoleProps): ReactElement {
  const store = useStore(onSignOut);

  return (
    <AppShell
      page={page}
      onNavigate={onNavigate}
      theme={theme}
      onToggleTheme={onToggleTheme}
      onSignOut={onSignOut}
      revision={store.revision}
    >
      {store.phase === "loading" && <Spinner label="Loading configuration…" />}
      {store.phase === "error" && (
        <div className="axw-content">
          <Alert
            severity="error"
            title="Failed to reach the appliance"
            message={store.loadError}
          />
          <div>
            <Button variant="primary" onClick={store.reload}>
              Retry
            </Button>
          </div>
        </div>
      )}
      {store.phase === "ready" && store.config !== undefined && (
        <div className="axw-content">
          {store.saveError !== undefined && (
            <Alert
              severity="error"
              title="Change rejected — config rolled back"
              message={store.saveError}
              onDismiss={store.dismissSaveError}
            />
          )}
          {page === "dashboard" && (
            <DashboardPage config={store.config} status={store.status} />
          )}
          {page === "firewall" && (
            <FirewallPage
              config={store.config}
              saving={store.saving}
              onChange={store.changeConfig}
            />
          )}
          {page === "nat" && (
            <NatPage
              config={store.config}
              saving={store.saving}
              onChange={store.changeConfig}
            />
          )}
          {page === "aliases" && (
            <AliasesPage
              config={store.config}
              saving={store.saving}
              onChange={store.changeConfig}
            />
          )}
          {page === "services" && (
            <ServicesPage
              config={store.config}
              saving={store.saving}
              onChange={store.changeConfig}
            />
          )}
          {page === "status" && (
            <StatusPage
              status={store.status}
              onRefresh={store.refreshStatus}
              refreshError={store.statusRefreshError}
            />
          )}
          {page === "backup" && (
            <BackupPage
              config={store.config}
              saving={store.saving}
              onChange={store.changeConfig}
              client={store.client}
              onRestored={store.reload}
            />
          )}
        </div>
      )}
    </AppShell>
  );
}
