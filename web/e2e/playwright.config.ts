// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { defineConfig } from '@playwright/test';

// web/scripts/run-payload-encryption-e2e.sh starts dexcli dev and sets DEX_WEB_E2E_URL.
export default defineConfig({
  testDir: '.',
  testMatch: /.*\.e2e\.ts$/,
  timeout: 120_000,
  workers: 1,
  outputDir: process.env.DEX_WEB_E2E_OUTPUT_DIR ?? '/tmp/dex-web-e2e-results',
  reporter: 'list',
  use: {
    baseURL: process.env.DEX_WEB_E2E_URL ?? 'http://127.0.0.1:18912',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
  },
});
