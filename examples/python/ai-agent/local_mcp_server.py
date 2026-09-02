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

from mcp import types
from mcp.server.mcpserver import MCPServer


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


SERVERS = {
    "search": search_server,
    "slack": slack_server,
    "google-docs": google_docs_server,
}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("server", choices=SERVERS)
    arguments = parser.parse_args()
    SERVERS[arguments.server]().run("stdio")
