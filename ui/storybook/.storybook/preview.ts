import type { Preview } from '@storybook/react-vite';

import '@lib/styles/theme.scss';
import '@lib/styles/main.scss';
import './preview.scss';

const preview: Preview = {
  globalTypes: {
    theme: {
      description: 'Color theme',
      toolbar: {
        title: 'Theme',
        icon: 'mirror',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
  initialGlobals: { theme: 'light' },
  decorators: [
    (Story, context) => {
      // Same class the dashboard and Argo CD use to switch themes.
      const { classList } = document.documentElement;
      classList.toggle('theme-dark', context.globals.theme === 'dark');
      classList.toggle('theme-light', context.globals.theme !== 'dark');
      return Story();
    },
  ],
};

export default preview;
