import type { Meta, StoryObj } from '@storybook/react-vite'
import { AppShell } from './AppShell'
import { DashboardPage } from '../../pages/DashboardPage'
import { sampleConfig, sampleStatus } from '../../fixtures/fixtures'

const meta = {
  component: AppShell,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof AppShell>
export default meta
type Story = StoryObj<typeof meta>

export const Shell: Story = {
  args: {
    page: 'dashboard',
    onNavigate: () => {},
    theme: 'dark',
    onToggleTheme: () => {},
    revision: 'rev-000042',
    children: <DashboardPage config={sampleConfig()} status={sampleStatus()} />,
  },
}
