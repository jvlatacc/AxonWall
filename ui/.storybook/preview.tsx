import type { Decorator, Preview } from '@storybook/react'
import { createElement } from 'react'
import '../src/styles/tokens.css'
import '../src/styles/app.css'

// Theme toolbar: stories render in both themes without touching the
// components themselves — the tokens flip on the html element.
const withTheme: Decorator = (Story, context) => {
  const dark = context.globals.theme === 'dark'
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.classList.toggle('light', !dark)
  return createElement(Story)
}

const preview: Preview = {
  globalTypes: {
    theme: {
      description: 'Color theme',
      toolbar: {
        title: 'Theme',
        items: [
          { value: 'dark', title: 'Dark' },
          { value: 'light', title: 'Light' },
        ],
        dynamicTitle: true,
      },
    },
  },
  initialGlobals: { theme: 'dark' },
  decorators: [withTheme],
  parameters: {
    layout: 'fullscreen',
    backgrounds: { disable: true },
  },
}

export default preview
