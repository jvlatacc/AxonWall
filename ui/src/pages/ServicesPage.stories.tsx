import type { Meta, StoryObj } from '@storybook/react-vite'
import { ServicesPage } from './ServicesPage'
import { sampleConfig } from '../fixtures/fixtures'
import type { AxonWallConfig } from '../api/types'

/** Same document with the whole services section absent (everything off). */
function withoutServices(config: AxonWallConfig): AxonWallConfig {
  return {
    version: config.version,
    zones: config.zones,
    interfaces: config.interfaces,
    firewall: config.firewall,
  }
}

const meta = { component: ServicesPage } satisfies Meta<typeof ServicesPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { config: sampleConfig(), saving: false, onChange: () => {} },
}

export const ServicesOff: Story = {
  args: { config: withoutServices(sampleConfig()), saving: false, onChange: () => {} },
}
