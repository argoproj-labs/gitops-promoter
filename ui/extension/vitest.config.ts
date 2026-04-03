import path from 'path';
import { defineConfig } from 'vitest/config';

// Istanbul LCOV uses path.relative(projectRoot, file); repo root => SF: lines match Codecov/git.
const repoRoot = path.resolve(__dirname, '../..');

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
    coverage: {
      provider: 'v8',
      reporter: ['text', ['lcov', { projectRoot: repoRoot }]],
      reportsDirectory: './coverage',
      include: ['**/*.{ts,tsx}'],
      exclude: [
        '**/*.test.{ts,tsx}',
        'test/**',
        'node_modules/**',
        'dist/**',
        'vitest.config.ts',
        'iconStyles.generated.ts',
      ],
    },
  },
});
