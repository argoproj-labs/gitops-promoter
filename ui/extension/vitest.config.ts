import path from 'path';
import { fileURLToPath } from 'url';
import { defineConfig } from 'vitest/config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

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
      // Sibling packages live outside vitest root; default allowExternal:false drops them from LCOV.
      allowExternal: true,
      reporter: ['text', ['lcov', { projectRoot: repoRoot }]],
      reportsDirectory: './coverage',
      include: [
        'ui/extension/**/*.{ts,tsx}',
        'ui/shared/src/**/*.{ts,tsx}',
        'ui/components-lib/src/**/*.{ts,tsx}',
      ],
      exclude: [
        '**/*.test.{ts,tsx}',
        'ui/extension/test/**',
        '**/node_modules/**',
        'ui/extension/dist/**',
        'ui/extension/vitest.config.ts',
        'ui/extension/iconStyles.generated.ts',
      ],
    },
  },
});
