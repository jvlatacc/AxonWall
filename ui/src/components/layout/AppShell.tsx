import type { ReactElement, ReactNode } from 'react'
import { NAV_ITEMS } from '../../nav'
import type { PageId } from '../../nav'
import type { Theme } from '../../theme'

export interface AppShellProps {
  /** Currently active page id. */
  readonly page: PageId
  readonly onNavigate: (page: PageId) => void
  readonly theme: Theme
  readonly onToggleTheme: () => void
  /** Config revision chip, e.g. "rev-000042". */
  readonly revision?: string
  readonly children: ReactNode
}

export function AppShell({
  page,
  onNavigate,
  theme,
  onToggleTheme,
  revision,
  children,
}: AppShellProps): ReactElement {
  return (
    <div className="axw-shell">
      <aside className="axw-sidebar">
        <div className="axw-brand">
          <span className="axw-brand-mark" aria-hidden="true" />
          <span className="axw-brand-name">AxonWall</span>
        </div>
        <nav aria-label="Main navigation">
          <ul className="axw-nav">
            {NAV_ITEMS.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  className={`axw-nav-item${page === item.id ? ' is-active' : ''}`}
                  aria-current={page === item.id ? 'page' : undefined}
                  onClick={() => onNavigate(item.id)}
                >
                  {item.label}
                </button>
              </li>
            ))}
          </ul>
        </nav>
        <div className="axw-sidebar-footer">
          <button type="button" className="axw-btn axw-btn-ghost axw-btn-sm" onClick={onToggleTheme}>
            {theme === 'dark' ? 'Light mode' : 'Dark mode'}
          </button>
          {revision !== undefined && <span className="axw-revision-chip">{revision}</span>}
        </div>
      </aside>
      <main className="axw-shell-main">{children}</main>
    </div>
  )
}
