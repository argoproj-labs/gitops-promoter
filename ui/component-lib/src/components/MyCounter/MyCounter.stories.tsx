import type { Meta, StoryObj } from '@storybook/react'
import { fn } from '@storybook/test'

import { MyCounter } from '.'

const meta = {
  title: 'Components/MyCounter',
  component: MyCounter,
  parameters: {
    layout: 'centered',
  },
  tags: ['autodocs'],
  args: {
    initialValue: 0,
    step: 1,
    onClick: fn(),
  },
} satisfies Meta<typeof MyCounter>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}

export const WithMinMax: Story = {
  args: {
    min: 0,
    max: 10,
  },
}

export const CustomStep: Story = {
  args: {
    step: 5,
  },
}
