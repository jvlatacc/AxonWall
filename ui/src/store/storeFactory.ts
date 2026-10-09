import type { AxonWallClient } from "../api/client";
import { HttpAxonWallClient } from "../api/httpClient";
import { getSessionToken } from "../auth/session";
import { ConfigStore } from "./ConfigStore";

/**
 * Production wiring for the ConfigStore: the live HTTP client against
 * axond (same-origin — axond serves this bundle over TLS on the LAN). The
 * mock client remains available to stories and tests.
 */
export function createAppClient(): AxonWallClient {
  return new HttpAxonWallClient({ getToken: getSessionToken });
}

export function createAppStore(
  onUnauthorized?: () => void,
  client?: AxonWallClient,
): ConfigStore {
  return ConfigStore.create(client ?? createAppClient(), onUnauthorized);
}
