// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createServer, type IncomingMessage, type Server } from "node:http";

import { canonicalOperationName, connectorToolMetaKeys } from "@superdurable/dex-agents-cli-core";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";

/**
 * A stateless Streamable HTTP MCP server in the test process with a Hacker News read and a Slack write that carry
 * the connector, connection, and operation in `_meta`, like the connectors MCP server.
 *
 * It also serves an idempotent Notion write, a read named `read`, as Pi's own tool is, and a read named
 * `connector_read`, as the prefixed name of that tool is.
 */
export class FakeConnectorsMcpServer {
  /** The name of every tool run, in order. */
  public readonly runs: string[] = [];
  private server: Server | undefined;

  /**
   * Starts the server on a free loopback port.
   * @returns The MCP URL.
   */
  public async start(): Promise<string> {
    const server = createServer((request, response) => {
      void (async () => {
        const mcpServer = this.createMcpServer();
        const transport = new StreamableHTTPServerTransport({});
        response.on("close", () => {
          void transport.close();
          void mcpServer.close();
        });
        await mcpServer.connect(transport);
        await transport.handleRequest(request, response, await readJsonBody(request));
      })().catch((error: unknown) => {
        console.error("fake connectors MCP request failed", error);
        if (!response.headersSent) {
          response.writeHead(500).end();
        }
      });
    });
    this.server = server;
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (address === null || typeof address === "string") {
      throw new Error("the fake MCP server has no port");
    }
    return `http://127.0.0.1:${address.port}/mcp`;
  }

  /**
   * Returns how many times a tool really ran.
   * @param toolName - The tool.
   * @returns Its run count.
   */
  public runCount(toolName: string): number {
    return this.runs.filter((name) => name === toolName).length;
  }

  /**
   * Stops the server.
   * @returns A Promise that resolves after it closed.
   */
  public async stop(): Promise<void> {
    const server = this.server;
    this.server = undefined;
    if (server !== undefined) {
      server.closeAllConnections();
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  }

  private createMcpServer(): McpServer {
    const server = new McpServer({ name: "fake-connectors", version: "1.0.0" });
    server.registerTool(
      "hackernews_get_top_stories",
      {
        description: "List the top Hacker News stories.",
        inputSchema: {},
        annotations: { readOnlyHint: true },
        _meta: identityMeta("hackernews", "default", "getTopStories"),
      },
      () => {
        this.runs.push("hackernews_get_top_stories");
        return { content: [{ type: "text", text: "1. Durable agents; 2. Local first" }] };
      },
    );
    server.registerTool(
      "slack_post_thread_reply",
      {
        description: "Reply in a Slack thread.",
        inputSchema: { thread: z.string(), text: z.string() },
        _meta: identityMeta("slack", "support-slack", "postThreadReply"),
      },
      (toolArguments) => {
        this.runs.push("slack_post_thread_reply");
        return { content: [{ type: "text", text: `posted ${toolArguments.text} in ${toolArguments.thread}` }] };
      },
    );
    server.registerTool(
      "notion_update_page_title",
      {
        description: "Set a Notion page's title.",
        inputSchema: { page: z.string(), title: z.string() },
        annotations: { readOnlyHint: false, idempotentHint: true },
        _meta: identityMeta("notion", "default", "updatePageTitle"),
      },
      (toolArguments) => {
        this.runs.push("notion_update_page_title");
        return { content: [{ type: "text", text: `titled ${toolArguments.page} ${toolArguments.title}` }] };
      },
    );
    server.registerTool(
      "read",
      {
        description: "Read a file through the files connector.",
        inputSchema: { path: z.string() },
        annotations: { readOnlyHint: true },
        _meta: identityMeta("files", "default", "read"),
      },
      (toolArguments) => {
        this.runs.push("read");
        return { content: [{ type: "text", text: `read through the files connector: ${toolArguments.path}` }] };
      },
    );
    server.registerTool(
      "connector_read",
      {
        description: "Read a file through the archive connector.",
        inputSchema: { path: z.string() },
        annotations: { readOnlyHint: true },
        _meta: identityMeta("archive", "default", "connectorRead"),
      },
      (toolArguments) => {
        this.runs.push("connector_read");
        return { content: [{ type: "text", text: `read through the archive connector: ${toolArguments.path}` }] };
      },
    );
    return server;
  }
}

function identityMeta(connector: string, connection: string, operation: string): Record<string, unknown> {
  return {
    [connectorToolMetaKeys.connector]: connector,
    [connectorToolMetaKeys.connection]: connection,
    [connectorToolMetaKeys.operation]: operation,
    [connectorToolMetaKeys.operationName]: canonicalOperationName(operation),
  };
}

async function readJsonBody(request: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  const text = Buffer.concat(chunks).toString("utf8");
  return text === "" ? undefined : JSON.parse(text);
}
