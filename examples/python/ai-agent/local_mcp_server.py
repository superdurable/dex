# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Credential-free MCP servers for the local AI Agent playground."""

from __future__ import annotations

import argparse
import re
import urllib.request
from html.parser import HTMLParser

import anyio
from mcp import types
from mcp.server.mcpserver import Context, MCPServer

MAX_PAGE_BYTES = 2 * 1024 * 1024
MAX_PAGE_CHARACTERS = 6_000
# Paces each stage so the chat page can show the tool's progress.
PROGRESS_PAUSE_SECONDS = 1.5


def search_server() -> MCPServer:
    server = MCPServer("dex-local-search")

    @server.tool(annotations=types.ToolAnnotations(read_only_hint=True))
    def web_search(query: str) -> str:
        return f"Demo search results for {query!r}. Configure Brave for live results."

    return server


def slack_server() -> MCPServer:
    server = MCPServer("dex-local-slack")

    @server.tool(annotations=types.ToolAnnotations(read_only_hint=True))
    def search_messages(query: str) -> str:
        return f"Demo Slack messages matching {query!r}."

    @server.tool()
    def post_message(channel: str, message: str) -> str:
        return f"Demo Slack message posted to {channel!r}: {message}"

    return server


def google_docs_server() -> MCPServer:
    server = MCPServer("dex-local-google-docs")

    @server.tool(annotations=types.ToolAnnotations(read_only_hint=True))
    def search_documents(query: str) -> str:
        return f"Demo Google Docs matching {query!r}."

    @server.tool()
    def create_document(title: str, content: str) -> str:
        return f"Demo Google Doc {title!r} created with {len(content)} characters."

    return server


def web_server() -> MCPServer:
    server = MCPServer("dex-local-web")

    @server.tool(annotations=types.ToolAnnotations(read_only_hint=True))
    async def fetch_page(url: str, ctx: Context) -> str:
        """Fetch a public web page and return its readable text."""
        if not url.startswith(("https://", "http://")):
            raise ValueError("url must start with https:// or http://")
        await ctx.report_progress(1, message=f"Fetching {url}")
        html = await anyio.to_thread.run_sync(_download_page, url)
        await anyio.sleep(PROGRESS_PAUSE_SECONDS)
        await ctx.report_progress(2, message=f"Downloaded {len(html) // 1024} KB")
        text = _readable_text(html)
        await anyio.sleep(PROGRESS_PAUSE_SECONDS)
        await ctx.report_progress(3, message=f"Extracted {len(text)} characters of text")
        return text[:MAX_PAGE_CHARACTERS]

    return server


def _download_page(url: str) -> str:
    request = urllib.request.Request(url, headers={"User-Agent": "dex-ai-agent-example"})
    with urllib.request.urlopen(request, timeout=20) as response:
        charset = response.headers.get_content_charset() or "utf-8"
        return response.read(MAX_PAGE_BYTES).decode(charset, errors="replace")


class _ReadableTextParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.parts: list[str] = []
        self._skipped_depth = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag in {"script", "style", "noscript", "svg"}:
            self._skipped_depth += 1

    def handle_endtag(self, tag: str) -> None:
        if tag in {"script", "style", "noscript", "svg"} and self._skipped_depth:
            self._skipped_depth -= 1

    def handle_data(self, data: str) -> None:
        if not self._skipped_depth:
            self.parts.append(data)


def _readable_text(html: str) -> str:
    parser = _ReadableTextParser()
    parser.feed(html)
    return re.sub(r"\s+", " ", " ".join(parser.parts)).strip()


SERVERS = {
    "search": search_server,
    "slack": slack_server,
    "google-docs": google_docs_server,
    "web": web_server,
}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("server", choices=SERVERS)
    arguments = parser.parse_args()
    SERVERS[arguments.server]().run("stdio")
