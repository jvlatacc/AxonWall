import type { Meta, StoryObj } from '@storybook/react-vite'
import { DashboardPage } from './DashboardPage'
import { sampleConfig, sampleStatus } from '../fixtures/fixtures'

const meta = { component: DashboardPage } satisfies Meta<typeof DashboardPage>
export default meta
type Story = StoryObj<typeof meta>

export const WithStatus: Story = {
  args: { config: sampleConfig(), status: sampleStatus() },
}

export const StatusPending: Story = {
  args: { config: sampleConfig(), status: undefined },
}
