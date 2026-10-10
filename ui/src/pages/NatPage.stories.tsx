import type { Meta, StoryObj } from '@storybook/react-vite'
import { NatPage } from './NatPage'
import { sampleConfig } from '../fixtures/fixtures'

const meta = { component: NatPage } satisfies Meta<typeof NatPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { config: sampleConfig(), saving: false, onChange: () => {} },
}

export const NoNatRules: Story = {
  args: {
    config: { ...sampleConfig(), firewall: { ...sampleConfig().firewall, nat: [] } },
    saving: false,
    onChange: () => {},
  },
}
