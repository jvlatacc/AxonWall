// The single store instance lives in the app container (created per mount,
// never a module singleton) and is the only thing that talks to the client.
// Components receive plain data and callbacks as props.

import { ApiError, type AxonWallClient } from "../api/client";
import type { AxonWallConfig, SystemStatus } from "../api/types";

export interface ConfigState {
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

const INITIAL_STATE: ConfigState = {
  phase: "loading",
  config: undefined,
  status: undefined,
  revision: undefined,
  saving: false,
  loadError: undefined,
  saveError: undefined,
  statusRefreshError: undefined,
};

/**
 * Holds the loaded config + status, applies changes through the client, and
 * rolls an optimistic update back when the appliance rejects it. Listeners
 * are wired into React via useSyncExternalStore.
 */
export class ConfigStore {
  /**
   * `onUnauthorized` fires when the appliance rejects the session's token
   * (401) — the app layer clears the session and shows the login page.
   */
  static create(
    client: AxonWallClient,
    onUnauthorized?: () => void,
  ): ConfigStore {
    return new ConfigStore(client, onUnauthorized);
  }

  private constructor(
    private readonly client: AxonWallClient,
    private readonly onUnauthorized?: () => void,
  ) {}

  private state: ConfigState = INITIAL_STATE;
  private readonly listeners = new Set<() => void>();

  getState = (): ConfigState => this.state;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  /** Load config and status from the client. */
  load = async (): Promise<void> => {
    this.set({ phase: "loading", loadError: undefined });
    try {
      const [{ config, revision }, status] = await Promise.all([
        this.client.getConfig(),
        this.client.getStatus(),
      ]);
      this.set({
        phase: "ready",
        config,
        revision,
        status,
        loadError: undefined,
      });
    } catch (err) {
      if (isUnauthorized(err)) {
        this.onUnauthorized?.();
        return;
      }
      this.set({ phase: "error", loadError: errorMessage(err) });
    }
  };

  /**
   * Apply a new config document optimistically; on rejection, restore the
   * previous document and surface the appliance's error.
   */
  save = async (next: AxonWallConfig): Promise<void> => {
    const { config, revision } = this.state;
    if (config === undefined || revision === undefined) return;
    this.set({ config: next, saving: true, saveError: undefined });
    try {
      const { revision: newRevision } = await this.client.putConfig(
        next,
        revision,
      );
      this.set({
        config: next,
        revision: newRevision,
        saving: false,
        saveError: undefined,
      });
    } catch (err) {
      if (isUnauthorized(err)) {
        this.onUnauthorized?.();
        return;
      }
      this.set({ config, saving: false, saveError: errorMessage(err) });
    }
  };

  /**
   * Refresh the runtime status in place — no loading phase, config and
   * revision untouched. Used by the status page's live refresh.
   */
  refreshStatus = async (): Promise<void> => {
    try {
      const status = await this.client.getStatus();
      this.set({ status, statusRefreshError: undefined });
    } catch (err) {
      if (isUnauthorized(err)) {
        this.onUnauthorized?.();
        return;
      }
      this.set({ statusRefreshError: errorMessage(err) });
    }
  };

  dismissSaveError = (): void => {
    this.set({ saveError: undefined });
  };

  private set(patch: Partial<ConfigState>): void {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener();
  }
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}
