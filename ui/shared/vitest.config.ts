import path from 'path';
import { fileURLToPath } from 'url';
import { defineConfig } from 'vitest/config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Istanbul LCOV uses path.relative(projectRoot, file); repo root => SF: lines match Codecov/git.
const repoRoot = path.resolve(__dirname, '../..');

export default defineConfig({
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
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
      include: ['ui/shared/src/**/*.{ts,tsx}'],
      exclude: ['**/*.test.{ts,tsx}', '**/node_modules/**', '**/*.d.ts', '**/vitest.config.ts'],
    },
  },
});
