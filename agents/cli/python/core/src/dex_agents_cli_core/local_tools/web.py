# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The web fetch tool."""

from __future__ import annotations

from html.parser import HTMLParser
from urllib.parse import urlsplit

import httpx2

from dex_agents import ToolExecutionError

_FETCH_TIMEOUT_SECONDS = 30.0
_MAX_FETCHED_CHARACTERS = 100_000
_SKIPPED_ELEMENTS = frozenset({"script", "style", "noscript", "template"})
_BLOCK_ELEMENTS = frozenset(
    {"p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article"}
)


async def fetch_web_page(url: str) -> str:
    """Fetch a page with GET and return its text; HTML is reduced to its visible text.

    Args:
        url: An ``http`` or ``https`` URL.

    Returns:
        The final URL, status, content type, and text.

    Raises:
        ToolExecutionError: If the URL is not http or https, or the request fails.
    """
    if urlsplit(url).scheme not in ("http", "https"):
        raise ToolExecutionError(f"{url} is not an http or https URL")
    try:
        async with httpx2.AsyncClient(
            follow_redirects=True, timeout=_FETCH_TIMEOUT_SECONDS
        ) as client:
            response = await client.get(url)
    except httpx2.HTTPError as error:
        raise ToolExecutionError(f"cannot fetch {url}: {error}") from error
    content_type = response.headers.get("content-type", "")
    text = response.text
    if "html" in content_type:
        text = html_to_text(text)
    if len(text) > _MAX_FETCHED_CHARACTERS:
        omitted = len(text) - _MAX_FETCHED_CHARACTERS
        text = f"{text[:_MAX_FETCHED_CHARACTERS]}\n... ({omitted} more characters)"
    return (
        f"url: {response.url}\nstatus: {response.status_code}\n"
        f"content-type: {content_type}\n\n{text}"
    )


def html_to_text(html: str) -> str:
    """Return the visible text of an HTML document, one block per line.

    Args:
        html: The document.

    Returns:
        Its text without scripts, styles, or markup.
    """
    parser = _TextExtractor()
    parser.feed(html)
    parser.close()
    lines = (" ".join(line.split()) for line in "".join(parser.parts).splitlines())
    return "\n".join(line for line in lines if line)


class _TextExtractor(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.parts: list[str] = []
        self._skipped_depth = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag in _SKIPPED_ELEMENTS:
            self._skipped_depth += 1
        elif tag in _BLOCK_ELEMENTS:
            self.parts.append("\n")

    def handle_endtag(self, tag: str) -> None:
        if tag in _SKIPPED_ELEMENTS and self._skipped_depth > 0:
            self._skipped_depth -= 1
        elif tag in _BLOCK_ELEMENTS:
            self.parts.append("\n")

    def handle_data(self, data: str) -> None:
        if self._skipped_depth == 0:
            self.parts.append(data)
