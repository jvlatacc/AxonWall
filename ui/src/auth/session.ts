// Session token storage: memory-first, mirrored to sessionStorage so a
// page reload in the same tab keeps the session, and a tab close drops it.
// Deliberately never localStorage — a firewall console's credentials do not
// belong in persistent browser storage.

const STORAGE_KEY = 'axonwall.session-token'

let current: string | undefined
let initialized = false

const listeners = new Set<() => void>()

function initFromStorage(): void {
  if (initialized) return
  initialized = true
  try {
    const stored = sessionStorage.getItem(STORAGE_KEY)
    if (stored !== null && stored !== '') current = stored
  } catch {
    // Storage unavailable (privacy mode, tests): memory-only session.
  }
}

function persist(token: string | undefined): void {
  try {
    if (token === undefined) sessionStorage.removeItem(STORAGE_KEY)
    else sessionStorage.setItem(STORAGE_KEY, token)
  } catch {
    // Memory-only fallback; the in-memory value is authoritative.
  }
}

function notify(): void {
  for (const listener of listeners) listener()
}

export function getSessionToken(): string | undefined {
  initFromStorage()
  return current
}

export function setSessionToken(token: string): void {
  current = token
  persist(token)
  notify()
}

/** Replaces the token in place (auth rotation) without clearing the session. */
export function replaceSessionToken(token: string): void {
  setSessionToken(token)
}

export function clearSessionToken(): void {
  const wasSet = current !== undefined
  current = undefined
  persist(undefined)
  if (wasSet) notify()
}

export function subscribeSession(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}
