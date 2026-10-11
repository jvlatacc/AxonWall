import type { Meta, StoryObj } from '@storybook/react-vite'
import { AliasesPage } from './AliasesPage'
import { sampleConfig } from '../fixtures/fixtures'

const meta = { component: AliasesPage } satisfies Meta<typeof AliasesPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { config: sampleConfig(), saving: false, onChange: () => {} },
}

export const NoAliases: Story = {
  args: {
    config: { ...sampleConfig(), firewall: { ...sampleConfig().firewall, aliases: {} } },
    saving: false,
    onChange: () => {},
  },
}
