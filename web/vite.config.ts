// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  resolve: {
    preserveSymlinks: true,
    alias: {
      '@': fileURLToPath(new URL('.', import.meta.url)),
    },
  },
  optimizeDeps: {
    // preserveSymlinks resolves the linked workspace packages through web/node_modules, so
    // Vite would otherwise pre-bundle them and serve that bundle for the life of the
    // process, hiding every edit to their source. Excluding them serves the source instead.
    // Their files still sit under node_modules, which the watcher ignores, so restart the
    // dev server after editing one.
    exclude: ['@superdurable/flow-definition-renderer'],
  },
  server: {
    proxy: {
      '/api': process.env.DEX_WEB_PROXY ?? 'http://127.0.0.1:8902',
      '/healthz': process.env.DEX_WEB_PROXY ?? 'http://127.0.0.1:8902',
    },
  },
  build: {
    outDir: 'assets/dist',
    emptyOutDir: true,
  },
});
