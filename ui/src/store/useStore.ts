import { useEffect, useMemo, useState } from 'react'
import type { AxonWallConfig, SystemStatus } from '../api/types'
import type { ConfigStore } from './ConfigStore'
import { createAppStore } from './storeFactory'

export interface StoreState {
  readonly phase: 'loading' | 'ready' | 'error'
  readonly config: AxonWallConfig | undefined
  readonly status: SystemStatus | undefined
  readonly revision: string | undefined
  readonly saving: boolean
  readonly loadError: string | undefined
  readonly saveError: string | undefined
}

export interface StoreApi extends StoreState {
  /** Route a config change through the store (save → rollback on reject). */
  readonly changeConfig: (next: AxonWallConfig) => void
  readonly dismissSaveError: () => void
  readonly reload: () => void
}

/** Subscribe the current component to the application ConfigStore. */
export function useStore(): StoreApi {
  const store: ConfigStore = useMemo(() => createAppStore(), [])
  const [state, setState] = useState<StoreState>(store.getState)

  useEffect(() => {
    const unsubscribe = store.subscribe(() => {
      setState(store.getState())
    })
    setState(store.getState())
    void store.load()
    return unsubscribe
  }, [store])

  return {
    phase: state.phase,
    config: state.config,
    status: state.status,
    revision: state.revision,
    saving: state.saving,
    loadError: state.loadError,
    saveError: state.saveError,
    changeConfig: (next) => {
      void store.save(next)
    },
    dismissSaveError: store.dismissSaveError,
    reload: () => {
      void store.load()
    },
  }
}
