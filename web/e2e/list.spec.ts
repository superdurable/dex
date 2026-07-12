import { test, expect } from '@playwright/test';
import { readState } from './state';

test.describe('List page', () => {
  const state = readState();

  // OpsService.ListRuns requires (namespace, flow_type, status). The BFF
  // fans out across statuses when status is omitted; we still need flowType
  // in the URL so the visibility query matches the seeded runs.
  const listUrl = `/?namespace=${encodeURIComponent(state.namespace)}&flowType=${encodeURIComponent(state.flowType)}`;

  test('renders the runs table with seeded runs', async ({ page }) => {
    await page.goto(listUrl);

    const table = page.locator('[data-testid="runs-table"]');
    await expect(table).toBeVisible();

    const rows = page.locator('[data-testid="run-row"]');
    await expect(rows.first()).toBeVisible({ timeout: 30_000 });
    expect(await rows.count()).toBeGreaterThan(0);

    const firstRow = rows.first();
    await expect(firstRow.locator('[data-testid="status-badge"]')).toBeVisible();

    const firstLink = firstRow.locator('[data-testid="run-link"]');
    const href = await firstLink.getAttribute('href');
    expect(href).toMatch(new RegExp(`^/flow/show\\?namespace=${state.namespace}&runId=`));
  });

  test('clicking a run navigates to the show page', async ({ page }) => {
    await page.goto(listUrl);
    const firstRow = page.locator('[data-testid="run-row"]').first();
    await expect(firstRow).toBeVisible({ timeout: 30_000 });
    await firstRow.locator('[data-testid="run-link"]').click();
    await page.waitForURL(/\/flow\/show\?/);
    await expect(page.locator('[data-testid="run-summary"]')).toBeVisible();
  });

  test('namespace input updates the URL query', async ({ page }) => {
    await page.goto(`/?flowType=${encodeURIComponent(state.flowType)}`);
    const input = page.locator('[data-testid="namespace-input"]');
    await input.fill(state.namespace);
    await input.press('Enter');
    await expect(page).toHaveURL(new RegExp(`namespace=${state.namespace}`));
    await expect(page.locator('[data-testid="runs-table"]')).toBeVisible({ timeout: 30_000 });
  });

  test('namespace-only URL (any flow type / any status) returns runs', async ({ page }) => {
    // No flowType, no status -> exercises the server-side optional filter
    // path. Without that fix, OpsService.ListRuns would treat empty
    // flow_type + status=Pending as literal filters and return nothing.
    await page.goto(`/?namespace=${encodeURIComponent(state.namespace)}`);
    await expect(page.locator('[data-testid="runs-table"]')).toBeVisible({ timeout: 30_000 });
    const rows = page.locator('[data-testid="run-row"]');
    await expect(rows.first()).toBeVisible({ timeout: 30_000 });
    expect(await rows.count()).toBeGreaterThan(0);
  });
});
