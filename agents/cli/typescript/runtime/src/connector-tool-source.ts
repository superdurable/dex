// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ToolExecutionError,
  type AgentToolCall,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type SourceToolDefinition,
  type ToolSource,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import { McpToolSource, type McpTool } from "@superdurable/dex-agents/mcp";
import { Client as McpClient } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

import {
  canonicalOperationName,
  isCoveredByConnectorEntry,
  type ConnectorEntry,
  type TurnConfig,
} from "./cli-config.js";
import type { TurnConfigResolver } from "./turn-config-resolver.js";

/**
 * The `_meta` keys with which the connectors MCP server identifies each connector tool. The runtime filters
 * connector tools only by these keys and never parses tool names.
 *
 * - `connector`: the connector ID, such as `slack`.
 * - `connection`: the connection name, such as `support-slack`.
 * - `operation`: the operationId, such as `postThreadReply`.
 * - `operationName`: the operationId in snake case, such as `post_thread_reply`.
 * - `connectorVersion`: the connector's release version.
 *
 * @since 0.5.0
 */
export const connectorToolMetaKeys = Object.freeze({
  connector: "io.superdurable.connectors/connector",
  connection: "io.superdurable.connectors/connection",
  operation: "io.superdurable.connectors/operation",
  operationName: "io.superdurable.connectors/operationName",
  connectorVersion: "io.superdurable.connectors/connectorVersion",
});

const mcpServerName = "connectors";
const identityListingTtlMs = 30_000;
const probeTimeoutMs = 5_000;
const disabledReferenceKey = "disabled_tool";

/**
 * The connector, connection, and operation of one connector MCP tool, from its `_meta`.
 *
 * @since 0.5.0
 */
export interface ConnectorToolIdentity {
  /** The tool name on the MCP server, which is also the name the agent sees. */
  readonly toolName: string;
  /** Connector ID, such as `slack`. */
  readonly connector: string;
  /** Connection name, such as `support-slack`. */
  readonly connection: string;
  /** The operationId, such as `postThreadReply`, or `null` when `_meta` has none. */
  readonly operation: string | null;
  /** The snake-case operation name, such as `post_thread_reply`, or `null` when `_meta` has none. */
  readonly operationName: string | null;
  /** The connector's release version, or `null` when `_meta` has none. */
  readonly connectorVersion: string | null;
}

/**
 * Whether a connectors MCP server answers, and why not when it does not.
 *
 * @since 0.5.0
 */
export type ConnectorsMcpAvailability =
  | { readonly isAvailable: true }
  | { readonly isAvailable: false; readonly reason: string };

/** A listed tool with the MCP tool it renames. */
interface ConnectorSourceTool extends SourceToolDefinition {
  readonly mcpTool: McpTool;
}

/**
 * The connector tools of a connectors MCP server, as a {@link ToolSource} that applies each turn's
 * `connectors.enabled` and `approval.always`.
 *
 * It wraps `McpToolSource`, so each call runs in its own tool Step with the session's idempotency key, and the
 * server's effect, approval, and outcome conventions apply. The agent sees each tool by its MCP name, such as
 * `slack_post_thread_reply`, without the `connectors__` prefix. While `connectors.enabled` is set, a tool whose
 * `_meta` names no connector and connection is hidden, and an entry that limits operations also needs the
 * tool's `operation` and `operationName`. A server that cannot be listed offers no tools in that model call, so
 * `connectors.mcp: auto` keeps working when the server starts or returns later.
 *
 * @since 0.5.0
 */
export class ConnectorsMcpToolSource implements ToolSource {
  /** The stable source name stored with each call. */
  public readonly sourceName = "connectors";
  private readonly mcp: McpToolSource;
  private identities: { readonly expiresAt: number; readonly byToolName: ReadonlyMap<string, ConnectorToolIdentity> } =
    { expiresAt: 0, byToolName: new Map() };
  private lastListing: readonly ConnectorSourceTool[] = [];
  private lastListingFailure: string | undefined;

  /**
   * Creates the source.
   * @param url - The connectors MCP server's Streamable HTTP URL.
   * @param turns - The per-turn configuration.
   * @param log - Writes a line when the server becomes unavailable or available again.
   */
  public constructor(
    private readonly url: string,
    private readonly turns: TurnConfigResolver,
    private readonly log: (line: string) => void,
  ) {
    this.mcp = new McpToolSource(
      [{ name: mcpServerName, transport: "streamable_http", url, trustsToolAnnotations: true }],
      { resourceAndPromptToolsEnabled: false, clientName: "dex-agents-cli" },
    );
  }

  /**
   * Lists the tools the running turn enables.
   * @returns The tools, named as the agent sees them.
   */
  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    const turn = this.turns.currentTurn();
    let tools: readonly McpTool[];
    let identities: ReadonlyMap<string, ConnectorToolIdentity>;
    try {
      [tools, identities] = await Promise.all([this.mcp.listTools(), this.readIdentities()]);
    } catch (error: unknown) {
      const reason = describeError(error);
      if (reason !== this.lastListingFailure) {
        this.log(`Connector tools are unavailable for now: ${reason}`);
        this.lastListingFailure = reason;
      }
      this.lastListing = [];
      return [];
    }
    if (this.lastListingFailure !== undefined) {
      this.log("Connector tools are available again.");
      this.lastListingFailure = undefined;
    }
    const listed: ConnectorSourceTool[] = [];
    for (const tool of tools) {
      if (this.isToolEnabled(tool.mcpToolName, identities, turn)) {
        listed.push({ name: tool.mcpToolName, description: tool.description, inputSchema: tool.inputSchema, mcpTool: tool });
      }
    }
    this.lastListing = listed;
    return listed;
  }

  /**
   * Returns the last listing without I/O.
   * @returns The tools.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.lastListing;
  }

  /**
   * Builds the deferred call, with the turn's approval rules.
   * @param turn - The `RunModel` context.
   * @param call - The model's call.
   * @returns The deferred call.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall {
    const turnConfig = this.turns.turnOf(turn);
    const mcpTool = this.findMcpTool(call.listedTool, call.toolName);
    if (mcpTool === undefined || !this.isToolEnabled(call.toolName, this.identities.byToolName, turnConfig)) {
      return {
        toolCallId: call.toolCallId,
        toolName: call.toolName,
        arguments: call.arguments,
        policy: { effect: "read", requiresApproval: false },
        toolSourceReference: JSON.stringify({ [disabledReferenceKey]: call.toolName }),
      };
    }
    const inner = this.mcp.deferredToolCall(turn, {
      toolCallId: call.toolCallId,
      toolName: mcpTool.name,
      arguments: call.arguments,
      listedTool: mcpTool,
    });
    return {
      ...inner,
      toolName: call.toolName,
      policy: {
        effect: inner.policy.effect,
        requiresApproval: inner.policy.requiresApproval || turnConfig.approval.always.includes(call.toolName),
      },
    };
  }

  /**
   * Runs one call in its tool Step.
   * @param call - The approved call.
   * @returns The tool's outcome.
   * @throws {ToolExecutionError} When the tool is switched off for the session or failed.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    throwWhenDisabled(call.toolCall);
    return this.mcp.executeDeferredToolCall(call);
  }

  /**
   * Polls a task that {@link executeDeferredToolCall} started.
   * @param poll - The call's record.
   * @returns The poll outcome.
   */
  public pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return this.mcp.pollToolTask(poll);
  }

  /**
   * Cancels an abandoned task.
   * @param cancellation - The call's record.
   * @returns A Promise that resolves once the server took the cancellation.
   */
  public cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    return this.mcp.cancelToolTask(cancellation);
  }

  /**
   * Returns the connector identity of every tool the server lists now, whatever the configuration enables.
   * @returns The identities, by tool name.
   * @throws {Error} When the server cannot be listed.
   */
  public async readIdentities(): Promise<ReadonlyMap<string, ConnectorToolIdentity>> {
    if (Date.now() < this.identities.expiresAt) {
      return this.identities.byToolName;
    }
    const byToolName = new Map<string, ConnectorToolIdentity>();
    for (const identity of await listConnectorToolIdentities(this.url)) {
      byToolName.set(identity.toolName, identity);
    }
    this.identities = { expiresAt: Date.now() + identityListingTtlMs, byToolName };
    return byToolName;
  }

  private isToolEnabled(
    toolName: string,
    identities: ReadonlyMap<string, ConnectorToolIdentity>,
    turn: TurnConfig,
  ): boolean {
    const enabled = turn.connectors.enabled;
    if (enabled === null) {
      return true;
    }
    const identity = identities.get(toolName);
    return identity !== undefined && enabled.some((entry) => isConnectorToolEnabledBy(entry, identity));
  }

  private findMcpTool(listedTool: SourceToolDefinition, toolName: string): McpTool | undefined {
    const fromListing = (listedTool as Partial<ConnectorSourceTool>).mcpTool;
    if (fromListing !== undefined && listedTool.name === toolName) {
      return fromListing;
    }
    return this.lastListing.find((tool) => tool.name === toolName)?.mcpTool;
  }
}

/**
 * Lists the connector identity of each tool a connectors MCP server offers, from the tools' `_meta`. A tool without
 * a connector and a connection in `_meta` is left out.
 *
 * @param url - The server's Streamable HTTP URL.
 * @returns The identities, in listing order.
 * @throws {Error} When the server cannot be reached or listed.
 * @since 0.5.0
 */
export async function listConnectorToolIdentities(url: string): Promise<ConnectorToolIdentity[]> {
  const client = new McpClient({ name: "dex-agents-cli", version: "0.5.0" });
  await client.connect(new StreamableHTTPClientTransport(new URL(url)));
  try {
    const identities: ConnectorToolIdentity[] = [];
    let cursor: string | undefined;
    do {
      const page = await client.listTools(cursor === undefined ? {} : { cursor });
      for (const tool of page.tools) {
        const meta = (tool._meta ?? {}) as Record<string, unknown>;
        const connector = readMetaString(meta, connectorToolMetaKeys.connector);
        const connection = readMetaString(meta, connectorToolMetaKeys.connection);
        if (connector !== null && connection !== null) {
          identities.push({
            toolName: tool.name,
            connector,
            connection,
            operation: readMetaString(meta, connectorToolMetaKeys.operation),
            operationName: readMetaString(meta, connectorToolMetaKeys.operationName),
            connectorVersion: readMetaString(meta, connectorToolMetaKeys.connectorVersion),
          });
        }
      }
      cursor = page.nextCursor;
    } while (cursor !== undefined && cursor !== "");
    return identities;
  } finally {
    await client.close();
  }
}

/**
 * Checks whether a connectors MCP server answers an `initialize` request.
 *
 * `dexcli dev` serves the server at `<Dex Web URL>/mcp` and answers 503 with a reason until it is ready. An older
 * `dexcli` answers 404.
 *
 * @param url - The server's Streamable HTTP URL.
 * @returns Whether it is available, and the reason when it is not.
 * @since 0.5.0
 */
export async function probeConnectorsMcp(url: string): Promise<ConnectorsMcpAvailability> {
  let response: Response;
  try {
    response = await fetch(url, {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "dex-agents-cli", version: "0.5.0" } },
      }),
      signal: AbortSignal.timeout(probeTimeoutMs),
    });
  } catch (error: unknown) {
    return { isAvailable: false, reason: `nothing answers at ${url}: ${describeError(error)}` };
  }
  const body = await response.text().catch(() => "");
  const sessionId = response.headers.get("mcp-session-id");
  if (sessionId !== null) {
    await fetch(url, { method: "DELETE", headers: { "mcp-session-id": sessionId } }).catch(() => undefined);
  }
  if (response.ok) {
    return { isAvailable: true };
  }
  if (response.status === 404) {
    return { isAvailable: false, reason: `${url} serves no MCP server (HTTP 404); dexcli dev serves one from dexcli 1.43` };
  }
  const reason = body.trim().slice(0, 500);
  return { isAvailable: false, reason: `${url} answered HTTP ${response.status}${reason === "" ? "" : `: ${reason}`}` };
}

/**
 * Returns whether a `connectors.enabled` entry enables a connector tool. The connector and, when the entry names
 * one, the connection must match. An entry that limits operations needs both the tool's `operation` and
 * `operationName`, and one of them must name a listed operation.
 *
 * @param entry - The entry.
 * @param identity - The tool's identity from `_meta`.
 * @returns `true` when the entry enables the tool.
 * @since 0.5.0
 */
export function isConnectorToolEnabledBy(entry: ConnectorEntry, identity: ConnectorToolIdentity): boolean {
  if (!isCoveredByConnectorEntry(entry, identity.connector, identity.connection)) {
    return false;
  }
  if (entry.tools === null) {
    return true;
  }
  if (identity.operation === null || identity.operationName === null) {
    return false;
  }
  return entry.tools.includes(identity.operationName) || entry.tools.includes(canonicalOperationName(identity.operation));
}

function readMetaString(meta: Record<string, unknown>, key: string): string | null {
  const value = meta[key];
  return typeof value === "string" && value !== "" ? value : null;
}

function throwWhenDisabled(toolCall: AgentToolCall): void {
  if (toolCall.toolSourceReference === undefined || toolCall.toolSourceReference === null) {
    return;
  }
  let reference: unknown;
  try {
    reference = JSON.parse(toolCall.toolSourceReference);
  } catch {
    return;
  }
  if (typeof reference === "object" && reference !== null && disabledReferenceKey in reference) {
    throw new ToolExecutionError(`tool ${toolCall.toolName} is not enabled for this session`);
  }
}

function describeError(error: unknown): string {
  if (error instanceof Error) {
    const cause = (error as { cause?: unknown }).cause;
    return cause instanceof Error ? `${error.message} (${cause.message})` : error.message;
  }
  return String(error);
}
