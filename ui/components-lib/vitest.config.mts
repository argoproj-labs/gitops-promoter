import path from 'path';
import { fileURLToPath } from 'url';
import { defineConfig } from 'vitest/config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '../..');

export default defineConfig({
  resolve: {
    alias: {
      '@lib': path.resolve(__dirname, 'src'),
      '@shared': path.resolve(__dirname, '../shared/src'),
      react: path.resolve(__dirname, 'node_modules/react'),
      'react-dom': path.resolve(__dirname, 'node_modules/react-dom'),
    },
    dedupe: ['react', 'react-dom'],
  },
  test: {
    environment: 'jsdom',
    // JUnit output is uploaded to Codecov Test Analytics by CI (gitignored). Setting reporters
    // turns off Vitest's implicit github-actions reporter, so add it back for failure annotations.
    reporters: [
      'default',
      'junit',
      ...(process.env.GITHUB_ACTIONS === 'true' ? ['github-actions'] : []),
    ],
    outputFile: { junit: './junit.xml' },
    css: false,
    passWithNoTests: true,
    include: ['test/**/*.test.{ts,tsx}'],
    setupFiles: ['test/setup.ts'],
    server: {
      deps: {
        inline: [/react-icons/],
      },
    },
    coverage: {
      provider: 'v8',
      allowExternal: true,
      reporter: ['text', ['lcov', { projectRoot: repoRoot }]],
      reportsDirectory: './coverage',
      include: ['src/components/PromotionExplorer/**/*.{ts,tsx}'],
      exclude: ['**/node_modules/**', '**/*.d.ts', '**/vitest.config.ts'],
    },
  },
});
