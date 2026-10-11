// Renders every story of every surface from its own props: the isolation
// guarantee the brief demands, exercised mechanically. If a page starts
// reaching for the API client or module state, its stories stop rendering.
import { createElement } from 'react'
import type { ComponentType } from 'react'
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import * as appShell from './components/layout/AppShell.stories'
import * as dashboard from './pages/DashboardPage.stories'
import * as firewall from './pages/FirewallPage.stories'
import * as nat from './pages/NatPage.stories'
import * as aliases from './pages/AliasesPage.stories'
import * as services from './pages/ServicesPage.stories'
import * as status from './pages/StatusPage.stories'
import * as backup from './pages/BackupPage.stories'

interface StoryModule {
  default: { component?: unknown }
  [storyName: string]: unknown
}

const modules: Record<string, StoryModule> = {
  'Shell/AppShell': appShell,
  'Pages/Dashboard': dashboard,
  'Pages/Firewall': firewall,
  'Pages/NAT': nat,
  'Pages/Aliases': aliases,
  'Pages/Services': services,
  'Pages/Status': status,
  'Pages/Backup': backup,
}

describe('story smoke tests', () => {
  for (const [surface, mod] of Object.entries(modules)) {
    it(`renders every story of ${surface}`, () => {
      // The story registry erases page prop types on purpose — pages are
      // rendered from plain args objects here. The cast lives at this one
      // render site, never in the stories themselves.
      const component = mod.default.component as unknown as
        | ComponentType<Record<string, unknown>>
        | undefined
      expect(component, `${surface} meta needs a component`).toBeDefined()
      const storyNames = Object.keys(mod).filter((k) => k !== 'default')
      expect(storyNames.length, `${surface} needs at least one story`).toBeGreaterThan(0)
      for (const storyName of storyNames) {
        const args = (mod[storyName] as unknown as { args?: Record<string, unknown> }).args ?? {}
        const { container } = render(createElement(component!, args))
        expect(container.firstChild, `${surface}/${storyName} rendered nothing`).not.toBeNull()
      }
    })
  }
})
