import { test, expect } from '@playwright/test';
import { readState } from './state';

async function triggerRetryRun(benchPort: number): Promise<string[]> {
  const url = `http://127.0.0.1:${benchPort}/trigger?mode=retry&runs=1&numSteps=1&stateSize=64`;
  const resp = await fetch(url);
  if (!resp.ok) {
    throw new Error(`benchmark retry trigger failed: HTTP ${resp.status} ${await resp.text()}`);
  }
  const body = (await resp.json()) as { run_ids?: string[] };
  return body.run_ids ?? [];
}

test.describe('Show page - retry flow', () => {
  const state = readState();
  test.skip(!state.benchPort, 'no benchmark port in e2e state');

  test('shows retry badge while step is retrying then completes', async ({ page }) => {
    const runIds = await triggerRetryRun(state.benchPort);
    test.skip(runIds.length === 0, 'no retry run triggered');
    const runId = runIds[0];
    const namespace = state.namespace;
    const showUrl = `/flow/show?namespace=${encodeURIComponent(namespace)}&runId=${encodeURIComponent(runId)}&view=graph`;

    await page.goto(showUrl);

    await expect(page.locator('[data-testid="step-retry-badge"]').first()).toBeVisible({
      timeout: 90_000,
    });

    const badge = page.locator('[data-testid="step-retry-badge"]').first();
    await expect(badge).toContainText(/Retrying · #/);

    await expect(page.locator('[data-testid="run-summary"]')).toContainText(/Completed|Failed|Running/, {
      timeout: 120_000,
    });
  });
});
