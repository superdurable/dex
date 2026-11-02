// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { setTimeout as delay } from "node:timers/promises";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { Transport } from "@modelcontextprotocol/sdk/shared/transport.js";
import {
  CallToolResultSchema,
  CreateTaskResultSchema,
  ErrorCode,
  McpError,
  type CallToolResult,
  type Progress,
  type Task,
  type Tool,
} from "@modelcontextprotocol/sdk/types.js";

import {
  ToolExecutionError,
  toolEffectFromMcpToolAnnotations,
  type AgentEventWriter,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type McpToolAnnotations,
  type ToolExecutionResult,
  type ToolPolicy,
} from "../agent-framework-runner.js";
import type { ToolEffect } from "../agent-session-values.js";
import { validateMcpServerConfig, type McpServerConfig } from "./mcp-server-config.js";

/** `_meta` key that carries the session's idempotency key on every `tools/call`. */
export const mcpIdempotencyKeyMetaKey = "io.superdurable.dex/idempotencyKey";
/** `AgentEvents` type for an MCP `notifications/progress` of a running tool call. */
export const mcpToolProgressEventType = "mcp.tool_progress";
/** `AgentEvents` type for each observed status of an MCP task. */
export const mcpTaskStatusEventType = "mcp.task_status";
/** Default bound on one MCP `tools/call` or task request, in milliseconds: 10 minutes. */
export const defaultMcpRequestTimeoutMs = 600_000;
/** Default bound on polling one MCP task in one tool Step attempt, in milliseconds: 10 minutes. */
export const defaultMcpTaskWaitTimeoutMs = 600_000;
/** Default bound on connecting, the `initialize` handshake, and `tools/list`, in milliseconds: 30 seconds. */
export const defaultMcpConnectionTimeoutMs = 30_000;
/** Default number of connection attempts before a request reports the server unreachable. */
export const defaultMcpConnectionAttempts = 3;

const toolNameLengthLimit = 64;
const toolListingPageLimit = 100;
const connectionRetryDelayMs = 500;
const defaultTaskPollIntervalMs = 1_000;
const minimumTaskPollIntervalMs = 50;
const maximumTaskPollIntervalMs = 30_000;
const taskCancelTimeoutMs = 5_000;
const sessionTerminationTimeoutMs = 5_000;
// The requested task TTL outlasts CallTool's three bounded waits, with margin for retry delays.
const taskTtlWaitMultiple = 4;
// The server or a gateway may report InternalError after the call ran, so it leaves the outcome unknown.
const noResultErrorCodes: ReadonlySet<number> = new Set([
  ErrorCode.ConnectionClosed,
  ErrorCode.RequestTimeout,
  ErrorCode.InternalError,
]);

/** Handler that runs one deferred tool call, as {@link McpToolSource.executeDeferredToolCall} does. */
export type DeferredToolHandler = (call: DeferredToolExecution) => Promise<ToolExecutionResult>;

/** One MCP tool described as an agent tool definition, returned by {@link McpToolSource.listTools}. */
export interface McpTool {
  /**
   * Agent tool name, `<serverName>__<mcpToolName>`. Characters other than letters, digits, `_`, and
   * `-` become `_`. A name longer than 64 characters is cut to 55 and ends with `_` and 8 hex digits
   * of its SHA-256. Key {@link ToolPolicy} entries by this name.
   */
  readonly name: string;
  /** Name of the configured server that offers the tool. */
  readonly serverName: string;
  /** The tool's name on its MCP server. */
  readonly mcpToolName: string;
  /** Tool description for the model. */
  readonly description: string;
  /** JSON Schema of the tool's arguments object. */
  readonly inputSchema: Readonly<Record<string, unknown>>;
  /** The server's behavior hints, or `undefined` when it sent none. */
  readonly annotations: McpToolAnnotations | undefined;
  /**
   * Effect from {@link toolEffectFromMcpToolAnnotations} when the server configuration trusts the
   * annotations, else `undefined`. An explicit {@link ToolPolicy} overrides it.
   */
  readonly frameworkEffect: ToolEffect | undefined;
  /**
   * The tool's MCP `execution.taskSupport`. A `required` tool is called as a task and polled inside
   * its tool Step.
   */
  readonly taskSupport: "required" | "optional" | "forbidden" | undefined;
}

/** Options of {@link McpToolSource}. */
export interface McpToolSourceOptions {
  /**
   * Bound on one `tools/call` or task request in milliseconds. A `tools/call` that times out throws
   * {@link McpToolCallInterruptedError}. Defaults to {@link defaultMcpRequestTimeoutMs}.
   */
  readonly requestTimeoutMs?: number;
  /**
   * Bound on polling one MCP task in one tool Step attempt, in milliseconds. When it passes, the
   * source throws {@link McpToolCallInterruptedError} and leaves the task running; a retried attempt
   * resumes polling it. The source requests a task TTL of four times this bound, so the task outlives
   * every attempt. Defaults to {@link defaultMcpTaskWaitTimeoutMs}.
   */
  readonly taskWaitTimeoutMs?: number;
  /**
   * Bound on connecting, the `initialize` handshake, and each `tools/list` request, in milliseconds.
   * Defaults to {@link defaultMcpConnectionTimeoutMs}.
   */
  readonly connectionTimeoutMs?: number;
  /** Connection attempts per request. Defaults to {@link defaultMcpConnectionAttempts}. */
  readonly connectionAttempts?: number;
  /** `clientInfo.name` sent to the servers. Defaults to `dex-agents`. */
  readonly clientName?: string;
}

/** Per-call options of {@link McpToolSource.callTool}. */
export interface McpToolCallOptions {
  /** Key sent in `_meta` under {@link mcpIdempotencyKeyMetaKey}. Defaults to none. */
  readonly idempotencyKey?: string;
  /** Tool call ID stamped on progress and task events. */
  readonly toolCallId?: string;
  /** Writer for `mcp.tool_progress` and `mcp.task_status` events. */
  readonly events?: AgentEventWriter;
  /** Awaited between task polls. */
  readonly heartbeat?: () => Promise<void>;
  /** `DeferredToolExecution.heartbeatCheckpoint`, so a retried attempt resumes the earlier attempt's task. */
  readonly heartbeatCheckpoint?: string;
  /** `DeferredToolExecution.saveHeartbeatCheckpoint`, so an MCP task survives a retry. */
  readonly saveHeartbeatCheckpoint?: (checkpoint: string) => Promise<void>;
  /** Aborts the requests, for example the tool Step's cancellation signal. */
  readonly signal?: AbortSignal;
}

/**
 * A `tools/call` that reached an MCP server but returned no result.
 *
 * {@link McpToolSource} throws it when the connection broke, the request timed out, the server process
 * exited, the server or a gateway answered with an internal error, an HTTP error status, or an unreadable
 * body, or an MCP task stopped being observable. The tool may or may not have taken effect. It is not a
 * {@link ToolExecutionError}, so the tool Step's effect decides what happens next: `CallTool` re-issues
 * a `read` or `idempotent_write` call as a new request with the same idempotency key, and
 * `CallSingleAttemptTool` records `outcome_unknown` for the other effects and never sends it again.
 */
export class McpToolCallInterruptedError extends Error {
  /**
   * Creates the error.
   * @param serverName - Configured name of the MCP server.
   * @param mcpToolName - The tool's name on that server.
   * @param detail - What went wrong.
   */
  public constructor(
    public readonly serverName: string,
    public readonly mcpToolName: string,
    public readonly detail: string,
  ) {
    super(`MCP tool ${mcpToolName} of server ${serverName} returned no result: ${detail}`);
    this.name = "McpToolCallInterruptedError";
  }
}

/**
 * No configured MCP server could be listed.
 *
 * {@link McpToolSource.listTools} leaves out a server it cannot list and logs a warning; it throws this
 * error only when every configured server failed and none has an earlier listing. Thrown from
 * `runModelStep`, it fails the `RunModel` attempt, which retries.
 */
export class McpToolListingError extends Error {
  /**
   * Creates the error.
   * @param serverName - Configured name of the last MCP server that failed.
   * @param detail - What went wrong with that server.
   */
  public constructor(
    public readonly serverName: string,
    public readonly detail: string,
  ) {
    super(`could not list the tools of MCP server ${serverName}: ${detail}`);
    this.name = "McpToolListingError";
  }
}

type ToolCallOutcome =
  | { readonly kind: "succeeded"; readonly output: string }
  | { readonly kind: "failed"; readonly message: string }
  | { readonly kind: "interrupted"; readonly detail: string }
  | { readonly kind: "server_unreachable"; readonly detail: string }
  | { readonly kind: "task_lost"; readonly detail: string };

/** Where one call goes: the configured server, the tool's name there, and its task support. */
interface McpToolTarget {
  readonly server: McpServerConfig;
  readonly mcpToolName: string;
  readonly toolName: string;
  readonly taskSupport: McpTool["taskSupport"];
}

interface McpConnection {
  readonly client: Client;
  readonly transport: Transport;
}

/**
 * Offers the tools of configured MCP servers to an agent, one Dex Step per call.
 *
 * Configure the servers on the Worker, never from Flow input, a user message, or a browser.
 * {@link listTools} lists each server's tools and caches them per server as {@link McpTool} definitions
 * that a runner offers to the model. When the model calls one, the runner returns
 * {@link deferredToolCall}, so the session runs the call in its own tool Step, and the Step calls
 * {@link executeDeferredToolCall}. No MCP call runs inside `RunModel`. {@link deferredToolCall} stores
 * the server and the tool's MCP name on the call's record, so the tool Step calls that server directly,
 * whatever the other servers do.
 *
 * Each tool's effect comes from its annotations through {@link toolEffectFromMcpToolAnnotations}:
 * `readOnlyHint` makes it `read`, and `idempotentHint` makes it `idempotent_write`. Any other tool gets
 * the session's `defaultToolEffect`, `unclassified` by default. Annotations are trusted by default. A
 * {@link ToolPolicy} in `AgentSessionOptions.toolPolicies` keyed by {@link McpTool.name} wins.
 *
 * The source uses `@modelcontextprotocol/sdk` 1.x, which speaks MCP up to 2025-11-25: every request
 * opens a new connection, performs the `initialize` handshake, ends the HTTP protocol session, and
 * closes it, so no protocol session outlives a request. Every `tools/call` carries the session's
 * idempotency key in `_meta` under {@link mcpIdempotencyKeyMetaKey}.
 *
 * Results map onto the session contract:
 * - A result becomes the tool output: text blocks joined with newlines, other blocks and structured
 *   content as JSON.
 * - An `isError` result, a JSON-RPC error response other than an internal error, and a failed,
 *   cancelled, or `input_required` task throw {@link ToolExecutionError}, so the session records
 *   `failed` and the model sees the text.
 * - No result after the request was sent throws {@link McpToolCallInterruptedError}; the tool Step's
 *   effect decides whether it is re-issued.
 * - A call that was never sent, because its server is not configured or cannot be reached, throws
 *   {@link ToolExecutionError}.
 * - A tool whose `execution.taskSupport` is `required` is called as a task and polled with `tasks/get`
 *   inside the tool Step, with a heartbeat per poll, for at most `taskWaitTimeoutMs` per attempt. For a
 *   Streamable HTTP server the task ID is saved as the attempt's heartbeat checkpoint, so a retried
 *   attempt polls the same task instead of calling again. A server that binds tasks to its protocol
 *   session, as the SDK's task store does, cannot show the task to the retry's new session, so the
 *   retry calls the tool again.
 *
 * Share one source between the parallel tool Steps of a Worker; build it once at startup.
 *
 * @example
 * ```ts
 * const tools = new McpToolSource([
 *   { transport: "stdio", name: "files", command: "mcp-server-files" },
 *   { transport: "streamable_http", name: "tickets", url: ticketsUrl },
 * ]);
 * class MyRunner implements AgentFrameworkRunner {
 *   // buildUserMessage omitted
 *   public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
 *     const offered = await tools.listTools(); // name, description, inputSchema
 *     const response = await callMyModel(turn.messages, offered);
 *     return {
 *       kind: "tool_calls_requested",
 *       frameworkRunState: JSON.stringify(response),
 *       usage: response.usage,
 *       toolCalls: response.toolCalls.map((call) =>
 *         tools.deferredToolCall(turn, { toolCallId: call.id, toolName: call.name, arguments: call.arguments })),
 *     };
 *   }
 *   public executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
 *     return tools.executeDeferredToolCall(call);
 *   }
 * }
 * ```
 */
export class McpToolSource {
  private readonly servers: ReadonlyMap<string, McpServerConfig>;
  private readonly requestTimeoutMs: number;
  private readonly taskWaitTimeoutMs: number;
  private readonly connectionTimeoutMs: number;
  private readonly connectionAttempts: number;
  private readonly connector: McpConnector;
  private readonly toolsByServer = new Map<string, readonly McpTool[]>();
  private tools: ReadonlyMap<string, McpTool> | undefined;
  private pendingListing: Promise<ReadonlyMap<string, McpTool>> | undefined;

  /**
   * Describes the servers without connecting to them.
   * @param servers - Server configurations with unique names.
   * @param options - Timeouts, connection attempts, and client name.
   * @throws {TypeError} When a configuration is invalid, server names repeat, a timeout is not positive,
   * or `connectionAttempts` is less than 1.
   */
  public constructor(servers: readonly McpServerConfig[], options: McpToolSourceOptions = {}) {
    const serversByName = new Map<string, McpServerConfig>();
    for (const server of servers) {
      validateMcpServerConfig(server);
      if (serversByName.has(server.name)) {
        throw new TypeError(`MCP server name ${server.name} is configured twice`);
      }
      serversByName.set(server.name, server);
    }
    this.servers = serversByName;
    this.requestTimeoutMs = options.requestTimeoutMs ?? defaultMcpRequestTimeoutMs;
    this.taskWaitTimeoutMs = options.taskWaitTimeoutMs ?? defaultMcpTaskWaitTimeoutMs;
    this.connectionTimeoutMs = options.connectionTimeoutMs ?? defaultMcpConnectionTimeoutMs;
    this.connectionAttempts = options.connectionAttempts ?? defaultMcpConnectionAttempts;
    if (!(this.requestTimeoutMs > 0) || !(this.taskWaitTimeoutMs > 0) || !(this.connectionTimeoutMs > 0)) {
      throw new TypeError("requestTimeoutMs, taskWaitTimeoutMs, and connectionTimeoutMs must be positive");
    }
    if (!Number.isInteger(this.connectionAttempts) || this.connectionAttempts < 1) {
      throw new TypeError("connectionAttempts must be an integer of at least 1");
    }
    this.connector = new McpConnector(
      { name: options.clientName ?? "dex-agents", version: dexAgentsVersion() },
      this.connectionTimeoutMs,
    );
  }

  /**
   * Returns the tools of every reachable configured server.
   *
   * Each server is listed on first use and cached on its own; `refresh: true` lists every server again.
   * A server that cannot be listed is left out with a warning and tried again on the next call; when a
   * refresh fails, its earlier listing stays. When two tools map to the same agent tool name, the first
   * in server order wins and the other is left out with a warning. Call it from `runModelStep` to offer
   * the tools, or at Worker startup.
   * @param options - `refresh: true` lists every server again.
   * @returns Every offered tool, in server order and then in each server's order.
   * @throws {McpToolListingError} When no server could be listed and none has an earlier listing.
   */
  public async listTools(options: { readonly refresh?: boolean } = {}): Promise<readonly McpTool[]> {
    while (this.pendingListing !== undefined) {
      await this.pendingListing.catch(() => undefined);
    }
    const listing = this.listServers(options.refresh === true);
    this.pendingListing = listing;
    try {
      return [...(await listing).values()];
    } finally {
      this.pendingListing = undefined;
    }
  }

  /**
   * Returns one listed tool by its agent tool name.
   * @param toolName - {@link McpTool.name}.
   * @returns The cached definition.
   * @throws {RangeError} When {@link listTools} has not run or no listed server offers the tool.
   */
  public tool(toolName: string): McpTool {
    if (this.tools === undefined) {
      throw new RangeError("call listTools before looking up an MCP tool");
    }
    const tool = this.tools.get(toolName);
    if (tool === undefined) {
      throw new RangeError(`no listed MCP server offers the tool ${toolName}`);
    }
    return tool;
  }

  /**
   * Returns a {@link ToolPolicy} for every listed tool whose annotations declare an effect.
   *
   * Use it with a framework adapter that offers the MCP tools as its own tools: pass it in the
   * adapter's `toolPolicies` and spread your own policies after it, so yours win.
   * @example
   * ```ts
   * await tools.listTools();
   * const toolPolicies = { ...tools.toolPolicies(), files__delete: { effect: "non_idempotent_write" } };
   * ```
   * @returns Policies by {@link McpTool.name}, with the effect's default approval.
   * @throws {RangeError} When {@link listTools} has not run.
   */
  public toolPolicies(): Record<string, ToolPolicy> {
    if (this.tools === undefined) {
      throw new RangeError("call listTools before reading MCP tool policies");
    }
    const policies: Record<string, ToolPolicy> = {};
    for (const tool of this.tools.values()) {
      if (tool.frameworkEffect !== undefined) {
        policies[tool.name] = { effect: tool.frameworkEffect };
      }
    }
    return policies;
  }

  /**
   * Builds the {@link DeferredToolCall} for one model call of a listed MCP tool.
   *
   * The policy comes from `turn.resolveToolPolicy` with the tool's annotation effect, so an explicit
   * {@link ToolPolicy} wins. The call's `toolSourceReference` names the server, the tool's MCP name, and
   * its task support.
   * @param turn - The `RunModel` context.
   * @param call - Framework tool call ID, the {@link McpTool.name} the model called, and the arguments as
   * JSON text.
   * @returns The call to put in `ToolCallsRequested.toolCalls`.
   * @throws {RangeError} When {@link listTools} has not run or the tool is unknown.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    const tool = this.tool(call.toolName);
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: turn.resolveToolPolicy(call.toolName, tool.frameworkEffect),
      toolSourceReference: JSON.stringify({
        mcp_server: tool.serverName,
        mcp_tool: tool.mcpToolName,
        ...(tool.taskSupport === undefined ? {} : { task_support: tool.taskSupport }),
      }),
    };
  }

  /**
   * Returns one deferred-call handler per listed MCP tool, keyed by agent tool name.
   *
   * Merge them with a runner's own handlers and dispatch `executeDeferredToolCall` by
   * `call.toolCall.toolName`. Build the map inside `executeDeferredToolCall`, not once at construction:
   * a replacement Worker runs tool Steps before any `RunModel`.
   * @example
   * ```ts
   * public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
   *   const handlers = { ...this.localHandlers, ...(await tools.deferredToolHandlers()) };
   *   return handlers[call.toolCall.toolName]!(call);
   * }
   * ```
   * @returns Handlers that each call {@link executeDeferredToolCall}.
   * @throws {McpToolListingError} When no server could be listed.
   */
  public async deferredToolHandlers(): Promise<Record<string, DeferredToolHandler>> {
    const handlers: Record<string, DeferredToolHandler> = {};
    for (const tool of await this.listTools()) {
      handlers[tool.name] = (call) => this.executeDeferredToolCall(call);
    }
    return handlers;
  }

  /**
   * Runs one deferred MCP tool call in its tool Step.
   *
   * Call it from `AgentFrameworkRunner.executeDeferredToolCall`. A call built by {@link deferredToolCall}
   * goes straight to the server named on its record, without a listing. It passes the call's idempotency
   * key, events, heartbeat, heartbeat checkpoint, and cancellation signal on.
   * @param call - The approved call from the tool Step.
   * @returns The tool output the model sees.
   * @throws {ToolExecutionError} When the arguments are not a JSON object, the tool failed, the server
   * answered with an error or asked for input, or the call was never sent.
   * @throws {McpToolCallInterruptedError} When the call was sent and no result came back.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolArguments = parseToolArguments(call.toolCall.arguments);
    const target =
      this.targetFromReference(call.toolCall.toolName, call.toolCall.toolSourceReference) ??
      (await this.targetFromListing(call.toolCall.toolName));
    return this.callTarget(target, toolArguments, {
      idempotencyKey: call.idempotencyKey,
      toolCallId: call.toolCall.toolCallId,
      events: call.events,
      heartbeat: () => call.heartbeat(),
      ...(call.heartbeatCheckpoint === undefined ? {} : { heartbeatCheckpoint: call.heartbeatCheckpoint }),
      ...(call.saveHeartbeatCheckpoint === undefined
        ? {}
        : { saveHeartbeatCheckpoint: (checkpoint: string) => call.saveHeartbeatCheckpoint!(checkpoint) }),
      signal: call.cancellationSignal,
    });
  }

  /**
   * Calls one listed MCP tool once, as a tool Step does.
   *
   * Use it from a framework tool that wraps an MCP tool, when the adapter gives the tool only the
   * idempotency key. It finds the tool's server through the per-server listing. Call it only inside a
   * tool Step: the session's retry rules for the effect assume it.
   * @param toolName - {@link McpTool.name}.
   * @param toolArguments - Tool arguments.
   * @param options - Idempotency key, events, heartbeat, checkpoints, and abort signal.
   * @returns The tool output the model sees.
   * @throws {ToolExecutionError} When the tool failed, the server answered with an error or asked for
   * input, or the call was never sent.
   * @throws {McpToolCallInterruptedError} When the call was sent and no result came back.
   */
  public async callTool(
    toolName: string,
    toolArguments: Readonly<Record<string, unknown>>,
    options: McpToolCallOptions = {},
  ): Promise<ToolExecutionResult> {
    return this.callTarget(await this.targetFromListing(toolName), { ...toolArguments }, options);
  }

  private async callTarget(
    target: McpToolTarget,
    toolArguments: Record<string, unknown>,
    options: McpToolCallOptions,
  ): Promise<ToolExecutionResult> {
    const limits = {
      requestTimeoutMs: this.requestTimeoutMs,
      taskWaitTimeoutMs: this.taskWaitTimeoutMs,
      connectionAttempts: this.connectionAttempts,
    };
    const call = new McpToolCall(this.connector, limits, target, toolArguments, options);
    const outcome = await call.run(taskIdFromCheckpoint(options.heartbeatCheckpoint, target));
    switch (outcome.kind) {
      case "succeeded":
        return { output: outcome.output };
      case "failed":
        throw new ToolExecutionError(outcome.message);
      case "interrupted":
      case "task_lost":
        throw new McpToolCallInterruptedError(target.server.name, target.mcpToolName, outcome.detail);
      case "server_unreachable":
        throw new ToolExecutionError(
          `could not connect to MCP server ${target.server.name}, so ${target.mcpToolName} did not run: ${outcome.detail}`,
        );
    }
  }

  private targetFromReference(toolName: string, toolSourceReference: string | undefined): McpToolTarget | undefined {
    const reference = parseToolSourceReference(toolSourceReference);
    if (reference === undefined) {
      return undefined;
    }
    const server = this.servers.get(reference.serverName);
    if (server === undefined) {
      throw new ToolExecutionError(
        `MCP server ${reference.serverName} is no longer configured, so ${reference.mcpToolName} did not run`,
      );
    }
    return { server, mcpToolName: reference.mcpToolName, toolName, taskSupport: reference.taskSupport };
  }

  private async targetFromListing(toolName: string): Promise<McpToolTarget> {
    let tool: McpTool | undefined;
    try {
      tool = (await this.listTools()).find((listed) => listed.name === toolName);
      if (tool === undefined) {
        tool = (await this.listTools({ refresh: true })).find((listed) => listed.name === toolName);
      }
    } catch (error: unknown) {
      if (error instanceof McpToolListingError) {
        throw new ToolExecutionError(`${error.message}, so ${toolName} did not run`, { cause: error });
      }
      throw error;
    }
    const server = tool === undefined ? undefined : this.servers.get(tool.serverName);
    if (tool === undefined || server === undefined) {
      throw new ToolExecutionError(`no listed MCP server offers the tool ${toolName}`);
    }
    return { server, mcpToolName: tool.mcpToolName, toolName: tool.name, taskSupport: tool.taskSupport };
  }

  private async listServers(isRefresh: boolean): Promise<ReadonlyMap<string, McpTool>> {
    let failure: McpToolListingError | undefined;
    let hasChanged = this.tools === undefined;
    for (const server of this.servers.values()) {
      if (!isRefresh && this.toolsByServer.has(server.name)) {
        continue;
      }
      try {
        this.toolsByServer.set(server.name, await this.listServerTools(server));
        hasChanged = true;
      } catch (error: unknown) {
        if (!(error instanceof McpToolListingError)) {
          throw error;
        }
        failure = error;
        console.warn(`left MCP server ${server.name} out of the tool listing: ${error.message}`);
      }
    }
    if (this.toolsByServer.size === 0 && failure !== undefined) {
      throw failure;
    }
    if (hasChanged || this.tools === undefined) {
      this.tools = this.combineServerTools();
    }
    return this.tools;
  }

  private combineServerTools(): ReadonlyMap<string, McpTool> {
    const tools = new Map<string, McpTool>();
    for (const serverName of this.servers.keys()) {
      for (const tool of this.toolsByServer.get(serverName) ?? []) {
        const existing = tools.get(tool.name);
        if (existing !== undefined) {
          console.warn(
            `left MCP tool ${tool.serverName}/${tool.mcpToolName} out: ${existing.serverName}/${existing.mcpToolName} already has the agent tool name ${tool.name}`,
          );
          continue;
        }
        tools.set(tool.name, tool);
      }
    }
    return tools;
  }

  private async listServerTools(server: McpServerConfig): Promise<McpTool[]> {
    let detail = "no listing attempt";
    for (let listingAttempt = 1; listingAttempt <= this.connectionAttempts; listingAttempt += 1) {
      let connection: McpConnection | undefined;
      try {
        connection = await this.connector.connect(server);
        const listed: Tool[] = [];
        let cursor: string | undefined;
        for (let page = 0; page < toolListingPageLimit; page += 1) {
          const result = await connection.client.listTools(cursor === undefined ? {} : { cursor }, {
            timeout: this.connectionTimeoutMs,
          });
          listed.push(...result.tools);
          cursor = result.nextCursor;
          if (cursor === undefined || cursor === "") {
            break;
          }
        }
        return listed.map((tool) => mcpToolFromListing(server, tool));
      } catch (error: unknown) {
        detail = describeError(error);
      } finally {
        await this.connector.disconnectQuietly(connection);
      }
      if (listingAttempt < this.connectionAttempts) {
        await delay(connectionRetryDelayMs * listingAttempt);
      }
    }
    throw new McpToolListingError(server.name, detail);
  }
}

/** Opens one connection per request with a bounded handshake, and ends it. */
class McpConnector {
  public constructor(
    private readonly clientInfo: { readonly name: string; readonly version: string },
    private readonly connectionTimeoutMs: number,
  ) {}

  public async connect(server: McpServerConfig): Promise<McpConnection> {
    const client = new Client(this.clientInfo, { capabilities: {} });
    let transport: Transport;
    if (server.transport === "stdio") {
      transport = new StdioClientTransport({
        command: server.command,
        args: [...(server.args ?? [])],
        ...(server.env === undefined ? {} : { env: { ...server.env } }),
        ...(server.cwd === undefined ? {} : { cwd: server.cwd }),
        stderr: "inherit",
      });
    } else {
      transport = new StreamableHTTPClientTransport(new URL(server.url), {
        requestInit: { headers: { ...(server.headers ?? {}) } },
      });
    }
    const connection = { client, transport };
    try {
      await client.connect(transport, { timeout: this.connectionTimeoutMs });
    } catch (error: unknown) {
      await this.disconnectQuietly(connection);
      throw error;
    }
    return connection;
  }

  /** Ends the HTTP protocol session, then closes the connection; failures are logged. */
  public async disconnectQuietly(connection: McpConnection | undefined): Promise<void> {
    if (connection === undefined) {
      return;
    }
    const { client, transport } = connection;
    try {
      if (transport instanceof StreamableHTTPClientTransport && transport.sessionId !== undefined) {
        await Promise.race([
          transport.terminateSession(),
          delay(sessionTerminationTimeoutMs).then(() => {
            throw new Error(`no answer to the session DELETE within ${sessionTerminationTimeoutMs} ms`);
          }),
        ]);
      }
    } catch (error: unknown) {
      // The server times idle sessions out; the outcome is already known.
      console.warn(`ending an MCP session failed: ${describeError(error)}`);
    }
    try {
      await client.close();
    } catch (error: unknown) {
      // The outcome is already known; a failed close only leaks this one connection.
      console.warn(`closing an MCP connection failed: ${describeError(error)}`);
    }
  }
}

interface McpCallLimits {
  readonly requestTimeoutMs: number;
  readonly taskWaitTimeoutMs: number;
  readonly connectionAttempts: number;
}

/** One tool call: connection attempts, the request, and task polling. */
class McpToolCall {
  private readonly reporter: McpToolCallReporter;

  public constructor(
    private readonly connector: McpConnector,
    private readonly limits: McpCallLimits,
    private readonly target: McpToolTarget,
    private readonly toolArguments: Record<string, unknown>,
    private readonly options: McpToolCallOptions,
  ) {
    this.reporter = new McpToolCallReporter(target.toolName, options.toolCallId, options.events);
  }

  public async run(resumedTaskId: string | undefined): Promise<ToolCallOutcome> {
    if (resumedTaskId !== undefined) {
      const outcome = await this.withConnection((client) => this.awaitTask(client, resumedTaskId, undefined));
      if (outcome.kind === "server_unreachable") {
        // An earlier attempt started the task, so it may be running.
        return {
          kind: "interrupted",
          detail: `could not reach the server to poll MCP task ${resumedTaskId}: ${outcome.detail}`,
        };
      }
      if (outcome.kind !== "task_lost") {
        return outcome;
      }
      // Only CallTool retries an attempt, and it may send the call again.
      console.warn(`calling MCP tool ${this.target.mcpToolName} again: ${outcome.detail}`);
    }
    return this.withConnection((client) => this.sendToolCall(client));
  }

  private async withConnection(operation: (client: Client) => Promise<ToolCallOutcome>): Promise<ToolCallOutcome> {
    const { connectionAttempts } = this.limits;
    let outcome: ToolCallOutcome = { kind: "server_unreachable", detail: "no connection attempt" };
    for (let connectionAttempt = 1; connectionAttempt <= connectionAttempts; connectionAttempt += 1) {
      outcome = await this.runOnNewConnection(operation);
      if (outcome.kind !== "server_unreachable") {
        return outcome;
      }
      if (connectionAttempt < connectionAttempts) {
        await delay(connectionRetryDelayMs * connectionAttempt);
      }
    }
    return outcome;
  }

  private async runOnNewConnection(operation: (client: Client) => Promise<ToolCallOutcome>): Promise<ToolCallOutcome> {
    let connection: McpConnection;
    try {
      connection = await this.connector.connect(this.target.server);
    } catch (error: unknown) {
      return { kind: "server_unreachable", detail: describeError(error) };
    }
    try {
      return await operation(connection.client);
    } catch (error: unknown) {
      return { kind: "interrupted", detail: `the connection broke before a result arrived: ${describeError(error)}` };
    } finally {
      await this.connector.disconnectQuietly(connection);
    }
  }

  private async sendToolCall(client: Client): Promise<ToolCallOutcome> {
    const meta =
      this.options.idempotencyKey === undefined ? undefined : { [mcpIdempotencyKeyMetaKey]: this.options.idempotencyKey };
    const params = { name: this.target.mcpToolName, arguments: this.toolArguments, ...(meta === undefined ? {} : { _meta: meta }) };
    const requestOptions = {
      timeout: this.limits.requestTimeoutMs,
      onprogress: (progress: Progress) => this.reporter.writeProgress(progress),
      ...(this.options.signal === undefined ? {} : { signal: this.options.signal }),
    };
    const isTaskCall =
      this.target.taskSupport === "required" && client.getServerCapabilities()?.tasks?.requests?.tools?.call !== undefined;
    let createdTask: Task;
    try {
      if (!isTaskCall) {
        const response = await client.callTool(params, CallToolResultSchema, requestOptions);
        if (!("content" in response)) {
          // A pre-2024-11-05 server answers with a bare toolResult.
          return { kind: "succeeded", output: JSON.stringify(response.toolResult) };
        }
        return outcomeFromToolResult(response as CallToolResult);
      }
      createdTask = (
        await client.request({ method: "tools/call", params }, CreateTaskResultSchema, {
          ...requestOptions,
          task: { ttl: this.limits.taskWaitTimeoutMs * taskTtlWaitMultiple },
        })
      ).task;
    } catch (error: unknown) {
      if (error instanceof McpError && !noResultErrorCodes.has(error.code)) {
        return { kind: "failed", message: error.message };
      }
      throw error;
    }
    if (this.options.saveHeartbeatCheckpoint !== undefined && this.target.server.transport === "streamable_http") {
      // A stdio server's tasks end with its process, so only HTTP tasks resume.
      await this.options.saveHeartbeatCheckpoint(taskCheckpoint(this.target, createdTask.taskId));
    }
    // The task exists and may run on, so a polling failure propagates as an unknown outcome.
    return this.awaitTask(client, createdTask.taskId, createdTask);
  }

  private async awaitTask(client: Client, taskId: string, created: Task | undefined): Promise<ToolCallOutcome> {
    const { requestTimeoutMs, taskWaitTimeoutMs } = this.limits;
    const deadline = Date.now() + taskWaitTimeoutMs;
    let task = created;
    for (;;) {
      if (task !== undefined) {
        this.reporter.writeTaskStatus(task);
        if (task.status === "completed") {
          const result = await client.experimental.tasks.getTaskResult(taskId, CallToolResultSchema, {
            timeout: requestTimeoutMs,
          });
          return outcomeFromToolResult(result);
        }
        if (task.status === "failed") {
          return { kind: "failed", message: `MCP task ${taskId} failed: ${task.statusMessage ?? "no error detail"}` };
        }
        if (task.status === "cancelled") {
          const reason = task.statusMessage === undefined ? "" : `: ${task.statusMessage}`;
          return { kind: "failed", message: `MCP task ${taskId} was cancelled${reason}` };
        }
        if (task.status === "input_required") {
          await cancelTaskQuietly(client, taskId);
          const detail = task.statusMessage === undefined ? "" : ` (${task.statusMessage})`;
          return {
            kind: "failed",
            message: `the MCP server asked for input that this agent session cannot provide yet${detail}, so the tool did not finish`,
          };
        }
        const remainingMs = deadline - Date.now();
        if (remainingMs <= 0) {
          return {
            kind: "interrupted",
            detail: `MCP task ${taskId} did not finish within ${taskWaitTimeoutMs} ms in this attempt; it keeps running, and a retried attempt resumes polling it`,
          };
        }
        await this.options.heartbeat?.();
        await delay(Math.min(taskPollIntervalMs(task), remainingMs));
      }
      try {
        task = await client.experimental.tasks.getTask(taskId, { timeout: requestTimeoutMs });
      } catch (error: unknown) {
        if (created === undefined && error instanceof McpError && !noResultErrorCodes.has(error.code)) {
          return { kind: "task_lost", detail: `tasks/get for MCP task ${taskId} failed: ${error.message}` };
        }
        throw error;
      }
    }
  }
}

class McpToolCallReporter {
  public constructor(
    private readonly toolName: string,
    private readonly toolCallId: string | undefined,
    private readonly events: AgentEventWriter | undefined,
  ) {}

  public writeProgress(progress: Progress): void {
    this.events?.writeEvent(mcpToolProgressEventType, {
      tool_call_id: this.toolCallId ?? null,
      tool_name: this.toolName,
      progress: progress.progress,
      total: progress.total ?? null,
      message: progress.message ?? null,
    });
  }

  public writeTaskStatus(task: Task): void {
    this.events?.writeEvent(mcpTaskStatusEventType, {
      tool_call_id: this.toolCallId ?? null,
      tool_name: this.toolName,
      task_id: task.taskId,
      status: task.status,
      status_message: task.statusMessage ?? null,
    });
  }
}

function mcpToolFromListing(server: McpServerConfig, tool: Tool): McpTool {
  const annotations: McpToolAnnotations | undefined =
    tool.annotations === undefined
      ? undefined
      : {
          ...(tool.annotations.readOnlyHint === undefined ? {} : { readOnlyHint: tool.annotations.readOnlyHint }),
          ...(tool.annotations.idempotentHint === undefined ? {} : { idempotentHint: tool.annotations.idempotentHint }),
          ...(tool.annotations.destructiveHint === undefined ? {} : { destructiveHint: tool.annotations.destructiveHint }),
        };
  const trustsAnnotations = server.trustsToolAnnotations ?? true;
  return {
    name: buildAgentToolName(server.name, tool.name),
    serverName: server.name,
    mcpToolName: tool.name,
    description: tool.description ?? tool.title ?? `MCP tool ${tool.name} of ${server.name}`,
    inputSchema: tool.inputSchema,
    annotations,
    frameworkEffect: trustsAnnotations && annotations !== undefined ? toolEffectFromMcpToolAnnotations(annotations) : undefined,
    taskSupport: tool.execution?.taskSupport,
  };
}

function buildAgentToolName(serverName: string, mcpToolName: string): string {
  const candidate = `${serverName}__${mcpToolName}`.replace(/[^A-Za-z0-9_-]/g, "_");
  if (candidate.length <= toolNameLengthLimit) {
    return candidate;
  }
  const digest = createHash("sha256").update(candidate).digest("hex").slice(0, 8);
  return `${candidate.slice(0, 55)}_${digest}`;
}

function parseToolArguments(argumentsJson: string): Record<string, unknown> {
  let toolArguments: unknown;
  try {
    toolArguments = JSON.parse(argumentsJson);
  } catch (error: unknown) {
    throw new ToolExecutionError(`the tool arguments are not valid JSON: ${describeError(error)}`, { cause: error });
  }
  if (toolArguments === null || typeof toolArguments !== "object" || Array.isArray(toolArguments)) {
    throw new ToolExecutionError("the tool arguments must be a JSON object");
  }
  return toolArguments as Record<string, unknown>;
}

function parseJsonObject(text: string | undefined): Record<string, unknown> | undefined {
  if (text === undefined) {
    return undefined;
  }
  try {
    const value: unknown = JSON.parse(text);
    return value !== null && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
  } catch {
    // Text from another tool source, not an MCP reference.
    return undefined;
  }
}

function parseToolSourceReference(
  toolSourceReference: string | undefined,
): { readonly serverName: string; readonly mcpToolName: string; readonly taskSupport: McpTool["taskSupport"] } | undefined {
  const reference = parseJsonObject(toolSourceReference);
  const serverName = reference?.["mcp_server"];
  const mcpToolName = reference?.["mcp_tool"];
  if (typeof serverName !== "string" || typeof mcpToolName !== "string") {
    return undefined;
  }
  const taskSupport = reference?.["task_support"];
  return {
    serverName,
    mcpToolName,
    taskSupport: taskSupport === "required" || taskSupport === "optional" || taskSupport === "forbidden" ? taskSupport : undefined,
  };
}

function taskCheckpoint(target: McpToolTarget, taskId: string): string {
  return JSON.stringify({ mcp_server: target.server.name, mcp_tool: target.mcpToolName, mcp_task_id: taskId });
}

function taskIdFromCheckpoint(checkpoint: string | undefined, target: McpToolTarget): string | undefined {
  const saved = parseJsonObject(checkpoint);
  if (saved?.["mcp_server"] !== target.server.name || saved["mcp_tool"] !== target.mcpToolName) {
    return undefined;
  }
  const taskId = saved["mcp_task_id"];
  return typeof taskId === "string" ? taskId : undefined;
}

function outcomeFromToolResult(result: CallToolResult): ToolCallOutcome {
  const parts = result.content.map((block) => (block.type === "text" ? block.text : JSON.stringify(block)));
  if (parts.length === 0 && result.structuredContent !== undefined) {
    parts.push(JSON.stringify(result.structuredContent));
  }
  const text = parts.join("\n");
  if (result.isError === true) {
    return { kind: "failed", message: text === "" ? "the MCP tool reported an error" : text };
  }
  return { kind: "succeeded", output: text };
}

function taskPollIntervalMs(task: Task): number {
  if (task.pollInterval === undefined) {
    return defaultTaskPollIntervalMs;
  }
  return Math.min(Math.max(task.pollInterval, minimumTaskPollIntervalMs), maximumTaskPollIntervalMs);
}

async function cancelTaskQuietly(client: Client, taskId: string): Promise<void> {
  try {
    await client.experimental.tasks.cancelTask(taskId, { timeout: taskCancelTimeoutMs });
  } catch (error: unknown) {
    // Cancellation is cooperative; the tool's outcome is already reported either way.
    console.warn(`tasks/cancel for MCP task ${taskId} failed: ${describeError(error)}`);
  }
}

function describeError(error: unknown): string {
  if (error instanceof AggregateError) {
    return error.errors.map(describeError).join("; ");
  }
  if (error instanceof Error) {
    const cause = error.cause instanceof Error ? ` (${error.cause.message})` : "";
    return `${error.name}: ${error.message}${cause}`;
  }
  return String(error);
}

function dexAgentsVersion(): string {
  try {
    const manifest = createRequire(import.meta.url)("../../../package.json") as { version?: unknown };
    return typeof manifest.version === "string" ? manifest.version : "0";
  } catch {
    // A bundled copy has no package.json beside it; the version is informational.
    return "0";
  }
}
