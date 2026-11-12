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

"""A stdio MCP server with one read tool, for the MCP quick start variant."""

from __future__ import annotations

from mcp.server.mcpserver import MCPServer
from mcp.types import ToolAnnotations

server = MCPServer("orders")


@server.tool(annotations=ToolAnnotations(read_only_hint=True))
def track_shipment(order_id: str) -> str:
    """Return the shipping status of one order."""
    return f"Order {order_id} is in transit."


if __name__ == "__main__":
    server.run()
