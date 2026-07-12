import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';

const PORT = 3000;

export default defineConfig({
  testDir: './e2e',
  testMatch: /.*\.spec\.ts$/,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false, // single shared backend stack — keep specs serial
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: `next dev -p ${PORT}`,
    url: `http://localhost:${PORT}`,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
    cwd: path.resolve(__dirname),
    env: {
      OPS_SERVICE_ADDRESS: process.env.OPS_SERVICE_ADDRESS || '127.0.0.1:7235',
    },
  },
  globalSetup: './e2e/globalSetup.ts',
  globalTeardown: './e2e/globalTeardown.ts',
});
