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
        reporter: ['text', ['lcov', { projectRoot: repoRoot }]],
        reportsDirectory: './coverage',
        include: [
          'src/**/*.{ts,tsx}',
          '../shared/src/**/*.{ts,tsx}',
          '../components-lib/src/**/*.{ts,tsx}',
        ],
        exclude: ['node_modules/**', '**/*.d.ts', '**/vitest.config.ts'],
      },
    },
  }),
);
