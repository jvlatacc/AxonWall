// The single store instance lives in the app container (created per mount,
// never a module singleton) and is the only thing that talks to the client.
// Components receive plain data and callbacks as props.

import type { AxonWallClient } from '../api/client'
import type { AxonWallConfig, SystemStatus } from '../api/types'

export interface ConfigState {
  readonly phase: 'loading' | 'ready' | 'error'
  readonly config: AxonWallConfig | undefined
  readonly status: SystemStatus | undefined
  readonly revision: string | undefined
  readonly saving: boolean
  readonly loadError: string | undefined
  readonly saveError: string | undefined
}

const INITIAL_STATE: ConfigState = {
  phase: 'loading',
  config: undefined,
  status: undefined,
  revision: undefined,
  saving: false,
  loadError: undefined,
  saveError: undefined,
}

/**
 * Holds the loaded config + status, applies changes through the client, and
 * rolls an optimistic update back when the appliance rejects it. Listeners
 * are wired into React via useSyncExternalStore.
 */
export class ConfigStore {
  static create(client: AxonWallClient): ConfigStore {
    return new ConfigStore(client)
  }

  private constructor(private readonly client: AxonWallClient) {}

  private state: ConfigState = INITIAL_STATE
  private readonly listeners = new Set<() => void>()

  getState = (): ConfigState => this.state

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** Load config and status from the client. */
  load = async (): Promise<void> => {
    this.set({ phase: 'loading', loadError: undefined })
    try {
      const [{ config, revision }, status] = await Promise.all([
        this.client.getConfig(),
        this.client.getStatus(),
      ])
      this.set({ phase: 'ready', config, revision, status, loadError: undefined })
    } catch (err) {
      this.set({ phase: 'error', loadError: errorMessage(err) })
    }
  }

  /**
   * Apply a new config document optimistically; on rejection, restore the
   * previous document and surface the appliance's error.
   */
  save = async (next: AxonWallConfig): Promise<void> => {
    const { config, revision } = this.state
    if (config === undefined || revision === undefined) return
    this.set({ config: next, saving: true, saveError: undefined })
    try {
      const { revision: newRevision } = await this.client.putConfig(next, revision)
      this.set({ config: next, revision: newRevision, saving: false, saveError: undefined })
    } catch (err) {
      this.set({ config, saving: false, saveError: errorMessage(err) })
    }
  }

  dismissSaveError = (): void => {
    this.set({ saveError: undefined })
  }

  private set(patch: Partial<ConfigState>): void {
    this.state = { ...this.state, ...patch }
    for (const listener of this.listeners) listener()
  }
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
