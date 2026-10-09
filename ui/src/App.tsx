import { useCallback, useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import { AppShell } from './components/layout/AppShell'
import { Alert } from './components/ui/Alert'
import { Button } from './components/ui/Button'
import { Spinner } from './components/ui/Spinner'
import { AliasesPage } from './pages/AliasesPage'
import { BackupPage } from './pages/BackupPage'
import { DashboardPage } from './pages/DashboardPage'
import { FirewallPage } from './pages/FirewallPage'
import { NatPage } from './pages/NatPage'
import { ServicesPage } from './pages/ServicesPage'
import { StatusPage } from './pages/StatusPage'
import type { PageId } from './nav'
import { useStore } from './store/useStore'
import { applyTheme, loadTheme, storeTheme } from './theme'

// The app container is the only stateful wiring point: it subscribes to the
// ConfigStore and hands pages pure props. Pages hold only local editor
// state; no component body fetches or reads global state.

export function App(): ReactElement {
  const store = useStore()
  const [page, setPage] = useState<PageId>('dashboard')
  const [theme, setTheme] = useState(loadTheme)

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = current === 'dark' ? 'light' : 'dark'
      storeTheme(next)
      return next
    })
  }, [])

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  return (
    <AppShell
      page={page}
      onNavigate={setPage}
      theme={theme}
      onToggleTheme={toggleTheme}
      revision={store.revision}
    >
      {store.phase === 'loading' && <Spinner label="Loading configuration…" />}
      {store.phase === 'error' && (
        <div className="axw-content">
          <Alert severity="error" title="Failed to reach the appliance" message={store.loadError} />
          <div>
            <Button variant="primary" onClick={store.reload}>
              Retry
            </Button>
          </div>
        </div>
      )}
      {store.phase === 'ready' && store.config !== undefined && (
        <div className="axw-content">
          {store.saveError !== undefined && (
            <Alert
              severity="error"
              title="Change rejected — config rolled back"
              message={store.saveError}
              onDismiss={store.dismissSaveError}
            />
          )}
          {page === 'dashboard' && <DashboardPage config={store.config} status={store.status} />}
          {page === 'firewall' && (
            <FirewallPage config={store.config} saving={store.saving} onChange={store.changeConfig} />
          )}
          {page === 'nat' && (
            <NatPage config={store.config} saving={store.saving} onChange={store.changeConfig} />
          )}
          {page === 'aliases' && (
            <AliasesPage config={store.config} saving={store.saving} onChange={store.changeConfig} />
          )}
          {page === 'services' && (
            <ServicesPage config={store.config} saving={store.saving} onChange={store.changeConfig} />
          )}
          {page === 'status' && <StatusPage status={store.status} />}
          {page === 'backup' && (
            <BackupPage config={store.config} saving={store.saving} onChange={store.changeConfig} />
          )}
        </div>
      )}
    </AppShell>
  )
}
