import type { StorybookConfig } from '@storybook/react-vite';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

const config: StorybookConfig = {
  stories: ['../../shared/src/components/plugins/**/*.stories.tsx'],
  framework: {
    name: '@storybook/react-vite',
    options: {},
  },
  viteFinal: async (config) => {
    config.resolve = config.resolve || {};
    // Dedupe React so ui/storybook and ui/shared share a single copy, avoiding invalid hook call errors.
    config.resolve.dedupe = ['react', 'react-dom'];
    config.resolve.alias = {
      ...config.resolve.alias,
      '@shared': resolve(__dirname, '../../shared/src'),
      '@lib': resolve(__dirname, '../../components-lib/src'),
    };
    return config;
  },
};

export default config;
