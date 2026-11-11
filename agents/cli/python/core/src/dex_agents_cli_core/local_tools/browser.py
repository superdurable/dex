# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The browser tools: one headless Chromium page per session, read as text snapshots."""

from __future__ import annotations

import asyncio
import logging
import sys
from urllib.parse import urlsplit

from playwright.async_api import Browser, Page, Playwright, async_playwright
from playwright.async_api import Error as PlaywrightError

from dex_agents import ToolExecutionError

_ACTION_TIMEOUT_MILLISECONDS = 15_000
_NAVIGATION_TIMEOUT_MILLISECONDS = 30_000
_MAX_SNAPSHOT_CHARACTERS = 50_000
_MISSING_BROWSER_MARKER = "Executable doesn't exist"
_INSTALL_TIMEOUT_SECONDS = 600
_LOGGER = logging.getLogger(__name__)


class BrowserUnavailableError(Exception):
    """Report that the headless browser cannot start or be installed."""


class BrowserSessions:
    """Keep one page per agent session in one headless browser, started on first use.

    The browser is Playwright's own Chromium when it is installed, else the
    system Chrome, else Chromium that Playwright downloads on first use.

    Pages live in this Worker process only. After a Worker restart a session
    has no page until the agent opens one again.
    """

    def __init__(self) -> None:
        """Create the sessions without starting a browser."""
        self._start_lock = asyncio.Lock()
        self._playwright: Playwright | None = None
        self._browser: Browser | None = None
        self._pages: dict[str, Page] = {}

    async def open_page(self, session_id: str, url: str) -> str:
        """Open a URL in the session's page.

        Args:
            session_id: The agent session, which owns one page.
            url: An ``http`` or ``https`` URL. Other schemes, such as ``file``,
                are refused, so the browser never reads local files.

        Returns:
            The page's text snapshot.
        """
        parts = urlsplit(url)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise ToolExecutionError(f"{url} is not an http or https URL")
        page = self._pages.get(session_id)
        if page is None or page.is_closed():
            browser = await self._ensure_browser()
            page = await browser.new_page()
            page.set_default_timeout(_ACTION_TIMEOUT_MILLISECONDS)
            self._pages[session_id] = page
        try:
            await page.goto(url, timeout=_NAVIGATION_TIMEOUT_MILLISECONDS)
        except PlaywrightError as error:
            raise ToolExecutionError(f"cannot open {url}: {error.message}") from error
        return await self.snapshot(session_id)

    async def snapshot(self, session_id: str) -> str:
        """Return the page's URL, title, and accessibility snapshot.

        Args:
            session_id: The agent session.

        Returns:
            The snapshot text.
        """
        page = self._page(session_id)
        try:
            tree = await page.locator("body").aria_snapshot()
            title = await page.title()
        except PlaywrightError as error:
            raise ToolExecutionError(f"cannot read the page: {error.message}") from error
        text = f"url: {page.url}\ntitle: {title}\n\n{tree}"
        if len(text) > _MAX_SNAPSHOT_CHARACTERS:
            omitted = len(text) - _MAX_SNAPSHOT_CHARACTERS
            text = f"{text[:_MAX_SNAPSHOT_CHARACTERS]}\n... ({omitted} more characters)"
        return text

    async def click(self, session_id: str, selector: str) -> str:
        """Click an element and return the page's snapshot afterwards.

        Args:
            session_id: The agent session.
            selector: A Playwright selector.

        Returns:
            The snapshot text.
        """
        page = self._page(session_id)
        try:
            await page.locator(selector).first.click()
            await page.wait_for_load_state()
        except PlaywrightError as error:
            raise ToolExecutionError(f"cannot click {selector}: {error.message}") from error
        return await self.snapshot(session_id)

    async def fill(self, session_id: str, selector: str, value: str) -> str:
        """Fill a form field and return the page's snapshot afterwards.

        Args:
            session_id: The agent session.
            selector: A Playwright selector of an input, text area, or editable element.
            value: The text to enter.

        Returns:
            The snapshot text.
        """
        page = self._page(session_id)
        try:
            await page.locator(selector).first.fill(value)
        except PlaywrightError as error:
            raise ToolExecutionError(f"cannot fill {selector}: {error.message}") from error
        return await self.snapshot(session_id)

    async def submit(self, session_id: str, selector: str) -> str:
        """Submit the form that contains an element, and return the next page's snapshot.

        Args:
            session_id: The agent session.
            selector: A Playwright selector of the form or an element inside it.

        Returns:
            The snapshot text.
        """
        page = self._page(session_id)
        try:
            async with page.expect_navigation(timeout=_NAVIGATION_TIMEOUT_MILLISECONDS):
                await page.locator(selector).first.evaluate(
                    "element => (element.form ?? element.closest('form') ?? element)"
                    ".requestSubmit()"
                )
        except PlaywrightError as error:
            raise ToolExecutionError(f"cannot submit {selector}: {error.message}") from error
        return await self.snapshot(session_id)

    async def close(self) -> None:
        """Close every page and stop the browser."""
        if self._browser is not None:
            await self._browser.close()
            self._browser = None
        if self._playwright is not None:
            await self._playwright.stop()
            self._playwright = None
        self._pages.clear()

    def _page(self, session_id: str) -> Page:
        page = self._pages.get(session_id)
        if page is None or page.is_closed():
            raise ToolExecutionError("no page is open in this session; call browser_navigate first")
        return page

    async def _ensure_browser(self) -> Browser:
        async with self._start_lock:
            if self._browser is not None:
                return self._browser
            if self._playwright is None:
                self._playwright = await async_playwright().start()
            try:
                self._browser = await self._launch(self._playwright)
            except BrowserUnavailableError as error:
                raise ToolExecutionError(str(error)) from error
            return self._browser

    async def _launch(self, playwright: Playwright) -> Browser:
        """Start Playwright's Chromium, else the system Chrome, else download Chromium first."""
        try:
            return await playwright.chromium.launch(headless=True)
        except PlaywrightError as error:
            if _MISSING_BROWSER_MARKER not in error.message:
                raise BrowserUnavailableError(
                    f"cannot start the browser: {error.message}"
                ) from error
        try:
            return await playwright.chromium.launch(headless=True, channel="chrome")
        except PlaywrightError:
            _LOGGER.info("no system Chrome; downloading Playwright's headless Chromium")
        await install_browser()
        try:
            return await playwright.chromium.launch(headless=True)
        except PlaywrightError as error:
            raise BrowserUnavailableError(f"cannot start the browser: {error.message}") from error


async def install_browser() -> None:
    """Download Playwright's headless Chromium, as the browser tools do on first use.

    Raises:
        BrowserUnavailableError: If the download fails.
    """
    _LOGGER.info("downloading the headless browser for the browser tools")
    process = await asyncio.create_subprocess_exec(
        sys.executable,
        "-m",
        "playwright",
        "install",
        "--only-shell",
        "chromium",
        stdin=asyncio.subprocess.DEVNULL,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.STDOUT,
    )
    try:
        output, _ = await asyncio.wait_for(process.communicate(), _INSTALL_TIMEOUT_SECONDS)
    except TimeoutError as error:
        # Another install, even of another project, may hold Playwright's cache lock.
        process.kill()
        await process.wait()
        raise BrowserUnavailableError(
            f"the headless browser download did not finish in {_INSTALL_TIMEOUT_SECONDS}s"
        ) from error
    if process.returncode != 0:
        detail = output.decode("utf-8", errors="replace").strip().splitlines()[-1:]
        raise BrowserUnavailableError(
            "cannot download the headless browser: " + (detail[0] if detail else "no output")
        )
