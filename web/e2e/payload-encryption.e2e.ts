// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// web/scripts/run-payload-encryption-e2e.sh starts both runs with a Go SDK Worker that encrypts every value.
const orderFlowId = process.env.DEX_WEB_E2E_ORDER_FLOW_ID ?? 'e2e-order';
const declinedFlowId = process.env.DEX_WEB_E2E_DECLINED_FLOW_ID ?? 'e2e-declined';
const screenshotDir = process.env.DEX_WEB_E2E_SCREENSHOT_DIR ?? '';

interface StoredAttribute {
  key: string;
  value: unknown;
}

async function storedAttributes(request: APIRequestContext, flowId: string): Promise<StoredAttribute[]> {
  const summary = await (await request.get(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`)).json() as { runId: string };
  const query = `flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(summary.runId)}`;
  const state = await (await request.get(`/api/flows/state?${query}`)).json() as { attributes: StoredAttribute[] };
  return state.attributes;
}

/** The Dex Web server marks an encrypted stored Value as {"__dexEnvelope": {encoding, payload}}. */
function envelopeOf(value: unknown): { encoding: string; payload: string } | undefined {
  return (value as { __dexEnvelope?: { encoding: string; payload: string } } | null)?.__dexEnvelope;
}

function isEnvelope(value: unknown): boolean {
  return envelopeOf(value)?.encoding === 'dex/1';
}

async function chooseCodecMode(page: Page, mode: 'Auto' | 'Off') {
  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByRole('group', { name: 'Codec endpoint' }).getByRole('button', { name: mode }).click();
  await page.keyboard.press('Escape');
}

test.describe.configure({ mode: 'serial' });

test('decrypts attributes, Step values, and error text through the local dev codec server', async ({ page, request }) => {
  const attributes = await storedAttributes(request, orderFlowId);
  const note = attributes.find((attribute) => attribute.key === 'customer-note');
  expect(isEnvelope(note?.value)).toBe(true);
  const ciphertext = envelopeOf(note?.value)?.payload ?? '';

  await page.goto(`/v1/flows/${orderFlowId}?view=overview`);
  await page.getByRole('button', { name: 'Settings' }).click();
  await expect(page.getByTestId('codec-endpoint-status')).toContainText('Local dev codec server');
  await page.keyboard.press('Escape');

  const decryptedNote = page.getByText('VIP customer: deliver before noon').first();
  await expect(decryptedNote).toBeVisible();
  await expect(page.getByRole('img', { name: 'Decrypted in this browser' }).first()).toBeVisible();
  await expect(page.getByText('waiting').first()).toBeVisible();
  expect(await page.locator('body').innerText()).not.toContain(ciphertext);
  if (screenshotDir) {
    await decryptedNote.locator('xpath=ancestor::section[1]').screenshot({ path: `${screenshotDir}/decrypted.png` });
  }

  // The packing slip is PrepareOrder's encrypted output and AwaitApproval's input.
  await page.goto(`/v1/flows/${orderFlowId}?view=timeline`);
  await expect(page.getByText('221B Baker Street').first()).toBeVisible();

  await page.goto(`/v1/flows/${declinedFlowId}`);
  await expect(page.getByText('card declined for Ada Lovelace').first()).toBeVisible();
  expect(await page.locator('body').innerText()).not.toContain('dex-encoded:');
});

test('shows a placeholder with the key ID when no codec endpoint is used', async ({ page }) => {
  await page.goto(`/v1/flows/${orderFlowId}?view=overview`);
  await chooseCodecMode(page, 'Off');
  const placeholder = page.getByText('Encrypted (key id: k1)').first();
  await expect(placeholder).toBeVisible();
  await expect(page.getByRole('img', { name: 'Encrypted' }).first()).toBeVisible();
  await expect(page.getByText('VIP customer: deliver before noon')).toHaveCount(0);
  if (screenshotDir) {
    await placeholder.locator('xpath=ancestor::section[1]').screenshot({ path: `${screenshotDir}/placeholder.png` });
  }

  // Without a codec, updating an encrypted Attribute is refused instead of writing plaintext.
  await page.getByRole('button', { name: 'Set attribute' }).click();
  const dialog = page.getByRole('dialog', { name: /Set attribute/ }).or(page.locator('.modal'));
  await dialog.getByLabel('Attribute key').fill('customer-note');
  await dialog.getByLabel('Value type').selectOption('string');
  await dialog.getByLabel('Value', { exact: true }).fill('plaintext attempt');
  await dialog.locator('.modal-actions').getByRole('button', { name: 'Set attribute' }).click();
  await expect(dialog.getByText(/Attribute customer-note is encrypted/)).toBeVisible();
  await dialog.getByRole('button', { name: 'Close', exact: true }).last().click();

  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByRole('group', { name: 'Codec endpoint' }).getByRole('button', { name: 'Auto' }).click();
  await expect(page.getByText('VIP customer: deliver before noon').first()).toBeVisible();
});

test('encodes writes in the browser and follows the stored Attribute form', async ({ page, request }) => {
  await page.goto(`/v1/flows/${orderFlowId}?view=overview`);
  await expect(page.getByText('VIP customer: deliver before noon').first()).toBeVisible();

  const setAttribute = async (key: string, value: string) => {
    await page.getByRole('button', { name: 'Set attribute' }).click();
    const dialog = page.locator('.modal');
    await dialog.getByLabel('Attribute key').fill(key);
    await dialog.getByLabel('Value type').selectOption('string');
    await dialog.getByLabel('Value', { exact: true }).fill(value);
    await dialog.locator('.modal-actions').getByRole('button', { name: 'Set attribute' }).click();
    await expect(dialog).toHaveCount(0);
  };

  // An update of an encrypted Attribute is encrypted again; a new Attribute is written in plaintext.
  await setAttribute('customer-note', 'Leave at the front desk');
  await setAttribute('web-note', 'written from Dex Web');
  await expect.poll(async () => {
    const attributes = await storedAttributes(request, orderFlowId);
    return {
      note: isEnvelope(attributes.find((attribute) => attribute.key === 'customer-note')?.value),
      webNote: attributes.find((attribute) => attribute.key === 'web-note')?.value,
    };
  }).toEqual({ note: true, webNote: 'written from Dex Web' });
  await expect(page.getByText('Leave at the front desk').first()).toBeVisible();

  // A Channel message is encrypted before Dex Web sends it; the Worker decrypts it and completes the run.
  await page.getByRole('button', { name: 'Publish message' }).click();
  const dialog = page.locator('.modal');
  await dialog.getByLabel('Channel name').fill('approval');
  await dialog.getByLabel('Value type').selectOption('string');
  await dialog.getByLabel('Value', { exact: true }).fill('approved by reviewer');
  await expect(dialog.getByText(/encrypted in your browser/)).toBeVisible();
  await dialog.locator('.modal-actions').getByRole('button', { name: 'Publish message' }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(async () => {
    const summary = await (await request.get(`/api/flows/summary?flowId=${orderFlowId}`)).json() as { flowStatus: string };
    return summary.flowStatus;
  }, { timeout: 30_000 }).toBe('Completed');
  const summary = await (await request.get(`/api/flows/summary?flowId=${orderFlowId}`)).json() as { runId: string };
  const history = await (await request.get(`/api/flows/history?flowId=${orderFlowId}&runId=${summary.runId}`)).text();
  expect(history).toContain('"encoding":"dex/1"');
  expect(history).not.toContain('approved by reviewer');
  await page.goto(`/v1/flows/${orderFlowId}`);
  await expect(page.getByText('approved by reviewer').first()).toBeVisible();
});
