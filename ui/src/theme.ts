// Theme handling. The tokens flip on the html element's class; persistence
// is best-effort because localStorage can be unavailable (privacy modes).

export type Theme = 'light' | 'dark'

const THEME_KEY = 'axonwall.theme'

export function loadTheme(): Theme {
  let stored: string | null = null
  try {
    stored = localStorage.getItem(THEME_KEY)
  } catch {
    // Storage unavailable (privacy mode / disabled) — fall through to the
    // media query below.
    stored = null
  }
  if (stored === 'light' || stored === 'dark') return stored
  const prefersLight =
    typeof window !== 'undefined' &&
    window.matchMedia !== undefined &&
    window.matchMedia('(prefers-color-scheme: light)').matches
  return prefersLight ? 'light' : 'dark'
}

export function applyTheme(theme: Theme): void {
  const root = document.documentElement
  root.classList.toggle('dark', theme === 'dark')
  root.classList.toggle('light', theme === 'light')
}

export function storeTheme(theme: Theme): void {
  try {
    localStorage.setItem(THEME_KEY, theme)
  } catch {
    // Persistence is best-effort; the toggle still works for the session.
  }
}
