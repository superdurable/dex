// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createServer, type IncomingMessage, type Server } from "node:http";

import { InMemoryTaskStore } from "@modelcontextprotocol/sdk/experimental/tasks/stores/in-memory.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";

import { canonicalOperationName, connectorToolMetaKeys } from "../../src/index.js";

/** One recorded tool run on the fake server. */
export interface FakeConnectorRun {
  readonly toolName: string;
  readonly arguments: Readonly<Record<string, unknown>>;
  readonly idempotencyKey: string | undefined;
  readonly meta: Readonly<Record<string, unknown>>;
}

/**
 * A stateless Streamable HTTP MCP server in the test process with connector tools that carry the connector,
 * connection, and operation in `_meta`, like the connectors MCP server.
 *
 * - `hackernews_get_top_stories` (hackernews/default, read-only)
 * - `gmail_get_message` (gmail/personal, read-only)
 * - `gmail_send_message` (gmail/personal, without operation keys in `_meta`)
 * - `slack_post_thread_reply` (slack/support-slack, a write without hints)
 * - `reports_long_report` (reports/default, a read-only task tool whose task stays working)
 */
export class FakeConnectorsMcpServer {
  /** Every tool run, in order. */
  public readonly runs: FakeConnectorRun[] = [];
  /** When set, every request answers 503 with this reason, as `dexcli dev` does before the server is ready. */
  public unavailableReason: string | undefined;
  /** When set, `initialize` still answers but every other request answers 503 with this reason. */
  public listingFailureReason: string | undefined;
  /** The connection that `slack_post_thread_reply` names; tests rebind it. */
  public slackConnection = "support-slack";
  /** The connection that the `reports_long_report` task tool names; tests rebind it. */
  public reportsConnection = "default";
  /** Tasks of `reports_long_report`, shared by the stateless per-request servers. */
  private readonly taskStore = new InMemoryTaskStore();
  private server: Server | undefined;

  /**
   * Starts the server on a free loopback port.
   * @returns The MCP URL.
   */
  public async start(): Promise<string> {
    this.server = createServer((request, response) => {
      void (async () => {
        if (this.unavailableReason !== undefined) {
          response.writeHead(503, { "content-type": "text/plain" }).end(this.unavailableReason);
          return;
        }
        const body = await readJsonBody(request);
        if (this.listingFailureReason !== undefined && !isInitializeRequest(body)) {
          response.writeHead(503, { "content-type": "text/plain" }).end(this.listingFailureReason);
          return;
        }
        const server = this.createMcpServer();
        const transport = new StreamableHTTPServerTransport({});
        response.on("close", () => {
          void transport.close();
          void server.close();
        });
        await server.connect(transport);
        await transport.handleRequest(request, response, body);
      })().catch((error: unknown) => {
        console.error("fake connectors MCP request failed", error);
        if (!response.headersSent) {
          response.writeHead(500).end();
        }
      });
    });
    const server = this.server;
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (address === null || typeof address === "string") {
      throw new Error("the fake MCP server has no port");
    }
    return `http://127.0.0.1:${address.port}/mcp`;
  }

  /**
   * Returns the runs of one tool.
   * @param toolName - The tool.
   * @returns Its runs.
   */
  public runsOf(toolName: string): FakeConnectorRun[] {
    return this.runs.filter((run) => run.toolName === toolName);
  }

  /**
   * Stops the server.
   * @returns A Promise that resolves after it closed.
   */
  public async stop(): Promise<void> {
    // Task TTL timers would keep the test process alive.
    this.taskStore.cleanup();
    const server = this.server;
    this.server = undefined;
    if (server !== undefined) {
      server.closeAllConnections();
      await new Promise<void>((resolve) => server.close(() => resolve()));
    }
  }

  private createMcpServer(): McpServer {
    const server = new McpServer(
      { name: "fake-connectors", version: "1.0.0" },
      { capabilities: { tasks: { requests: { tools: { call: {} } }, cancel: {} } }, taskStore: this.taskStore },
    );
    const record = (toolName: string, toolArguments: Record<string, unknown>, meta: Record<string, unknown> | undefined) => {
      const key = meta?.["io.superdurable.dex/idempotencyKey"];
      this.runs.push({
        toolName,
        arguments: toolArguments,
        idempotencyKey: typeof key === "string" ? key : undefined,
        meta: { ...meta },
      });
    };
    server.registerTool(
      "hackernews_get_top_stories",
      {
        description: "List the top Hacker News stories.",
        inputSchema: { limit: z.number().optional() },
        annotations: { readOnlyHint: true },
        _meta: identityMeta("hackernews", "default", "getTopStories"),
      },
      (toolArguments, extra) => {
        record("hackernews_get_top_stories", toolArguments, extra._meta);
        return { content: [{ type: "text", text: "top stories: dex-1, dex-2" }] };
      },
    );
    server.registerTool(
      "gmail_get_message",
      {
        description: "Read one email.",
        inputSchema: { id: z.string() },
        annotations: { readOnlyHint: true },
        _meta: identityMeta("gmail", "personal", "getMessage"),
      },
      (toolArguments, extra) => {
        record("gmail_get_message", toolArguments, extra._meta);
        return { content: [{ type: "text", text: `message ${toolArguments.id}` }] };
      },
    );
    server.experimental.tasks.registerToolTask(
      "reports_long_report",
      {
        description: "Write a long report as a task that stays working until the test completes it.",
        inputSchema: { topic: z.string() },
        annotations: { readOnlyHint: true },
        execution: { taskSupport: "required" },
        _meta: identityMeta("reports", this.reportsConnection, "longReport"),
      },
      {
        createTask: async (toolArguments, extra) => {
          record("reports_long_report", toolArguments, extra._meta);
          return { task: await extra.taskStore.createTask({ ttl: extra.taskRequestedTtl ?? 60_000, pollInterval: 50 }) };
        },
        getTask: async (_arguments, extra) => {
          const task = await extra.taskStore.getTask(extra.taskId);
          if (task === null) {
            throw new Error(`unknown task ${extra.taskId}`);
          }
          return task;
        },
        getTaskResult: async (_arguments, extra) => extra.taskStore.getTaskResult(extra.taskId) as never,
      },
    );
    server.registerTool(
      "gmail_send_message",
      {
        description: "Send an email. Its _meta lacks the operation, so an operation limit hides it.",
        inputSchema: { to: z.string() },
        _meta: {
          [connectorToolMetaKeys.connector]: "gmail",
          [connectorToolMetaKeys.connection]: "personal",
        },
      },
      (toolArguments, extra) => {
        record("gmail_send_message", toolArguments, extra._meta);
        return { content: [{ type: "text", text: `sent to ${toolArguments.to}` }] };
      },
    );
    server.registerTool(
      "slack_post_thread_reply",
      {
        description: "Reply in a Slack thread.",
        inputSchema: {
          text: z.string(),
          channel: z.string().optional(),
          token: z.string().optional(),
          body: z.string().optional(),
        },
        _meta: identityMeta("slack", this.slackConnection, "postThreadReply"),
      },
      (toolArguments, extra) => {
        record("slack_post_thread_reply", toolArguments, extra._meta);
        return { content: [{ type: "text", text: `posted ${toolArguments.text}` }] };
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
    [connectorToolMetaKeys.connectorVersion]: "1.0.0",
  };
}

function isInitializeRequest(body: unknown): boolean {
  return typeof body === "object" && body !== null && (body as { method?: unknown }).method === "initialize";
}

async function readJsonBody(request: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  const text = Buffer.concat(chunks).toString("utf8");
  return text === "" ? undefined : JSON.parse(text);
}
