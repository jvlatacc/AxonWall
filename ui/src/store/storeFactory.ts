import { MockAxonWallClient } from '../api/client'
import { ConfigStore } from './ConfigStore'

/**
 * Production wiring for the ConfigStore. Swapping the mock for the real
 * HTTP-backed client is a one-line change here — every surface keeps
 * consuming props.
 */
export function createAppStore(): ConfigStore {
  return ConfigStore.create(new MockAxonWallClient())
}
