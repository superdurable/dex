import { test, expect } from '@playwright/test';
import { readState } from './state';

test.describe('Show page', () => {
  const state = readState();

  test.skip(!state.completedRunId, 'no run id available');

  const runId = state.completedRunId!;
  // The show page defaults to the graph view; the timeline-focused tests
  // below opt in via ?view=timeline.
  const timelineUrl = `/flow/show?namespace=${encodeURIComponent(state.namespace)}&runId=${encodeURIComponent(runId)}&view=timeline`;

  test('renders the summary card and run id', async ({ page }) => {
    await page.goto(timelineUrl);
    await expect(page.locator('[data-testid="run-summary"]')).toBeVisible();
    await expect(page.locator('[data-testid="summary-runid"]')).toContainText(runId);
  });

  test('timeline renders at least one event with a RunStart card first', async ({ page }) => {
    await page.goto(timelineUrl);
    const timeline = page.locator('[data-testid="timeline"]');
    await expect(timeline).toBeVisible({ timeout: 30_000 });

    const cards = page.locator('[data-testid="event-card"]');
    await expect(cards.first()).toBeVisible();
    expect(await cards.count()).toBeGreaterThan(0);

    await expect(cards.first()).toHaveAttribute('data-event-type', 'RunStart');
  });

  test('completed run shows a RunStop event', async ({ page }) => {
    if (!state.completedRunId) test.skip(true, 'no completed run id');
    await page.goto(timelineUrl);
    const runEnd = page.locator('[data-event-type="RunStop"]');
    await expect(runEnd).toBeVisible({ timeout: 30_000 });
    await expect(runEnd).toContainText('Completed');
  });

  test('raw JSON toggle reveals payload pre block', async ({ page }) => {
    await page.goto(timelineUrl);
    const firstCard = page.locator('[data-testid="event-card"]').first();
    await expect(firstCard).toBeVisible();
    const toggle = firstCard.locator('[data-testid="event-raw-toggle"]');
    await toggle.click();
    const pre = firstCard.locator('[data-testid="event-raw-pre"]');
    await expect(pre).toBeVisible();
  });
});

test.describe('Show page - Graph view', () => {
  const state = readState();
  test.skip(!state.completedRunId, 'no run id available');

  const runId = state.completedRunId!;
  const showUrl = `/flow/show?namespace=${encodeURIComponent(state.namespace)}&runId=${encodeURIComponent(runId)}`;

  test('default view is graph; toggling to timeline updates URL and back', async ({ page }) => {
    // Visiting without ?view= should land on the graph (the new default).
    await page.goto(showUrl);
    await expect(page.locator('[data-testid="workflow-graph"]')).toBeVisible({ timeout: 30_000 });
    await expect(page).not.toHaveURL(/view=/);
    await expect(page.locator('[data-testid="timeline"]')).toHaveCount(0);

    // Switch to timeline -> URL gains view=timeline and the timeline renders.
    await page.locator('[data-testid="view-toggle-timeline"]').click();
    await expect(page).toHaveURL(/view=timeline/);
    await expect(page.locator('[data-testid="timeline"]')).toBeVisible();

    // Switch back -> URL drops view= and the graph re-renders.
    await page.locator('[data-testid="view-toggle-graph"]').click();
    await expect(page).not.toHaveURL(/view=/);
    await expect(page.locator('[data-testid="workflow-graph"]')).toBeVisible({ timeout: 30_000 });
  });

  test('renders step nodes including __start, __end, and at least one Completed step', async ({ page }) => {
    await page.goto(`${showUrl}&view=graph`);
    await expect(page.locator('[data-testid="workflow-graph"]')).toBeVisible({ timeout: 30_000 });

    const nodes = page.locator('[data-testid="step-node"]');
    await expect(nodes.first()).toBeVisible();
    expect(await nodes.count()).toBeGreaterThanOrEqual(3);

    await expect(page.locator('[data-step-exe-id="__start"]')).toBeVisible();
    await expect(page.locator('[data-step-exe-id="__end"]')).toBeVisible();

    const completed = page.locator('[data-testid="step-node"][data-status="Completed"]');
    expect(await completed.count()).toBeGreaterThan(0);

    // Sequential benchmark flow always finishes with a step whose stop_decision=COMPLETE.
    await expect(page.locator('[data-stop-decision="COMPLETE"]').first()).toBeVisible();
  });

  test('clicking a step node opens the detail panel with its events', async ({ page }) => {
    await page.goto(`${showUrl}&view=graph`);
    await expect(page.locator('[data-testid="workflow-graph"]')).toBeVisible({ timeout: 30_000 });

    const completedNode = page
      .locator('[data-testid="step-node"][data-status="Completed"]')
      .first();
    await expect(completedNode).toBeVisible();
    const stepExeId = await completedNode.getAttribute('data-step-exe-id');
    expect(stepExeId).toBeTruthy();
    await completedNode.click();

    const panel = page.locator('[data-testid="step-node-detail"]');
    await expect(panel).toBeVisible();
    await expect(panel).toContainText(stepExeId!);
    const cards = panel.locator('[data-testid="event-card"]');
    expect(await cards.count()).toBeGreaterThan(0);
  });
});
