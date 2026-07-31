import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('.', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8902',
      '/healthz': 'http://127.0.0.1:8902',
    },
  },
  build: {
    outDir: 'assets/dist',
    emptyOutDir: true,
  },
});
