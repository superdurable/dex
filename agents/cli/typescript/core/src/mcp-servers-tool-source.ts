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
  type ToolEffect,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type MediaStore,
  type SourceToolDefinition,
  type ToolSource,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import {
  McpToolCallInterruptedError,
  McpToolListingError,
  McpToolSource,
  type McpStreamableHttpServerConfig,
  type McpTool,
} from "@superdurable/dex-agents/mcp";

import { compareCodePoints, type EffectiveConfig } from "./cli-config.js";
import { dexSecretVariableNames } from "./cli-environment.js";
import type { TurnConfigResolver } from "./turn-config-resolver.js";

/**
 * The `sourceName` of {@link McpServersToolSource}, which stored calls of `mcp_servers` tools are routed by.
 *
 * @since 0.6.4
 */
export const mcpServersToolSourceName = "mcp_servers";

const variableReferencePattern = /\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g;
const printableAsciiPattern = /^[ -~]*$/;
const refusedReferenceKey = "refused_tool";
// Shorter values are no credible secret, and replacing them would garble ordinary text.
const minimumRedactedLength = 8;
const redactedText = "[redacted]";
const secretSeparatorPattern = /[\s=:,;]+/;
const singleAttemptEffects: ReadonlySet<ToolEffect> = new Set(["non_idempotent_write", "unclassified"]);

/**
 * An `mcp_servers` header names an environment variable that cannot be used: it is not set, is empty, or holds a
 * character outside printable ASCII.
 *
 * The message names the setting and the variable, never the variable's value, such as
 * `mcp_servers.studio.headers.Authorization needs the environment variable STUDIO_API_KEY, which is not set or is
 * empty.` A command stops with exit code 2 and prints the message.
 *
 * @since 0.6.4
 */
export class McpServerSettingsError extends Error {
  /**
   * Creates the error.
   * @param setting - The header to fix, such as `mcp_servers.studio.headers.Authorization`.
   * @param message - The exact message, without any variable value.
   */
  public constructor(
    public readonly setting: string,
    message: string,
  ) {
    super(message);
    this.name = "McpServerSettingsError";
  }
}

/**
 * Returns the remote MCP servers of a configuration with every `${NAME}` header reference replaced by the value of
 * the environment variable `NAME`.
 *
 * The servers come in Unicode code point order of their names. A value is used exactly as set: never trimmed and
 * never expanded again. Any `$` that does not start a reference stays as written. The Dex credential variables in
 * `dexSecretVariableNames` count as not set, so a Dex credential never reaches another server. Only the Worker calls it, right
 * before it connects; the effective configuration keeps the text as written. Never print, log, or store the
 * returned headers.
 *
 * @example
 * ```ts
 * const servers = resolveMcpServers(effective, process.env);
 * const source = new McpServersToolSource(servers, turns, (line) => console.error(line));
 * ```
 * @param config - The effective configuration.
 * @param environment - The environment variables, such as `process.env`.
 * @returns One Streamable HTTP server configuration per `mcp_servers` entry, named by its key.
 * @throws {McpServerSettingsError} For the first unusable variable: servers in name order, a server's headers in
 * name order, and a value's references from left to right.
 * @since 0.6.4
 */
export function resolveMcpServers(
  config: EffectiveConfig,
  environment: Readonly<Record<string, string | undefined>>,
): McpStreamableHttpServerConfig[] {
  const servers = config.mcp_servers ?? {};
  return Object.keys(servers)
    .sort(compareCodePoints)
    .map((name) => {
      const server = servers[name]!;
      // fromEntries defines own properties, so a header named __proto__ stays a header.
      const headers: Record<string, string> = Object.fromEntries(
        Object.keys(server.headers)
          .sort(compareCodePoints)
          .map((header) => [
            header,
            expandHeaderValue(`mcp_servers.${name}.headers.${header}`, server.headers[header]!, environment),
          ]),
      );
      return {
        transport: "streamable_http",
        name,
        url: server.url,
        headers,
        trustsToolAnnotations: server.trust_tool_annotations,
      };
    });
}

function expandHeaderValue(
  setting: string,
  value: string,
  environment: Readonly<Record<string, string | undefined>>,
): string {
  return value.replace(variableReferencePattern, (_reference, variable: string) => {
    const variableValue = readHeaderVariable(environment, variable);
    if (variableValue === "") {
      throw new McpServerSettingsError(
        setting,
        `${setting} needs the environment variable ${variable}, which is not set or is empty.`,
      );
    }
    if (!printableAsciiPattern.test(variableValue)) {
      throw new McpServerSettingsError(
        setting,
        `${setting} needs the environment variable ${variable}, whose value is not printable ASCII.`,
      );
    }
    return variableValue;
  });
}

/** Returns a variable's own string value, so built-in object properties such as `toString` never count as set. */
function readHeaderVariable(environment: Readonly<Record<string, string | undefined>>, variable: string): string {
  if (dexSecretVariableNames.includes(variable) || !Object.hasOwn(environment, variable)) {
    return "";
  }
  const value = environment[variable];
  return typeof value === "string" ? value : "";
}

/**
 * Returns the value of every environment variable that an `mcp_servers` header references, for
 * {@link McpServersToolSourceOptions.redactedValues}. Call it after {@link resolveMcpServers} succeeded.
 *
 * @param config - The effective configuration.
 * @param environment - The environment the references read.
 * @returns The values, possibly with repeats.
 * @since 0.6.4
 */
export function mcpServerHeaderVariableValues(
  config: EffectiveConfig,
  environment: Readonly<Record<string, string | undefined>>,
): string[] {
  return Object.values(config.mcp_servers ?? {}).flatMap((server) =>
    Object.values(server.headers).flatMap((value) =>
      [...value.matchAll(variableReferencePattern)].map((reference) => readHeaderVariable(environment, reference[1]!)),
    ),
  );
}

/**
 * Options of {@link McpServersToolSource}.
 *
 * @since 0.6.4
 */
export interface McpServersToolSourceOptions {
  /**
   * The store for the images that tool results carry. With a store, each image of a successful result is uploaded in
   * the tool Step and reaches the model as an image reference. Omitted, images stay JSON in the output text.
   */
  readonly mediaStore?: MediaStore;
  /**
   * Writes one line to the command's log file: each failed listing and each warning of the wrapped `McpToolSource`.
   * Omitted, they go to `console.warn`.
   */
  readonly writeLogLine?: (line: string) => void;
  /** A sentence that ends each terminal line about unavailable servers, such as where the log file is. */
  readonly unavailableHint?: string;
  /**
   * More secret values to replace with `[redacted]`, such as {@link mcpServerHeaderVariableValues}. The source always
   * replaces each header value and each of its parts between spaces, `=`, `:`, `,`, or `;`. Values shorter than 8
   * characters stay.
   */
  readonly redactedValues?: readonly string[];
}

/**
 * The tools of the remote MCP servers in `mcp_servers`, as a {@link ToolSource} that applies each turn's
 * `approval.always`.
 *
 * It wraps `McpToolSource`, so each call runs in its own tool Step with the session's idempotency key, and the agent
 * sees each tool as `<server name>__<tool name>`. A server whose configuration does not trust its annotations offers
 * tools whose calls take the session's default effect, so they wait for approval. A server that cannot be listed
 * offers no tools in that model call; the source prints one line when the servers stop answering and when they answer
 * again. The CLI core registers the source even without servers, so it refuses a stored call that another Worker
 * requested, without running it.
 *
 * Its header values may hold secrets: it never prints, logs, or stores them. A server may echo them, such as in
 * an HTTP 401 body or a tool error, so the source replaces each one with `[redacted]` in every line it logs, every
 * error a call throws, and every tool output. The Worker that runs a stored call checks it against its own
 * configuration too: for a server whose annotations it does not trust, the call must have had approval and a
 * single-attempt effect.
 *
 * @example
 * ```ts
 * const source = new McpServersToolSource(resolveMcpServers(effective, process.env), turns, (line) =>
 *   console.error(line),
 * );
 * const tools = await source.listTools();
 * ```
 * @since 0.6.4
 */
export class McpServersToolSource implements ToolSource {
  /** {@link mcpServersToolSourceName}, stored with each call. */
  public readonly sourceName = mcpServersToolSourceName;
  private readonly mcp: McpToolSource;
  private readonly logFileWriter: ((line: string) => void) | undefined;
  private readonly unavailableHint: string | undefined;
  private isUnavailable = false;
  private warningCount = 0;
  private readonly secretValues: readonly string[];
  private readonly serversByName: ReadonlyMap<string, McpStreamableHttpServerConfig>;

  /**
   * Creates the source without connecting to the servers.
   * @param servers - The servers, as {@link resolveMcpServers} returns them; none lists no tools.
   * @param turns - The per-turn configuration.
   * @param log - Writes a terminal line when the servers stop answering or answer again.
   * @param options - The media store, the log file, and the hint for unavailable servers.
   */
  public constructor(
    servers: readonly McpStreamableHttpServerConfig[],
    private readonly turns: TurnConfigResolver,
    private readonly log: (line: string) => void,
    options: McpServersToolSourceOptions = {},
  ) {
    this.logFileWriter = options.writeLogLine;
    this.unavailableHint = options.unavailableHint;
    this.secretValues = secretValuesOf(servers, options.redactedValues ?? []);
    this.serversByName = new Map(servers.map((server) => [server.name, server]));
    this.mcp = new McpToolSource(servers, {
      sourceName: mcpServersToolSourceName,
      resourceAndPromptToolsEnabled: false,
      clientName: "dex-agents-cli",
      logWarning: (message) => this.writeWarning(message),
      ...(options.mediaStore === undefined ? {} : { mediaStore: options.mediaStore }),
    });
  }

  /**
   * Lists the tools of every server that answers.
   * @returns The tools, named `<server name>__<tool name>`; none while no server answers.
   */
  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    let tools: readonly McpTool[];
    try {
      tools = await this.mcp.listTools();
    } catch (error: unknown) {
      if (!(error instanceof McpToolListingError)) {
        throw error;
      }
      this.writeWarning(`no MCP server of mcp_servers could be listed: ${error.message}`);
      if (!this.isUnavailable) {
        this.isUnavailable = true;
        this.log(this.withHint("MCP server tools are unavailable for now; each turn tries again."));
      }
      return [];
    }
    if (this.isUnavailable) {
      this.isUnavailable = false;
      this.log("MCP server tools are available again.");
    }
    return tools;
  }

  /**
   * Lists the servers once when the command starts, so {@link cachedTools} has their tools before the harness builds
   * its Flow, and prints one line when a server could not be listed.
   * @returns A Promise that resolves after the listing.
   */
  public async listAtStartup(): Promise<void> {
    const warningsBefore = this.warningCount;
    await this.listTools();
    if (!this.isUnavailable && this.warningCount > warningsBefore) {
      this.log(this.withHint("Some MCP server tools are unavailable for now."));
    }
  }

  /**
   * Returns the last listing without I/O.
   * @returns The tools.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.mcp.cachedTools();
  }

  /**
   * Builds the deferred call with the turn's `approval.always`. A tool this source did not list becomes a call that
   * its tool Step refuses.
   * @param turn - The `RunModel` context.
   * @param call - The model's call.
   * @param approvalToolName - The name `approval.always` matches; defaults to `call.toolName`.
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
    approvalToolName: string = call.toolName,
  ): DeferredToolCall {
    const mcpTool = this.mcp.cachedTools().find((tool) => tool.name === call.toolName);
    if (mcpTool === undefined) {
      return {
        toolCallId: call.toolCallId,
        toolName: call.toolName,
        arguments: call.arguments,
        policy: { effect: "read", requiresApproval: false },
        toolSourceReference: JSON.stringify({ [refusedReferenceKey]: call.toolName }),
      };
    }
    const inner = this.mcp.deferredToolCall(turn, { ...call, listedTool: mcpTool });
    const isAlwaysApproved = this.turns.turnOf(turn).approval.always.includes(approvalToolName);
    return { ...inner, policy: { ...inner.policy, requiresApproval: inner.policy.requiresApproval || isAlwaysApproved } };
  }

  /**
   * Runs one call in its tool Step, after checking it again against this Worker's `approval.always`.
   * @param call - The approved call.
   * @param approvalToolName - The name `approval.always` matches; defaults to the call's tool name.
   * @returns The tool's outcome.
   * @throws {ToolExecutionError} When the call was refused when it was built, now needs approval it did not have,
   * lacks the approval or single attempt that this Worker requires for a server whose annotations it does not
   * trust, has a missing or malformed reference or names a server this Worker does not configure, or failed. Every
   * error has its secrets replaced.
   */
  public async executeDeferredToolCall(
    call: DeferredToolExecution,
    approvalToolName: string = call.toolCall.toolName,
  ): Promise<DeferredToolOutcome> {
    const { toolName } = call.toolCall;
    if (isRefusedCall(call.toolCall)) {
      throw new ToolExecutionError(`tool ${toolName} is not offered by any MCP server of mcp_servers; it did not run`);
    }
    this.requireLocalAnnotationPolicy(call.toolCall);
    if (!call.toolCall.requiresApproval) {
      let alwaysApproved: readonly string[];
      try {
        alwaysApproved = (await this.turns.resolveToolStep({ flowId: call.flowId, turnId: call.toolCall.turnId })).approval
          .always;
      } catch (error: unknown) {
        throw new ToolExecutionError(
          `tool ${toolName} did not run: the session's settings are rejected here: ${describeError(error)}`,
          { cause: error },
        );
      }
      if (alwaysApproved.includes(approvalToolName)) {
        throw new ToolExecutionError(`tool ${approvalToolName} now needs approval, and this call had none; it did not run`);
      }
    }
    try {
      return this.redactOutcome(await this.mcp.executeDeferredToolCall(call));
    } catch (error: unknown) {
      throw this.redactError(error);
    }
  }

  /**
   * Refuses a call that names no server this Worker configures, or that another Worker built under a broader policy
   * than this Worker's configuration allows.
   */
  private requireLocalAnnotationPolicy(toolCall: AgentToolCall): void {
    const server = this.requireReferencedServer(toolCall);
    if (server.trustsToolAnnotations !== false) {
      return;
    }
    if (!toolCall.requiresApproval || !singleAttemptEffects.has(toolCall.effect)) {
      throw new ToolExecutionError(
        `tool ${toolCall.toolName} did not run: this Worker does not trust the annotations of MCP server ` +
          `${server.name}, so its calls need approval and run at most once`,
      );
    }
  }

  /**
   * Returns the configured server of a complete stored reference. McpToolSource resolves an incomplete one from its
   * listing by tool name, which could reach another server than the one checked.
   */
  private requireReferencedServer(toolCall: {
    readonly toolName: string;
    readonly toolSourceReference?: string | null | undefined;
  }): McpStreamableHttpServerConfig {
    const reference = readCompleteMcpReference(toolCall.toolSourceReference);
    const server = reference === undefined ? undefined : this.serversByName.get(reference.serverName);
    if (server === undefined) {
      throw new ToolExecutionError(
        `tool ${toolCall.toolName} did not run: its stored call names no MCP server that this Worker configures`,
      );
    }
    return server;
  }

  /**
   * Polls a task that {@link executeDeferredToolCall} started.
   * @param poll - The call's record.
   * @returns The poll outcome.
   * @throws {ToolExecutionError} When the call's stored reference names no server this Worker configures, or the
   * poll failed. Every error has its secrets replaced.
   */
  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    this.requireReferencedServer(poll.toolCall);
    try {
      return this.redactOutcome(await this.mcp.pollToolTask(poll));
    } catch (error: unknown) {
      throw this.redactError(error);
    }
  }

  /**
   * Cancels an abandoned task. A call whose stored reference names no server this Worker configures sends nothing.
   * @param cancellation - The call's record.
   * @returns A Promise that resolves once the server took the cancellation.
   */
  public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    const reference = readCompleteMcpReference(cancellation.toolCall.toolSourceReference);
    if (reference === undefined || !this.serversByName.has(reference.serverName)) {
      return;
    }
    try {
      await this.mcp.cancelToolTask(cancellation);
    } catch (error: unknown) {
      throw this.redactError(error);
    }
  }

  /** Replaces every secret value in a text with `[redacted]`. */
  private redact(text: string): string {
    let redacted = text;
    for (const secret of this.secretValues) {
      redacted = redacted.split(secret).join(redactedText);
    }
    return redacted;
  }

  /** Replaces the secrets of an outcome's output text. */
  private redactOutcome<T>(outcome: T): T {
    const output = (outcome as { readonly output?: unknown }).output;
    return typeof output === "string" ? { ...outcome, output: this.redact(output) } : outcome;
  }

  /** Replaces the secrets in an error and its causes in place, so their types and fields stay. */
  private redactError(error: unknown): unknown {
    const seen = new Set<unknown>();
    let current: unknown = error;
    while (current instanceof Error && !seen.has(current)) {
      seen.add(current);
      const fields = current as unknown as Record<string, unknown>;
      current.message = this.redact(current.message);
      if (current.stack !== undefined) {
        current.stack = this.redact(current.stack);
      }
      if (current instanceof McpToolCallInterruptedError || current instanceof McpToolListingError) {
        fields["detail"] = this.redact(current.detail);
      }
      current = current.cause;
    }
    return error;
  }

  private withHint(line: string): string {
    return this.unavailableHint === undefined ? line : `${line} ${this.unavailableHint}`;
  }

  /** Writes to the log file; a failing writer sends the line to `console.warn` instead. */
  private writeWarning(rawLine: string): void {
    this.warningCount += 1;
    const line = this.redact(rawLine);
    if (this.logFileWriter === undefined) {
      console.warn(line);
      return;
    }
    try {
      this.logFileWriter(line);
    } catch (error: unknown) {
      console.warn(`${line} (the log line could not be written: ${describeError(error)})`);
    }
  }
}

/** Returns each header value and its parts, at least 8 characters long, longest first. */
function secretValuesOf(servers: readonly McpStreamableHttpServerConfig[], extraValues: readonly string[]): string[] {
  const values = new Set<string>(extraValues);
  for (const server of servers) {
    for (const value of Object.values(server.headers ?? {})) {
      values.add(value);
      for (const part of value.split(secretSeparatorPattern)) {
        values.add(part);
      }
    }
  }
  return [...values]
    .filter((value) => value.length >= minimumRedactedLength)
    .sort((left, right) => right.length - left.length);
}

/**
 * Returns `McpToolSource`'s stored reference, `{"mcp_server": ..., "mcp_tool": ...}`, when both are non-empty
 * strings, as `McpToolSource` routes it.
 */
function readCompleteMcpReference(
  toolSourceReference: string | undefined | null,
): { readonly serverName: string; readonly mcpToolName: string } | undefined {
  if (toolSourceReference === undefined || toolSourceReference === null) {
    return undefined;
  }
  let reference: unknown;
  try {
    reference = JSON.parse(toolSourceReference);
  } catch {
    return undefined;
  }
  if (typeof reference !== "object" || reference === null || Array.isArray(reference)) {
    return undefined;
  }
  const { mcp_server: serverName, mcp_tool: mcpToolName } = reference as Record<string, unknown>;
  if (typeof serverName !== "string" || serverName === "" || typeof mcpToolName !== "string" || mcpToolName === "") {
    return undefined;
  }
  return { serverName, mcpToolName };
}

function isRefusedCall(toolCall: AgentToolCall): boolean {
  const text = toolCall.toolSourceReference;
  if (text === undefined || text === null) {
    return false;
  }
  try {
    const reference: unknown = JSON.parse(text);
    return typeof reference === "object" && reference !== null && refusedReferenceKey in reference;
  } catch {
    return false;
  }
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
