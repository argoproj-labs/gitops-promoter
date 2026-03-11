import { defineConfig } from 'vitest/config';
import path from 'path';

export default defineConfig({
  resolve: {
    alias: {
      '@shared': path.resolve(__dirname, '../shared/src'),
      '@components-lib': path.resolve(__dirname, '../components-lib/src'),
      react: path.resolve(__dirname, 'node_modules/react'),
      'react-dom': path.resolve(__dirname, 'node_modules/react-dom'),
    },
  },
  test: {
    environment: 'node',
    css: false,
    server: {
      deps: {
        inline: [/react-icons/],
      },
    },
  },
});
