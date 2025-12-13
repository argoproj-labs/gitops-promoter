import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react()],
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
  },
}); 