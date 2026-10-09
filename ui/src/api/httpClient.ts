// HTTP-backed AxonWallClient against a running axond. Same-origin by
// default: axond serves this UI bundle over TLS on the LAN, so the console
// and the API share one origin and one TLS certificate. The bearer token
// is never stored here — it is read from the injected TokenSource on every
// call, so a rotation takes effect without recreating the client.

import type { AxonWallConfig, ConfigRevision, NetDevice, SetupState, SystemStatus } from './types'
import { configFromYaml, configToYaml } from './yaml'
import { ApiError } from './client'

/** Reads the current session token; undefined = unauthenticated. */
export type TokenSource = () => string | undefined

export interface HttpAxonWallClientOptions {
  /** API base URL; '' (default) = same origin. Overridable for tests. */
  readonly baseUrl?: string
  readonly getToken: TokenSource
  /** fetch implementation; defaults to globalThis.fetch. Test seam. */
  readonly fetchImpl?: typeof fetch
}

const REVISION_HEADER = 'X-AxonWall-Revision'

export class HttpAxonWallClient {
  private readonly baseUrl: string
  private readonly getToken: TokenSource
  private readonly fetchImpl: typeof fetch

  constructor(options: HttpAxonWallClientOptions) {
    this.baseUrl = options.baseUrl ?? ''
    this.getToken = options.getToken
    this.fetchImpl = options.fetchImpl ?? fetch
  }

  async getConfig(): Promise<ConfigRevision> {
    const res = await this.request('GET', '/config')
    const revision = res.headers.get(REVISION_HEADER)
    if (revision === null || revision === '') {
      throw new ApiError(res.status, 'appliance did not report a config revision')
    }
    const text = await res.text()
    return { config: configFromYaml(text), revision }
  }

  async putConfig(config: AxonWallConfig, expectedRevision: string): Promise<ConfigRevision> {
    const res = await this.request('PUT', '/config', {
      body: configToYaml(config),
      headers: { 'Content-Type': 'application/yaml', [REVISION_HEADER]: expectedRevision },
    })
    const body = await errorOrJson(res)
    return { config, revision: String(body.revision ?? '') }
  }

  async getStatus(): Promise<SystemStatus> {
    const res = await this.request('GET', '/status')
    return (await res.json()) as SystemStatus
  }

  async getSetupState(): Promise<SetupState> {
    const res = await this.request('GET', '/setup')
    return (await res.json()) as SetupState
  }

  async completeSetup(): Promise<SetupState> {
    const res = await this.request('POST', '/setup/complete')
    return (await res.json()) as SetupState
  }

  async rotateToken(token: string): Promise<{ rotated: boolean; persisted: boolean }> {
    const res = await this.request('POST', '/auth/token', {
      body: JSON.stringify({ token }),
      headers: { 'Content-Type': 'application/json' },
    })
    return (await res.json()) as { rotated: boolean; persisted: boolean }
  }

  async listNetDevices(): Promise<readonly NetDevice[]> {
    const res = await this.request('GET', '/net/devices')
    return (await res.json()) as NetDevice[]
  }

  async exportBackup(): Promise<Blob> {
    const res = await this.request('GET', '/backup/export')
    return await res.blob()
  }

  async restoreBackup(archive: Blob): Promise<{ revision: string }> {
    const res = await this.request('POST', '/backup/restore', {
      body: archive,
      headers: { 'Content-Type': 'application/x-tar' },
    })
    const body = await errorOrJson(res)
    return { revision: String(body.revision ?? '') }
  }

  /** Raw request with bearer auth and uniform error translation. */
  private async request(
    method: 'GET' | 'PUT' | 'POST',
    path: string,
    init: { body?: string | Blob; headers?: Record<string, string> } = {},
  ): Promise<Response> {
    const token = this.getToken()
    let res: Response
    try {
      res = await this.fetchImpl(`${this.baseUrl}${path}`, {
        method,
        headers: {
          ...(token !== undefined ? { Authorization: `Bearer ${token}` } : {}),
          ...init.headers,
        },
        body: init.body,
      })
    } catch {
      throw new ApiError(0, 'cannot reach the appliance — check the network and address')
    }
    if (!res.ok) throw await apiErrorFrom(res)
    return res
  }
}

/**
 * Probe used by the login flow: verifies a token by reading the config and
 * returns the config revision on success. Throws ApiError (401) on a bad
 * token — the same translation the wired client uses.
 */
export async function verifyToken(baseUrl: string, token: string): Promise<ConfigRevision> {
  const client = new HttpAxonWallClient({ baseUrl, getToken: () => token })
  return client.getConfig()
}

async function apiErrorFrom(res: Response): Promise<ApiError> {
  let message = `${res.status} ${res.statusText}`
  try {
    const body = (await res.json()) as { error?: string; issues?: string[] }
    if (typeof body.error === 'string' && body.error !== '') {
      message = body.issues !== undefined && body.issues.length > 0
        ? `${body.error}: ${body.issues.join('; ')}`
        : body.error
    }
  } catch {
    // Non-JSON error body: the status line is the message.
  }
  return new ApiError(res.status, message)
}

async function errorOrJson(res: Response): Promise<{ revision?: unknown }> {
  try {
    return (await res.json()) as { revision?: unknown }
  } catch {
    return {}
  }
}
