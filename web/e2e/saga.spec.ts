import { test, expect } from '@playwright/test';
import { readState } from './state';

async function triggerSagaRun(benchPort: number, methodKind: 'execute' | 'waitFor'): Promise<string[]> {
  const url = `http://127.0.0.1:${benchPort}/trigger?mode=saga&runs=1&methodKind=${methodKind}`;
  const resp = await fetch(url);
  if (!resp.ok) {
    throw new Error(`benchmark saga trigger failed: HTTP ${resp.status} ${await resp.text()}`);
  }
  const body = (await resp.json()) as { run_ids?: string[] };
  return body.run_ids ?? [];
}

test.describe('Show page - saga proceed flow', () => {
  const state = readState();
  test.skip(!state.benchPort, 'no benchmark port in e2e state');
  test.setTimeout(180_000);

  test('execute retry-exhaustion proceeds to handler and completes', async ({ page }) => {
    const runIds = await triggerSagaRun(state.benchPort, 'execute');
    test.skip(runIds.length === 0, 'no saga run triggered');
    const runId = runIds[0];
    const showUrl = `/flow/show?namespace=${encodeURIComponent(state.namespace)}&runId=${encodeURIComponent(runId)}&view=graph`;

    await page.goto(showUrl);

    await expect(page.locator('[data-testid="method-failed-proceeded-badge"]').first()).toBeVisible({
      timeout: 120_000,
    });
    await expect(page.locator('[data-method-failed="true"]').first()).toContainText('Failed');
    await expect(page.locator('[data-testid="run-summary"]')).toContainText('Completed', {
      timeout: 120_000,
    });
  });

  test('waitFor retry-exhaustion proceeds to handler and completes', async ({ page }) => {
    const runIds = await triggerSagaRun(state.benchPort, 'waitFor');
    test.skip(runIds.length === 0, 'no saga run triggered');
    const runId = runIds[0];
    const showUrl = `/flow/show?namespace=${encodeURIComponent(state.namespace)}&runId=${encodeURIComponent(runId)}&view=graph`;

    await page.goto(showUrl);

    await expect(page.locator('[data-testid="method-failed-proceeded-badge"]').first()).toBeVisible({
      timeout: 120_000,
    });
    await expect(page.locator('[data-method-failed="true"]').first()).toContainText('Failed');
    await expect(page.locator('[data-testid="run-summary"]')).toContainText('Completed', {
      timeout: 120_000,
    });
  });
});
