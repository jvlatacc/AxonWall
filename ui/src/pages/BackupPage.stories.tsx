import type { Meta, StoryObj } from '@storybook/react-vite'
import { BackupPage } from './BackupPage'
import { sampleConfig } from '../fixtures/fixtures'

const meta = { component: BackupPage } satisfies Meta<typeof BackupPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { config: sampleConfig(), saving: false, onChange: () => {} },
}
