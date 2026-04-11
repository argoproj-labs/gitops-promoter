import path from 'path';
import { fileURLToPath } from 'url';
import { mergeConfig } from 'vite';
import { defineConfig } from 'vitest/config';
import viteConfig from './vite.config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Istanbul LCOV uses path.relative(projectRoot, file); repo root => SF: lines match Codecov/git.
const repoRoot = path.resolve(__dirname, '../..');

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      include: ['test/**/*.test.ts'],
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
        // Globs match absolute paths; picomatch uses contains:true, so `ui/...` need not be anchored.
        include: [
          'ui/dashboard/src/**/*.{ts,tsx}',
          'ui/shared/src/**/*.{ts,tsx}',
          'ui/components-lib/src/**/*.{ts,tsx}',
        ],
        exclude: ['**/node_modules/**', '**/*.d.ts', '**/vitest.config.ts'],
      },
    },
  }),
);
