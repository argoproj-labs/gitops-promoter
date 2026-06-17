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
    // Allow Cloudflare quick tunnels (*.trycloudflare.com) to reach the dev
    // server when sharing a public preview. Vite 7 blocks unknown Host headers
    // by default. This is a no-op for normal localhost development.
    allowedHosts: ['.trycloudflare.com', '.ngrok-free.app', '.loca.lt', '.lhr.life'],
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
