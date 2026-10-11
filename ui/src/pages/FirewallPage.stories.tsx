import type { Meta, StoryObj } from '@storybook/react-vite'
import { FirewallPage } from './FirewallPage'
import { sampleConfig } from '../fixtures/fixtures'

const meta = { component: FirewallPage } satisfies Meta<typeof FirewallPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { config: sampleConfig(), saving: false, onChange: () => {} },
}

export const WhileSaving: Story = {
  args: { config: sampleConfig(), saving: true, onChange: () => {} },
}

export const NoRules: Story = {
  args: {
    config: { ...sampleConfig(), firewall: { ...sampleConfig().firewall, rules: [] } },
    saving: false,
    onChange: () => {},
  },
}
