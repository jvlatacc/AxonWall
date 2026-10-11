import type { Meta, StoryObj } from '@storybook/react-vite'
import { StatusPage } from './StatusPage'
import { sampleStatus } from '../fixtures/fixtures'

const meta = { component: StatusPage } satisfies Meta<typeof StatusPage>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { status: sampleStatus() },
}

export const NotReported: Story = {
  args: { status: undefined },
}
