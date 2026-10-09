// The typed API boundary. Components never fetch: the app container owns a
// client instance, feeds a ConfigStore with it, and pages receive data and
// change callbacks as props (AxonWall spec — management plane, UI-first
// sequencing with a mocked client).
//
// The mock client below is the only implementation in this task; the HTTP
// client against axond (GET/PUT /config with bearer token and the
// X-AxonWall-Revision header, YAML body) implements the same interface when
// the backend integration wave lands.

import type {
  AxonWallConfig,
  ConfigRevision,
  SystemStatus,
} from './types'
import { SCHEMA_VERSION } from './types'
import { sampleConfig, sampleStatus } from '../fixtures/fixtures'

/** Error surfaced by client implementations for failed API calls. */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export interface AxonWallClient {
  /** Load the current config document and its store revision. */
  getConfig(): Promise<ConfigRevision>
  /**
   * Replace the config document. `expectedRevision` guards optimistic
   * concurrency: a mismatch rejects the write so a stale editor cannot
   * clobber a newer revision.
   */
  putConfig(config: AxonWallConfig, expectedRevision: string): Promise<ConfigRevision>
  /** Runtime status snapshot (interfaces, services, states, log). */
  getStatus(): Promise<SystemStatus>
}

export interface MockClientOptions {
  readonly config?: AxonWallConfig
  readonly status?: SystemStatus
  /** Simulated round-trip latency in ms; 0 for tests. */
  readonly latencyMs?: number
}

/**
 * In-memory client backed by fixture data. Returns deep clones so callers
 * can never mutate fixture state through a returned reference, and bumps
 * the revision on every accepted write, mirroring the git-backed store.
 */
export class MockAxonWallClient implements AxonWallClient {
  private currentConfig: AxonWallConfig
  private currentStatus: SystemStatus
  private revisionCounter = 42
  private revision: string
  private readonly latencyMs: number

  constructor(options: MockClientOptions = {}) {
    this.currentConfig = structuredClone(options.config ?? sampleConfig())
    this.currentStatus = structuredClone(options.status ?? sampleStatus())
    this.latencyMs = options.latencyMs ?? 120
    this.revision = `rev-${String(this.revisionCounter).padStart(6, '0')}`
  }

  async getConfig(): Promise<ConfigRevision> {
    await this.delay()
    return { config: structuredClone(this.currentConfig), revision: this.revision }
  }

  async putConfig(config: AxonWallConfig, expectedRevision: string): Promise<ConfigRevision> {
    await this.delay()
    if (expectedRevision !== this.revision) {
      throw new ApiError(409, 'config changed on the appliance — reload and retry')
    }
    if (config.version !== SCHEMA_VERSION) {
      throw new ApiError(400, `unsupported schema version ${String(config.version)}`)
    }
    this.currentConfig = structuredClone(config)
    this.revisionCounter += 1
    this.revision = `rev-${String(this.revisionCounter).padStart(6, '0')}`
    return { config: structuredClone(this.currentConfig), revision: this.revision }
  }

  async getStatus(): Promise<SystemStatus> {
    await this.delay()
    return structuredClone(this.currentStatus)
  }

  private delay(): Promise<void> {
    return this.latencyMs > 0
      ? new Promise((resolve) => setTimeout(resolve, this.latencyMs))
      : Promise.resolve()
  }
}
