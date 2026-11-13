// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createServer, type IncomingMessage, type Server } from "node:http";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";

/** One recorded tool run on the fake server. */
export interface FakeRemoteMcpRun {
  readonly toolName: string;
  readonly arguments: Readonly<Record<string, unknown>>;
}

/**
 * A stateless Streamable HTTP MCP server in the test process that answers only requests carrying
 * `Authorization: Bearer <token>`, like a remote MCP server that needs an API key. Like a careless server, it echoes
 * the `Authorization` header it got in its HTTP 401 and 500 bodies.
 *
 * - `search_assets`: read-only.
 * - `publish_asset`: a write without hints.
 * - `tag_asset`: an idempotent write.
 * - `report_failure`: fails with a tool error whose text holds the bearer token.
 * - `crash_report`: answered with HTTP 500 and a body that echoes the `Authorization` header.
 */
export class FakeRemoteMcpServer {
  /** Every tool run, in order. */
  public readonly runs: FakeRemoteMcpRun[] = [];
  /** How many requests carried the expected bearer token. */
  public authorizedRequests = 0;
  /** How many requests carried another `Authorization` header or none; each got HTTP 401. */
  public rejectedRequests = 0;
  /** Whether any request carried an `Mcp-Session-Id`, which a stateless server never issues. */
  public hasSeenSessionId = false;
  private server: Server | undefined;

  /**
   * Creates the server.
   * @param expectedToken - The bearer token every request must carry.
   */
  public constructor(private readonly expectedToken: string) {}

  /**
   * Starts the server on a free loopback port.
   * @returns The MCP URL.
   */
  public async start(): Promise<string> {
    this.server = createServer((request, response) => {
      void (async () => {
        if (request.headers["mcp-session-id"] !== undefined) {
          this.hasSeenSessionId = true;
        }
        const authorization = request.headers.authorization ?? "";
        if (authorization !== `Bearer ${this.expectedToken}`) {
          this.rejectedRequests += 1;
          response.writeHead(401, { "content-type": "text/plain" }).end(`missing or wrong bearer token: ${authorization}`);
          return;
        }
        this.authorizedRequests += 1;
        const body = await readJsonBody(request);
        if (isToolCallOf(body, "crash_report")) {
          this.runs.push({ toolName: "crash_report", arguments: {} });
          response.writeHead(500, { "content-type": "text/plain" }).end(`internal error for Authorization: ${authorization}`);
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
        console.error("fake remote MCP request failed", error);
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
   * @param toolName - The tool's name on this server.
   * @returns Its runs.
   */
  public runsOf(toolName: string): FakeRemoteMcpRun[] {
    return this.runs.filter((run) => run.toolName === toolName);
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
    const server = new McpServer({ name: "fake-remote", version: "1.0.0" });
    server.registerTool(
      "search_assets",
      {
        description: "Search the asset library.",
        inputSchema: { query: z.string() },
        annotations: { readOnlyHint: true },
      },
      (toolArguments) => {
        this.runs.push({ toolName: "search_assets", arguments: toolArguments });
        return { content: [{ type: "text", text: `assets for ${toolArguments.query}` }] };
      },
    );
    server.registerTool(
      "publish_asset",
      { description: "Publish one asset.", inputSchema: { name: z.string() } },
      (toolArguments) => {
        this.runs.push({ toolName: "publish_asset", arguments: toolArguments });
        return { content: [{ type: "text", text: `published ${toolArguments.name}` }] };
      },
    );
    server.registerTool(
      "tag_asset",
      {
        description: "Set one tag on an asset.",
        inputSchema: { name: z.string(), tag: z.string() },
        annotations: { readOnlyHint: false, idempotentHint: true, destructiveHint: false },
      },
      (toolArguments) => {
        this.runs.push({ toolName: "tag_asset", arguments: toolArguments });
        return { content: [{ type: "text", text: `tagged ${toolArguments.name} ${toolArguments.tag}` }] };
      },
    );
    server.registerTool(
      "report_failure",
      { description: "Fail with an upstream error.", inputSchema: { name: z.string() } },
      (toolArguments) => {
        this.runs.push({ toolName: "report_failure", arguments: toolArguments });
        return {
          isError: true,
          content: [{ type: "text", text: `upstream refused ${toolArguments.name} for Bearer ${this.expectedToken}` }],
        };
      },
    );
    server.registerTool("crash_report", { description: "Crash the server.", inputSchema: {} }, () => ({
      content: [{ type: "text", text: "never reached" }],
    }));
    return server;
  }
}

function isToolCallOf(body: unknown, toolName: string): boolean {
  const message = body as { readonly method?: unknown; readonly params?: { readonly name?: unknown } } | undefined;
  return message?.method === "tools/call" && message.params?.name === toolName;
}

async function readJsonBody(request: IncomingMessage): Promise<unknown> {
  if (request.method !== "POST") {
    return undefined;
  }
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8")) as unknown;
}
