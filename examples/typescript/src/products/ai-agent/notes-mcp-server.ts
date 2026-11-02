/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// A credential-free demo MCP server over stdio: a read tool, a write tool, and a resource.
import { setTimeout as delay } from "node:timers/promises";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

const notes = [
  "Dex runs every model call and every tool call of an agent in its own Step.",
  "A durable wait survives Worker restarts because it is a Dex Timer.",
  "Write tools wait for approval before they run.",
];

const server = new McpServer({ name: "notes", version: "1.0.0" });

server.registerTool(
  "search_notes",
  {
    description: "Search the team notes for a word, optionally after a delay in seconds.",
    inputSchema: { query: z.string(), delay_seconds: z.number().int().min(0).max(60).optional() },
    annotations: { readOnlyHint: true },
  },
  async ({ query, delay_seconds }) => {
    await delay((delay_seconds ?? 0) * 1_000);
    const matches = notes.filter((note) => note.toLowerCase().includes(query.toLowerCase()));
    return { content: [{ type: "text", text: matches.join("\n") || `no notes mention ${query}` }] };
  },
);

server.registerResource("notes", "notes://all", { mimeType: "text/plain" }, (uri) => ({
  contents: [{ uri: uri.href, text: notes.join("\n") }],
}));

// No annotations: the agent classifies the tool as unclassified, so every call waits for approval.
server.registerTool(
  "post_message",
  {
    description: "Post a message to a team channel.",
    inputSchema: { channel: z.string(), text: z.string() },
  },
  ({ channel, text }) => ({ content: [{ type: "text", text: `posted to ${channel}: ${text}` }] }),
);

await server.connect(new StdioServerTransport());
