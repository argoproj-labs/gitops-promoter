import { mergeConfig } from 'vite';
import { defineConfig } from 'vitest/config';
import viteConfig from './vite.config';

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
        reporter: ['text', 'lcov'],
        reportsDirectory: './coverage',
        include: ['src/**/*.{ts,tsx}'],
        exclude: ['node_modules/**', '**/*.d.ts', '**/vitest.config.ts'],
      },
    },
  }),
);
