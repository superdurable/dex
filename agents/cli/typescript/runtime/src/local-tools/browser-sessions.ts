// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

import { ToolExecutionError } from "@superdurable/dex-agents";
import type { Browser, Page } from "playwright-core";

import { parseHttpUrl } from "./web-page-fetch.js";

const actionTimeoutMs = 10_000;
const navigationTimeoutMs = 30_000;
const navigationWaitMs = 2_000;
const maximumSnapshotCharacters = 60_000;

/**
 * One headless Chromium per Worker process, with one page per session, driven through Playwright.
 *
 * Pages live in the Worker's memory, so a restarted Worker starts each session on a blank page. The browser starts
 * on first use: Playwright's own Chromium when it is installed, else the system Chrome, else Playwright downloads
 * Chromium once. Each result is the page's title, URL, and accessibility snapshot as text.
 */
export class BrowserSessions {
  private browser: Promise<Browser> | undefined;
  private readonly pages = new Map<string, Page>();

  /**
   * Creates the sessions.
   * @param log - Writes progress, such as the first-use download, for the person at the terminal.
   */
  public constructor(private readonly log: (line: string) => void) {}

  /**
   * Opens a URL in the session's page.
   * @param sessionId - The session.
   * @param url - An `http` or `https` URL.
   * @returns The page snapshot.
   */
  public async navigate(sessionId: string, url: string): Promise<string> {
    const target = parseHttpUrl(url);
    const page = await this.pageOf(sessionId);
    await this.runAction(`opening ${url}`, () =>
      page.goto(target.href, { waitUntil: "domcontentloaded", timeout: navigationTimeoutMs }),
    );
    return this.snapshotPage(page);
  }

  /**
   * Returns the session page's snapshot.
   * @param sessionId - The session.
   * @returns The snapshot, or a note that no page is open.
   */
  public async snapshot(sessionId: string): Promise<string> {
    const page = this.pages.get(sessionId);
    return page === undefined ? "No page is open. Call browser_navigate first." : this.snapshotPage(page);
  }

  /**
   * Clicks the element a selector finds.
   * @param sessionId - The session.
   * @param selector - A Playwright selector.
   * @returns The page snapshot after the click.
   */
  public async click(sessionId: string, selector: string): Promise<string> {
    const page = this.requirePage(sessionId);
    await this.runNavigatingAction(page, `clicking ${selector}`, () => page.click(selector, { timeout: actionTimeoutMs }));
    return this.snapshotPage(page);
  }

  /**
   * Replaces the value of the input a selector finds.
   * @param sessionId - The session.
   * @param selector - A Playwright selector.
   * @param value - The new value.
   * @returns The page snapshot after filling.
   */
  public async fill(sessionId: string, selector: string, value: string): Promise<string> {
    const page = this.requirePage(sessionId);
    await this.runAction(`filling ${selector}`, () => page.fill(selector, value, { timeout: actionTimeoutMs }));
    return this.snapshotPage(page);
  }

  /**
   * Submits the form that contains the element a selector finds, or the form it names.
   * @param sessionId - The session.
   * @param selector - A Playwright selector of the form or of an element inside it.
   * @returns The page snapshot after the submission.
   */
  public async submitForm(sessionId: string, selector: string): Promise<string> {
    const page = this.requirePage(sessionId);
    await this.runNavigatingAction(page, `submitting the form of ${selector}`, async () => {
      // The submission runs after evaluate returns, so a navigation cannot destroy its context.
      const hasForm = await page
        .locator(selector)
        .first()
        .evaluate(
          (element) => {
            const form = (element as unknown as FormElementLike).closest("form");
            setTimeout(() => form?.requestSubmit(), 0);
            return form !== null;
          },
          undefined,
          { timeout: actionTimeoutMs },
        );
      if (!hasForm) {
        throw new ToolExecutionError(`${selector} is not a form and is not inside one`);
      }
    });
    return this.snapshotPage(page);
  }

  /**
   * Closes the browser.
   * @returns A Promise that resolves after it closed.
   */
  public async close(): Promise<void> {
    const browser = this.browser;
    this.browser = undefined;
    this.pages.clear();
    if (browser !== undefined) {
      await (await browser.catch(() => undefined))?.close();
    }
  }

  private requirePage(sessionId: string): Page {
    const page = this.pages.get(sessionId);
    if (page === undefined || page.isClosed()) {
      throw new ToolExecutionError("no page is open; call browser_navigate first");
    }
    return page;
  }

  private async pageOf(sessionId: string): Promise<Page> {
    const existing = this.pages.get(sessionId);
    if (existing !== undefined && !existing.isClosed()) {
      return existing;
    }
    this.browser ??= this.launchBrowser();
    const browser = await this.browser.catch((error: unknown) => {
      this.browser = undefined;
      throw new ToolExecutionError(`the browser could not start: ${describe(error)}`, { cause: error });
    });
    const page = await (await browser.newContext()).newPage();
    this.pages.set(sessionId, page);
    return page;
  }

  private async launchBrowser(): Promise<Browser> {
    const { chromium } = await import("playwright-core");
    if (existsSync(chromium.executablePath())) {
      return chromium.launch({ headless: true });
    }
    try {
      return await chromium.launch({ headless: true, channel: "chrome" });
    } catch {
      this.log("Downloading the Playwright Chromium browser; this happens once.");
      await installPlaywrightChromium();
      return chromium.launch({ headless: true });
    }
  }

  private async snapshotPage(page: Page): Promise<string> {
    return this.runAction("reading the page", async () => {
      const title = await page.title();
      const tree = await page.locator("body").ariaSnapshot({ timeout: actionTimeoutMs });
      const text = `Page: ${title}\nURL: ${page.url()}\n\n${tree}`;
      return text.length > maximumSnapshotCharacters ? `${text.slice(0, maximumSnapshotCharacters)}\n[cut]` : text;
    });
  }

  /** Runs an action that may navigate, then waits for a navigation it started to load. */
  private async runNavigatingAction(page: Page, description: string, action: () => Promise<unknown>): Promise<void> {
    const navigation = page
      .waitForEvent("framenavigated", { predicate: (frame) => frame === page.mainFrame(), timeout: navigationWaitMs })
      .then(() => true)
      .catch(() => false);
    await this.runAction(description, action);
    if (await navigation) {
      await page.waitForLoadState("domcontentloaded", { timeout: navigationTimeoutMs }).catch(() => undefined);
    }
  }

  private async runAction<T>(description: string, action: () => Promise<T>): Promise<T> {
    try {
      return await action();
    } catch (error: unknown) {
      if (error instanceof ToolExecutionError) {
        throw error;
      }
      throw new ToolExecutionError(`${description} failed: ${describe(error)}`, { cause: error });
    }
  }
}

/** The part of a DOM element the form submission uses; this package compiles without the DOM library. */
interface FormElementLike {
  closest(selector: "form"): { requestSubmit(): void } | null;
}

function installPlaywrightChromium(): Promise<void> {
  const packageJson = createRequire(import.meta.url).resolve("playwright-core/package.json");
  const cli = join(dirname(packageJson), "cli.js");
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn(process.execPath, [cli, "install", "chromium"], { stdio: ["ignore", "inherit", "inherit"] });
    child.once("error", rejectPromise);
    child.once("close", (code) =>
      code === 0 ? resolvePromise() : rejectPromise(new Error(`playwright install chromium exited with ${code}`)),
    );
  });
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message.split("\n")[0] ?? error.message : String(error);
}
