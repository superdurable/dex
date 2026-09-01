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

"""Local MCP server used by AI Agent integration tests."""

from __future__ import annotations

import argparse

from mcp import types
from mcp.server.mcpserver import MCPServer

server = MCPServer("dex-ai-agent-test")


@server.tool(annotations=types.ToolAnnotations(read_only_hint=True))
def lookup(query: str) -> str:
    return f"found:{query}"


@server.tool()
def publish(message: str) -> str:
    return f"published:{message}"


@server.resource("test://guide")
def guide() -> str:
    return "Durable MCP resource"


@server.prompt()
def greeting(name: str) -> str:
    return f"Greet {name} and mention durable execution."


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--transport", choices=("stdio", "streamable-http"), default="stdio")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    arguments = parser.parse_args()
    server.run(arguments.transport, host=arguments.host, port=arguments.port)
