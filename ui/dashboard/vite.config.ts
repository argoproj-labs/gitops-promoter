import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react()],
  css: {
    preprocessorOptions: {
      scss: {
        // In dev, Vite injects this CSS into a <style> tag, so a leading
        // @charset (emitted by Sass when the source has non-ASCII chars) is
        // an invalid at-rule the browser drops — taking the next rule with it.
        charset: false,
      },
    },
  },
  server: {
    hmr: {
      host: 'localhost',
      port: 5173,
    },
    proxy: {
      '/list': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
      },

      '/watch': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
        ws: false,
      },
    },
  },
  resolve: {
    alias: {
      '@lib': resolve(__dirname, '../components-lib/src'),
      '@shared': resolve(__dirname, '../shared/src'),
    },
    // ensure that only one version of React exists in the bundle
    // otherwise, you might encounter issues with hooks or context not working properly
    dedupe: ['react', 'react-dom'],
  },
});
