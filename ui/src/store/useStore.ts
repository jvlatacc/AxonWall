import { useEffect, useMemo, useState } from "react";
import type { AxonWallClient } from "../api/client";
import type { AxonWallConfig, SystemStatus } from "../api/types";
import type { ConfigStore } from "./ConfigStore";
import { createAppClient, createAppStore } from "./storeFactory";

export interface StoreState {
  readonly phase: "loading" | "ready" | "error";
  readonly config: AxonWallConfig | undefined;
  readonly status: SystemStatus | undefined;
  readonly revision: string | undefined;
  readonly saving: boolean;
  readonly loadError: string | undefined;
  readonly saveError: string | undefined;
  /** Set when a background status refresh fails; the console stays usable. */
  readonly statusRefreshError: string | undefined;
}

export interface StoreApi extends StoreState {
  /** Route a config change through the store (save → rollback on reject). */
  readonly changeConfig: (next: AxonWallConfig) => void;
  readonly dismissSaveError: () => void;
  readonly reload: () => void;
  /** Refresh the runtime status in place (status page live view). */
  readonly refreshStatus: () => void;
  readonly statusRefreshError: string | undefined;
  /** The live API client — for direct calls like backup archive transfer. */
  readonly client: AxonWallClient;
}

/** Subscribe the current component to the application ConfigStore. */
export function useStore(
  onUnauthorized?: () => void,
  client?: AxonWallClient,
): StoreApi {
  const resolvedClient: AxonWallClient = useMemo(
    () => client ?? createAppClient(),
    [client],
  );
  const store: ConfigStore = useMemo(
    () => createAppStore(onUnauthorized, resolvedClient),
    [onUnauthorized, resolvedClient],
  );
  const [state, setState] = useState<StoreState>(store.getState);

  useEffect(() => {
    const unsubscribe = store.subscribe(() => {
      setState(store.getState());
    });
    setState(store.getState());
    void store.load();
    return unsubscribe;
  }, [store]);

  return {
    phase: state.phase,
    config: state.config,
    status: state.status,
    revision: state.revision,
    saving: state.saving,
    loadError: state.loadError,
    saveError: state.saveError,
    changeConfig: (next) => {
      void store.save(next);
    },
    dismissSaveError: store.dismissSaveError,
    reload: () => {
      void store.load();
    },
    refreshStatus: () => {
      void store.refreshStatus();
    },
    statusRefreshError: state.statusRefreshError,
    client: resolvedClient,
  };
}
