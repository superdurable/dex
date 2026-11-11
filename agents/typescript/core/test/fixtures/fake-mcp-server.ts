// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A local MCP server for the tool source tests: `node fake-mcp-server.js stdio`, `... http <port>`, or
// `... http-sessions <port>`, which keeps protocol sessions and logs `session-open` and `session-closed`.
// Every real tool run appends `<tool> <idempotency key>` to the InvocationLog in the state directory.
// Tools: lookup (readOnlyHint; crash_first exits during its first run), audit_lookup (readOnlyHint),
// audit.lookup (collides with audit_lookup), record_note (idempotentHint), purge_cache (idempotentHint
// and destructiveHint), send_message (no annotations; gate waits at a FileGate, crash exits),
// long_report (readOnlyHint, taskSupport required; honors the requested TTL and logs it; completes
// after 200 ms, or with gate once every poll marked the gate and it is released; outcome "unknown" completes
// it with a result that reports the Dex outcome unknown), and ask_account (readOnlyHint, taskSupport required; its
// task needs input), and take_screenshot (readOnlyHint; text and a PNG image block, or with broken, bytes that are
// not an image). Dex MCP convention tools: get_balance declares the Dex effect read; post_note declares
// non_idempotent_write without approval, and readOnlyHint, which the declaration overrides; send_payment
// declares idempotent_write with recommended approval, applies one payment per idempotency key (logged as
// payment-applied <key>), answers a repeated key with "already applied", takes outcome "unknown" or
// "rejected" to answer with that Dex outcome, and crash_after_apply to exit right after it applies a new key.
// In http mode, outcome "unknown_error" or "rejected_error" answers with a JSON-RPC InternalError whose
// data._meta carries that Dex outcome.
// It also offers the resource memo://greeting and the prompt summarize, with one topic argument.

import { randomUUID } from "node:crypto";
import { createServer, type IncomingMessage } from "node:http";
import { join } from "node:path";

import { InMemoryTaskStore } from "@modelcontextprotocol/sdk/experimental/tasks/stores/in-memory.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { ErrorCode, type CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";

import {
  mcpIdempotencyKeyMetaKey,
  mcpToolApprovalMetaKey,
  mcpToolApprovalRecommended,
  mcpToolEffectMetaKey,
  mcpToolOutcomeMetaKey,
  mcpToolOutcomeRejected,
  mcpToolOutcomeUnknown,
} from "../../src/mcp/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";
import {
  fakeMcpInvocationLogName,
  fakeMcpReadyLine,
  fakeMcpStateDirectoryVariable,
  screenshotPngBase64,
} from "./fake-mcp-server-names.js";

const readOnly = { readOnlyHint: true };

/** Completes a gated task once its gate is released; every poll marks the gate. */
class GatedTaskStore extends InMemoryTaskStore {
  private readonly gatedTasks = new Map<
    string,
    { readonly gate: string; readonly topic: string; readonly outcome: string | undefined }
  >();

  public gateTask(taskId: string, gate: string, topic: string, outcome: string | undefined): void {
    this.gatedTasks.set(taskId, { gate, topic, outcome });
  }

  public override async updateTaskStatus(
    taskId: string,
    status: Parameters<InMemoryTaskStore["updateTaskStatus"]>[1],
    statusMessage?: string,
    sessionId?: string,
  ): Promise<void> {
    if (status === "cancelled") {
      invocations().record(`cancel ${taskId}`);
      this.gatedTasks.delete(taskId);
    }
    await super.updateTaskStatus(taskId, status, statusMessage, sessionId);
  }

  public override async getTask(taskId: string, sessionId?: string): ReturnType<InMemoryTaskStore["getTask"]> {
    const gated = this.gatedTasks.get(taskId);
    // A task bound to another protocol session stays invisible, as in the SDK's store.
    if (gated !== undefined && (await super.getTask(taskId, sessionId)) !== null) {
      const fileGate = new FileGate(stateDirectory(), gated.gate);
      fileGate.markReached();
      if (fileGate.isReleased()) {
        this.gatedTasks.delete(taskId);
        await this.storeTaskResult(taskId, "completed", reportResult(gated.topic, gated.outcome), sessionId);
      }
    }
    return super.getTask(taskId, sessionId);
  }
}

// Tasks outlive the per-request servers of the stateless HTTP mode.
const taskStore = new GatedTaskStore();

function stateDirectory(): string {
  const directory = process.env[fakeMcpStateDirectoryVariable];
  if (directory === undefined || directory === "") {
    throw new Error(`${fakeMcpStateDirectoryVariable} is not set`);
  }
  return directory;
}

function invocations(): InvocationLog {
  return new InvocationLog(join(stateDirectory(), fakeMcpInvocationLogName));
}

function recordRun(toolName: string, meta: Record<string, unknown> | undefined): string {
  const value = meta?.[mcpIdempotencyKeyMetaKey];
  const key = typeof value === "string" ? value : "no-key";
  invocations().record(`${toolName} ${key}`);
  return key;
}

function text(value: string): { content: [{ type: "text"; text: string }] } {
  return { content: [{ type: "text", text: value }] };
}

function reportResult(topic: string, outcome: string | undefined): CallToolResult {
  if (outcome === mcpToolOutcomeUnknown) {
    return { ...text(`report on ${topic}`), isError: true, _meta: { [mcpToolOutcomeMetaKey]: mcpToolOutcomeUnknown } };
  }
  return text(`report on ${topic}`);
}

function createFakeMcpServer(): McpServer {
  const server = new McpServer(
    { name: "fake-mcp", version: "1.0.0" },
    { capabilities: { tasks: { requests: { tools: { call: {} } }, cancel: {} } }, taskStore },
  );
  server.registerResource(
    "greeting",
    "memo://greeting",
    { description: "A greeting memo.", mimeType: "text/plain", annotations: { priority: 1 } },
    (uri) => ({ contents: [{ uri: uri.href, mimeType: "text/plain", text: "hello from the memo" }] }),
  );
  server.registerPrompt(
    "summarize",
    { description: "Summarize a topic.", argsSchema: { topic: z.string() } },
    ({ topic }) => ({
      description: "Summarize a topic.",
      messages: [{ role: "user", content: { type: "text", text: `Summarize ${topic} in one line.` } }],
    }),
  );
  server.registerTool(
    "lookup",
    { description: "Look up a record.", inputSchema: { query: z.string(), crash_first: z.boolean().optional() }, annotations: readOnly },
    ({ query, crash_first }, extra) => {
      const isFirstRun = !invocations().names().some((name) => name.startsWith("lookup "));
      recordRun("lookup", extra._meta);
      if (crash_first === true && isFirstRun) {
        process.exit(1);
      }
      return text(`found ${query}`);
    },
  );
  server.registerTool(
    "audit_lookup",
    { description: "Look up a record and write an audit entry.", inputSchema: { query: z.string() }, annotations: readOnly },
    ({ query }, extra) => {
      recordRun("audit_lookup", extra._meta);
      return text(`audited ${query}`);
    },
  );
  server.registerTool(
    "take_screenshot",
    { description: "Take a screenshot of the page.", inputSchema: { broken: z.boolean().optional() }, annotations: readOnly },
    ({ broken }, extra): CallToolResult => {
      recordRun("take_screenshot", extra._meta);
      return {
        content: [
          { type: "text", text: "captured the page" },
          { type: "image", data: broken === true ? "aGVsbG8=" : screenshotPngBase64, mimeType: "image/png" },
        ],
      };
    },
  );
  server.registerTool(
    "audit.lookup",
    { description: "Collides with audit_lookup as an agent tool name.", inputSchema: { query: z.string() } },
    ({ query }) => text(`colliding ${query}`),
  );
  server.registerTool(
    "purge_cache",
    {
      description: "Delete one cache entry; deleting it again changes nothing.",
      inputSchema: { key: z.string() },
      annotations: { idempotentHint: true, destructiveHint: true },
    },
    ({ key }) => text(`purged ${key}`),
  );
  server.registerTool(
    "record_note",
    { description: "Store a note; repeating a call changes nothing.", inputSchema: { note: z.string() }, annotations: { idempotentHint: true } },
    ({ note }, extra) => {
      recordRun("record_note", extra._meta);
      return text(`noted ${note}`);
    },
  );
  server.registerTool(
    "send_message",
    {
      description: "Send a message to a person.",
      inputSchema: { text: z.string(), gate: z.string().optional(), crash: z.boolean().optional() },
    },
    async ({ text: message, gate, crash }, extra) => {
      recordRun("send_message", extra._meta);
      if (crash === true) {
        process.exit(1);
      }
      if (gate !== undefined) {
        const fileGate = new FileGate(stateDirectory(), gate);
        fileGate.markReached();
        await fileGate.waitForRelease();
      }
      const progressToken = extra._meta?.progressToken;
      if (progressToken !== undefined) {
        await extra.sendNotification({
          method: "notifications/progress",
          params: { progressToken, progress: 1, total: 1, message: `sent ${message}` },
        });
      }
      return text(`sent ${message}`);
    },
  );
  server.registerTool(
    "get_balance",
    {
      description: "Read the balance of one account.",
      inputSchema: { account: z.string() },
      _meta: { [mcpToolEffectMetaKey]: "read" },
    },
    ({ account }, extra) => {
      recordRun("get_balance", extra._meta);
      return text(`balance of ${account} is 5`);
    },
  );
  server.registerTool(
    "post_note",
    {
      description: "Post a note; posting it again posts a second note.",
      inputSchema: { text: z.string() },
      annotations: readOnly,
      _meta: { [mcpToolEffectMetaKey]: "non_idempotent_write" },
    },
    ({ text: note }, extra) => {
      recordRun("post_note", extra._meta);
      return text(`posted ${note}`);
    },
  );
  server.registerTool(
    "send_payment",
    {
      description: "Send a payment; one payment per idempotency key.",
      inputSchema: { amount: z.number().int(), outcome: z.string().optional(), crash_after_apply: z.boolean().optional() },
      _meta: { [mcpToolEffectMetaKey]: "idempotent_write", [mcpToolApprovalMetaKey]: mcpToolApprovalRecommended },
    },
    ({ amount, outcome, crash_after_apply }, extra): CallToolResult => {
      const key = recordRun("send_payment", extra._meta);
      if (outcome === mcpToolOutcomeUnknown) {
        return {
          ...text("the payment provider timed out"),
          isError: true,
          _meta: { [mcpToolOutcomeMetaKey]: mcpToolOutcomeUnknown },
        };
      }
      if (outcome === mcpToolOutcomeRejected) {
        return { ...text("the card was declined"), isError: true, _meta: { [mcpToolOutcomeMetaKey]: mcpToolOutcomeRejected } };
      }
      // The Dex call ID derives from the idempotency key, so a retried attempt applies nothing.
      if (invocations().names().includes(`payment-applied ${key}`)) {
        return text(`payment of ${amount} already applied`);
      }
      invocations().record(`payment-applied ${key}`);
      if (crash_after_apply === true) {
        process.exit(1);
      }
      return text(`paid ${amount}`);
    },
  );
  server.experimental.tasks.registerToolTask(
    "long_report",
    {
      description: "Write a long report as a task.",
      inputSchema: { topic: z.string(), gate: z.string().optional(), outcome: z.string().optional() },
      annotations: readOnly,
      execution: { taskSupport: "required" },
    },
    {
      createTask: async ({ topic, gate, outcome }, extra) => {
        recordRun("long_report", extra._meta);
        invocations().record(`long_report-ttl ${extra.taskRequestedTtl ?? "none"}`);
        const task = await extra.taskStore.createTask({ ttl: extra.taskRequestedTtl ?? 60_000, pollInterval: 50 });
        if (gate !== undefined) {
          taskStore.gateTask(task.taskId, gate, topic, outcome);
          return { task };
        }
        // The shared store, because this request's server is closed when the result lands.
        setTimeout(() => {
          taskStore.storeTaskResult(task.taskId, "completed", reportResult(topic, outcome)).catch((error: unknown) => {
            console.error("storing the long_report result failed", error);
          });
        }, 200);
        return { task };
      },
      getTask: async (_arguments, extra) => {
        const task = await extra.taskStore.getTask(extra.taskId);
        if (task === null) {
          throw new Error(`unknown task ${extra.taskId}`);
        }
        return task;
      },
      getTaskResult: async (_arguments, extra) =>
        (await extra.taskStore.getTaskResult(extra.taskId)) as ReturnType<typeof text>,
    },
  );
  server.experimental.tasks.registerToolTask(
    "ask_account",
    { description: "Ask which account to use, as a task.", annotations: readOnly, execution: { taskSupport: "required" } },
    {
      createTask: async (extra) => {
        recordRun("ask_account", extra._meta);
        const task = await extra.taskStore.createTask({ ttl: extra.taskRequestedTtl ?? 60_000, pollInterval: 50 });
        await extra.taskStore.updateTaskStatus(task.taskId, "input_required", "Which account?");
        return { task };
      },
      getTask: async (extra) => {
        const task = await extra.taskStore.getTask(extra.taskId);
        if (task === null) {
          throw new Error(`unknown task ${extra.taskId}`);
        }
        return task;
      },
      getTaskResult: async (extra) => (await extra.taskStore.getTaskResult(extra.taskId)) as ReturnType<typeof text>,
    },
  );
  return server;
}

async function readJsonBody(request: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  return chunks.length === 0 ? undefined : JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

/**
 * Returns the JSON-RPC error answer of a send_payment call with outcome "unknown_error" or "rejected_error".
 * McpServer turns an error a tool throws into an isError result, so the answer is built here.
 */
function dexOutcomeErrorAnswer(body: unknown): Record<string, unknown> | undefined {
  if (!isRecord(body) || body["method"] !== "tools/call" || !isRecord(body["params"])) {
    return undefined;
  }
  const params = body["params"];
  const toolArguments = params["arguments"];
  if (params["name"] !== "send_payment" || !isRecord(toolArguments)) {
    return undefined;
  }
  const outcome = toolArguments["outcome"];
  if (outcome !== "unknown_error" && outcome !== "rejected_error") {
    return undefined;
  }
  recordRun("send_payment", isRecord(params["_meta"]) ? params["_meta"] : undefined);
  const isUnknown = outcome === "unknown_error";
  return {
    jsonrpc: "2.0",
    id: body["id"],
    error: {
      code: ErrorCode.InternalError,
      message: isUnknown ? "the payment provider timed out" : "the card was declined",
      data: { _meta: { [mcpToolOutcomeMetaKey]: isUnknown ? mcpToolOutcomeUnknown : mcpToolOutcomeRejected } },
    },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

const [mode, port] = process.argv.slice(2);
if (mode === "stdio") {
  await createFakeMcpServer().connect(new StdioServerTransport());
} else if (mode === "http-sessions" && port !== undefined) {
  const transports = new Map<string, StreamableHTTPServerTransport>();
  const httpServer = createServer((request, response) => {
    void (async () => {
      const sessionId = request.headers["mcp-session-id"];
      let transport = typeof sessionId === "string" ? transports.get(sessionId) : undefined;
      if (transport === undefined) {
        const sessionTransport: StreamableHTTPServerTransport = new StreamableHTTPServerTransport({
          sessionIdGenerator: () => randomUUID(),
          onsessioninitialized: (id) => {
            transports.set(id, sessionTransport);
            invocations().record(`session-open ${id}`);
          },
          onsessionclosed: (id) => {
            transports.delete(id);
            invocations().record(`session-closed ${id}`);
          },
        });
        transport = sessionTransport;
        await createFakeMcpServer().connect(transport);
      }
      await transport.handleRequest(request, response, await readJsonBody(request));
    })().catch((error: unknown) => {
      console.error("fake MCP server request failed", error);
      if (!response.headersSent) {
        response.writeHead(500).end();
      }
    });
  });
  httpServer.listen(Number(port), "127.0.0.1", () => {
    console.log(`${fakeMcpReadyLine} on ${port}`);
  });
} else if (mode === "http" && port !== undefined) {
  const httpServer = createServer((request, response) => {
    void (async () => {
      const body = await readJsonBody(request);
      const errorAnswer = dexOutcomeErrorAnswer(body);
      if (errorAnswer !== undefined) {
        response.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(errorAnswer));
        return;
      }
      // Stateless mode: one server and transport per request, no protocol session.
      const server = createFakeMcpServer();
      const transport = new StreamableHTTPServerTransport({});
      response.on("close", () => {
        void transport.close();
        void server.close();
      });
      await server.connect(transport);
      await transport.handleRequest(request, response, body);
    })().catch((error: unknown) => {
      console.error("fake MCP server request failed", error);
      if (!response.headersSent) {
        response.writeHead(500).end();
      }
    });
  });
  httpServer.listen(Number(port), "127.0.0.1", () => {
    console.log(`${fakeMcpReadyLine} on ${port}`);
  });
} else {
  throw new Error("usage: fake-mcp-server.js stdio | http <port> | http-sessions <port>");
}
