import { defineConfig } from 'vitest/config';
import path from 'path';

export default defineConfig({
  resolve: {
    alias: {
      '@shared': path.resolve(__dirname, '../shared/src'),
      '@components-lib': path.resolve(__dirname, '../components-lib/src'),
    },
  },
  test: {
    environment: 'node',
  },
});
