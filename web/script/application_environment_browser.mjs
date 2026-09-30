// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const { chromium } = await import(pathToFileURL(process.env.DEX_PROJECT_CONFIG_PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ headless: true, executablePath: process.env.DEX_PROJECT_CONFIG_CHROMIUM || undefined });
try {
  const page = await browser.newPage();
  const privateValue = 'browser-fixture-private-value-long-enough';
  await page.goto(`${process.env.DEX_PROJECT_CONFIG_BROWSER_URL}/v2/connectors`);
  const form = page.getByRole('form', { name: 'Application environment' });
  await form.waitFor({ state: 'visible' });
  await page.getByLabel('APP_ENV (required)', { exact: true }).selectOption('production');
  await page.getByLabel('SIGNING_SECRET (required)', { exact: true }).fill(privateValue);
  const saved = page.waitForResponse((response) => response.url().endsWith('/api/v2/application-environment') && response.request().method() === 'PUT');
  await page.getByRole('button', { name: 'Save environment', exact: true }).click();
  const response = await saved;
  assert.equal(response.status(), 200, 'browser environment save failed');
  const safe = await response.text();
  for (const forbidden of [privateValue, 'secretRef', 'app-secrets/', 'sha256:']) assert.equal(safe.includes(forbidden), false, 'safe response exposed private material');
  await page.getByPlaceholder('Configured; leave untouched to keep').waitFor({ state: 'visible' });
  await page.reload();
  const secret = page.getByPlaceholder('Configured; leave untouched to keep');
  await secret.waitFor({ state: 'visible' });
  assert.equal(await secret.inputValue(), '', 'saved private value appeared in an input');
  assert.equal(await page.getByLabel('APP_ENV (required)', { exact: true }).inputValue(), 'production');
  console.log('Native application environment browser save/reload and write-only-secret checks passed.');
} finally {
  await browser.close();
}
