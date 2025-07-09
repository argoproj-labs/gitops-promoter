import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';

export default defineConfig({
  plugins: [react()],
  server: {
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