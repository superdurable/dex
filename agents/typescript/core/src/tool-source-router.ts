// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ToolExecutionError,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ToolCallStepOptions,
  type ToolExecutionResult,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "./agent-framework-runner.js";
import type { AgentToolCall } from "./agent-session-values.js";
import type { SourceToolDefinition, ToolSource } from "./stepwise/tool-source.js";

/**
 * Default `AgentDefinition.toolSourceListingTimeoutMs`: 10 seconds. It is also the default time one model call of a
 * framework adapter waits for each tool source's listing. Exported from the package root since 0.4.0.
 */
export const defaultToolSourceListingTimeoutMs = 10_000;

const toolSourceMethods = ["listTools", "deferredToolCall", "executeDeferredToolCall", "pollToolTask"] as const;
const toolSourceReferenceKey = "reference";

/**
 * One tool that a {@link ToolSource} listed for a model call, with the source that listed it.
 * @since 0.4.0
 */
export interface ListedSourceTool {
  /** The source that listed the tool. */
  readonly source: ToolSource;
  /** The tool definition as the source listed it. */
  readonly tool: SourceToolDefinition;
}

/**
 * Options of a {@link ToolSourceRouter}.
 * @since 0.4.0
 */
export interface ToolSourceRouterOptions {
  /**
   * JSON key under which the router stores the building source's name in each call's `toolSourceReference`,
   * such as `"vercel_ai_tool_source"`. Keep it fixed: stored calls are routed by it.
   */
  readonly sourceNameKey: string;
  /** Names of the runner's own tools, which no source tool may repeat. Defaults to none. */
  readonly localToolNames?: ReadonlySet<string>;
  /**
   * What the runner's own tools are, for the collision error, such as `"a function tool"`. Defaults to
   * `"a tool of the agent"`.
   */
  readonly localToolDescription?: string;
  /** Names of the built-in tools enabled for every session, which no source tool may repeat. Defaults to none. */
  readonly builtInToolNames?: ReadonlySet<string>;
  /**
   * How long one {@link ToolSourceRouter.listSourceTools} waits for each source, in milliseconds. Defaults to
   * {@link defaultToolSourceListingTimeoutMs}.
   */
  readonly listingTimeoutMs?: number;
}

/**
 * Serves {@link ToolSource}s from a framework runner, so their calls run in tool Steps as the Stepwise runner runs
 * them.
 *
 * A framework adapter that implements {@link AgentFrameworkRunner} builds one router from its `toolSources`. In each
 * `RunModel` it awaits {@link listSourceTools} and offers every listed tool to its framework as a native tool that
 * never runs inside the model call. When the model calls one, the adapter returns {@link buildDeferredToolCall}, so
 * the source picks the effect, the approval, and whether the call is an ordered write, and the router stores the
 * source's name with the source's own `toolSourceReference`. The tool Step then reaches that source through
 * {@link executeDeferredToolCall} on any Worker, with the full execution: the Step context, the heartbeat
 * checkpoint, and the attempt. {@link toolCallStepOptions}, {@link pollToolTask}, and {@link cancelToolTask} route
 * the same way. A runner whose `executeDeferredToolCall` returns only results, and so polls no tool tasks, uses
 * {@link executeDeferredToolCallForResult} instead. `StepwiseAgentRunner` uses a router too.
 *
 * Construction refuses a source without a {@link ToolSource} method or a non-blank, unique `sourceName`, and a
 * name that a source declares through `declaredToolNames` when it repeats a local tool, a built-in tool, or another
 * source's tool. Each listing refuses the same collisions among the listed names.
 *
 * @example
 * ```ts
 * const toolSources = new ToolSourceRouter([new ConnectorToolSource([refund])], {
 *   sourceNameKey: "my_framework_tool_source",
 *   localToolNames: new Set(Object.keys(frameworkTools)),
 * });
 * const listed = await toolSources.listSourceTools();
 * const call = toolSources.buildDeferredToolCall(turn, listed.get("refund")!, modelCall);
 * ```
 * @since 0.4.0
 */
export class ToolSourceRouter {
  private readonly toolSources: readonly ToolSource[];
  private readonly toolSourcesByName: ReadonlyMap<string, ToolSource>;
  private readonly sourceNameKey: string;
  private readonly localToolNames: ReadonlySet<string>;
  private readonly localToolDescription: string;
  private readonly listingTimeoutMs: number;
  /** The last listing of each source, so estimates count its tools without listing again. */
  private readonly lastListings = new Map<number, readonly SourceToolDefinition[]>();

  /**
   * Creates the router and checks the sources.
   * @param toolSources - The sources, in the order the model sees their tools.
   * @param options - The envelope key, the names source tools must not repeat, and the listing timeout.
   * @throws {TypeError} When a source lacks a {@link ToolSource} method or a non-blank `sourceName`, two sources
   * share a `sourceName`, `sourceNameKey` is blank, or a declared tool name repeats a local tool, a built-in tool, or
   * another source's tool.
   * @throws {RangeError} When `listingTimeoutMs` is not positive.
   */
  public constructor(toolSources: readonly ToolSource[], options: ToolSourceRouterOptions) {
    if (typeof options.sourceNameKey !== "string" || options.sourceNameKey.trim() === "") {
      throw new TypeError("ToolSourceRouterOptions.sourceNameKey must be a non-blank string");
    }
    for (const source of toolSources) {
      const missing = toolSourceMethods.find(
        (method) => typeof (source as unknown as Record<string, unknown> | null)?.[method] !== "function",
      );
      if (missing !== undefined) {
        throw new TypeError(`tool source ${String(source)} has no ${missing} method`);
      }
    }
    const toolSourcesByName = new Map<string, ToolSource>();
    for (const source of toolSources) {
      if (typeof source.sourceName !== "string" || source.sourceName.trim() === "") {
        throw new TypeError(`tool source ${String(source)} needs a non-blank sourceName`);
      }
      if (toolSourcesByName.has(source.sourceName)) {
        throw new TypeError(`two tool sources are named ${source.sourceName}`);
      }
      toolSourcesByName.set(source.sourceName, source);
    }
    const listingTimeoutMs = options.listingTimeoutMs ?? defaultToolSourceListingTimeoutMs;
    if (!(listingTimeoutMs > 0)) {
      throw new RangeError("toolSourceListingTimeoutMs must be positive");
    }
    this.toolSources = [...toolSources];
    this.toolSourcesByName = toolSourcesByName;
    this.sourceNameKey = options.sourceNameKey;
    this.localToolNames = new Set(options.localToolNames ?? []);
    this.localToolDescription = options.localToolDescription ?? "a tool of the agent";
    this.listingTimeoutMs = listingTimeoutMs;
    this.refuseDeclaredToolNameCollisions(options.builtInToolNames ?? new Set());
  }

  /** Whether the router has at least one source. */
  public get hasToolSources(): boolean {
    return this.toolSources.length > 0;
  }

  /**
   * Returns the names the sources always list, from their optional `declaredToolNames`.
   * @returns The names.
   */
  public declaredToolNames(): ReadonlySet<string> {
    const names = new Set<string>();
    for (const source of this.toolSources) {
      for (const toolName of source.declaredToolNames?.() ?? []) {
        names.add(toolName);
      }
    }
    return names;
  }

  /**
   * Lists every source's tools for one model call, in source order.
   *
   * A source whose listing throws or takes longer than the listing timeout offers no tools in this call, with a
   * warning, so a down server never fails a turn.
   * @param builtInToolNames - The built-in tools enabled for this turn. Defaults to none.
   * @returns The listed tools by name.
   * @throws {TypeError} When a listed name repeats a local tool, an enabled built-in tool, or another source's tool.
   */
  public async listSourceTools(
    builtInToolNames: ReadonlySet<string> = new Set(),
  ): Promise<ReadonlyMap<string, ListedSourceTool>> {
    const listed = new Map<string, ListedSourceTool>();
    for (const [index, source] of this.toolSources.entries()) {
      for (const tool of await this.listOneSource(index, source)) {
        this.refuseSourceToolName(tool.name, builtInToolNames, listed);
        listed.set(tool.name, { source, tool });
      }
    }
    return listed;
  }

  /**
   * Returns each source's tools without I/O: its `cachedTools` when it has them, else this router's last listing.
   * A name listed twice keeps its first source. Use it to estimate the model input.
   * @returns The cached tools by name.
   */
  public readCachedSourceTools(): ReadonlyMap<string, ListedSourceTool> {
    const cached = new Map<string, ListedSourceTool>();
    for (const [index, source] of this.toolSources.entries()) {
      const tools = source.cachedTools?.() ?? this.lastListings.get(index) ?? [];
      for (const tool of tools) {
        if (!cached.has(tool.name)) {
          cached.set(tool.name, { source, tool });
        }
      }
    }
    return cached;
  }

  /**
   * Builds the deferred call for one model call of a listed source tool.
   *
   * The source's `deferredToolCall` decides the policy, the `ordered` flag, and its own reference. The router
   * keeps all of it and wraps the reference with the source's name.
   * @param turn - The `RunModel` context.
   * @param listedTool - The listed tool the model called, from {@link listSourceTools} of this model call.
   * @param modelCall - The model's tool call ID and its arguments as JSON text.
   * @returns The deferred call to return in `ToolCallsRequested`.
   */
  public buildDeferredToolCall(
    turn: AgentTurnContext,
    listedTool: ListedSourceTool,
    modelCall: { readonly toolCallId: string; readonly arguments: string },
  ): DeferredToolCall {
    const built = listedTool.source.deferredToolCall(turn, {
      toolCallId: modelCall.toolCallId,
      toolName: listedTool.tool.name,
      arguments: modelCall.arguments,
      listedTool: listedTool.tool,
    });
    return {
      ...built,
      toolSourceReference: writeToolSourceEnvelope(
        this.sourceNameKey,
        listedTool.source.sourceName,
        built.toolSourceReference,
      ),
    };
  }

  /**
   * Whether a source built this call, so its tool Step and polls go to a source. It reads only the record, so it
   * answers even when that source is not configured on this Worker; {@link executeDeferredToolCall} then throws.
   * @param toolCall - The call's record.
   * @returns `true` when the record holds this router's envelope, or a reference while the router has sources.
   */
  public isSourceToolCall(toolCall: { readonly toolName: string; readonly toolSourceReference?: string }): boolean {
    if (readToolSourceEnvelope(this.sourceNameKey, toolCall.toolSourceReference) !== undefined) {
      return true;
    }
    return (toolCall.toolSourceReference ?? undefined) !== undefined && this.hasToolSources;
  }

  /**
   * Runs one call in its tool Step through the source that built it, with the source's own reference and every
   * other field of `call` unchanged, such as the Step context, the heartbeat checkpoint, and the attempt.
   * @param call - The deferred execution from the tool Step.
   * @returns The source's outcome.
   * @throws {RangeError} When no source built the call, or its source is not configured on this Worker, so the
   * effect decides the outcome.
   * @throws Any error of the source, such as `ToolExecutionError` or `ToolOutcomeUnknownError`.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const routed = this.requireToolSource(call.toolCall);
    return routed.source.executeDeferredToolCall({
      ...call,
      toolCall: withToolSourceReference(call.toolCall, routed.reference),
    });
  }

  /**
   * Runs one call as {@link executeDeferredToolCall} does, for a runner that polls no tool tasks and asks no tool
   * questions, so its `executeDeferredToolCall` returns only a `ToolExecutionResult`. Such a runner has no
   * `pollToolTask`, so the session tells the source that it cannot start tasks. `ConnectorToolSource` returns only
   * results.
   * @param call - The deferred execution from the tool Step.
   * @returns The source's result.
   * @throws {ToolExecutionError} When the source asked the user a question. A question comes before any effect, so
   * the call records `failed` and the model reads why.
   * @throws {Error} When the source started a tool task anyway. The task may run, so the effect decides the outcome.
   * @throws Any error of {@link executeDeferredToolCall}.
   */
  public async executeDeferredToolCallForResult(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const outcome = await this.executeDeferredToolCall(call);
    if (!("kind" in outcome)) {
      return outcome;
    }
    const toolName = call.toolCall.toolName;
    if (outcome.kind === "tool_input_requested") {
      throw new ToolExecutionError(`tool ${toolName} asked a question, which this agent cannot ask the user`);
    }
    throw new Error(`tool ${toolName} started a tool task, which this agent cannot poll`);
  }

  /**
   * Polls a source's tool task once, through the source that built the call.
   * @param poll - The call's record from `PollToolTask`.
   * @returns The source's outcome.
   * @throws {RangeError} When no source built the call. The task may still run, so `PollToolTask` retries and then
   * records the call by its effect.
   * @throws Any error of the source.
   */
  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    const routed = this.requireToolSource(poll.toolCall);
    return routed.source.pollToolTask({ ...poll, toolCall: withToolSourceReference(poll.toolCall, routed.reference) });
  }

  /**
   * Cancels a source's task through the source that built the call; a source without `cancelToolTask` leaves its
   * task running.
   * @param cancellation - The abandoned call's record from `CancelToolTask`.
   * @returns A Promise that resolves once the source took the cancellation.
   * @throws {RangeError} When no source built the call.
   * @throws Any error of the source.
   */
  public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    const routed = this.requireToolSource(cancellation.toolCall);
    await routed.source.cancelToolTask?.({
      ...cancellation,
      toolCall: withToolSourceReference(cancellation.toolCall, routed.reference),
    });
  }

  /**
   * Returns the source's options for one call's tool Step.
   * @param toolCall - The record whose tool Step starts.
   * @returns The source's options, or `undefined` when no source built the call or its source defines none.
   * @throws {RangeError} When a source built the call but it is not configured on this Worker; the session then
   * uses the defaults.
   */
  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    const routed = this.routeToToolSource(toolCall);
    return routed?.source.toolCallStepOptions?.(withToolSourceReference(toolCall, routed.reference));
  }

  private refuseDeclaredToolNameCollisions(builtInToolNames: ReadonlySet<string>): void {
    const declared = new Map<string, ToolSource>();
    for (const source of this.toolSources) {
      const names = [...(source.declaredToolNames?.() ?? [])].sort();
      for (const toolName of names) {
        this.refuseSourceToolName(toolName, builtInToolNames, declared);
      }
      for (const toolName of names) {
        declared.set(toolName, source);
      }
    }
  }

  private refuseSourceToolName(
    toolName: string,
    builtInToolNames: ReadonlySet<string>,
    otherSourceTools: ReadonlyMap<string, unknown>,
  ): void {
    if (builtInToolNames.has(toolName)) {
      throw new TypeError(`tool ${toolName} uses the name of an enabled built-in tool`);
    }
    if (this.localToolNames.has(toolName)) {
      throw new TypeError(`tool ${toolName} of a tool source uses the name of ${this.localToolDescription}`);
    }
    if (otherSourceTools.has(toolName)) {
      throw new TypeError(`two tool sources list a tool named ${toolName}`);
    }
  }

  private async listOneSource(index: number, source: ToolSource): Promise<readonly SourceToolDefinition[]> {
    const listing = source.listTools();
    // A listing that outlives this call keeps running; nobody else reads its error.
    listing.catch(() => undefined);
    let timer: NodeJS.Timeout | undefined;
    const timedOut = new Promise<"timed_out">((resolve) => {
      timer = setTimeout(() => resolve("timed_out"), this.listingTimeoutMs);
    });
    try {
      const tools = await Promise.race([listing, timedOut]);
      if (tools === "timed_out") {
        console.warn(`tool source ${index} listed nothing within ${this.listingTimeoutMs} ms`);
        return [];
      }
      this.lastListings.set(index, tools);
      return tools;
    } catch (error: unknown) {
      console.warn(`tool source ${index} could not list its tools: ${error instanceof Error ? error.message : String(error)}`);
      return [];
    } finally {
      clearTimeout(timer);
    }
  }

  private requireToolSource(toolCall: ToolSourceRoutedCall): RoutedToolSource {
    const routed = this.routeToToolSource(toolCall);
    if (routed === undefined) {
      throw new RangeError(`no tool source built the call of tool ${toolCall.toolName}`);
    }
    return routed;
  }

  private routeToToolSource(toolCall: ToolSourceRoutedCall): RoutedToolSource | undefined {
    const envelope = readToolSourceEnvelope(this.sourceNameKey, toolCall.toolSourceReference);
    if (envelope !== undefined) {
      const source = this.toolSourcesByName.get(envelope.sourceName);
      if (source === undefined) {
        throw new RangeError(
          `tool source ${envelope.sourceName}, which built the call of tool ${toolCall.toolName}, is not configured on this Worker`,
        );
      }
      return { source, reference: envelope.reference };
    }
    const reference = toolCall.toolSourceReference ?? undefined;
    if (reference === undefined || this.toolSources.length === 0) {
      return undefined;
    }
    const onlySource = this.toolSources.length === 1 ? this.toolSources[0] : undefined;
    if (onlySource !== undefined) {
      // A reference the source built itself, such as in a runner subclass.
      return { source: onlySource, reference };
    }
    throw new RangeError(`cannot tell which tool source built the call of ${toolCall.toolName}`);
  }
}

interface ToolSourceRoutedCall {
  readonly toolName: string;
  readonly toolSourceReference?: string;
}

interface RoutedToolSource {
  readonly source: ToolSource;
  readonly reference: string | undefined;
}

/**
 * The router's `toolSourceReference`: the source's name under `sourceNameKey` and its own reference, as JSON text.
 * @param sourceNameKey - The envelope key of the runner.
 * @param sourceName - The building source's name.
 * @param reference - The source's own reference, or `undefined`.
 * @returns The envelope text.
 */
export function writeToolSourceEnvelope(sourceNameKey: string, sourceName: string, reference: string | undefined): string {
  return JSON.stringify({ [sourceNameKey]: sourceName, [toolSourceReferenceKey]: reference ?? null });
}

/**
 * Reads an envelope that {@link writeToolSourceEnvelope} wrote with the same key.
 * @param sourceNameKey - The envelope key of the runner.
 * @param toolSourceReference - A record's `toolSourceReference`.
 * @returns The source's name and its own reference, or `undefined` when the text is no such envelope.
 */
export function readToolSourceEnvelope(
  sourceNameKey: string,
  toolSourceReference: string | undefined,
): { readonly sourceName: string; readonly reference: string | undefined } | undefined {
  if (toolSourceReference === undefined || toolSourceReference === null) {
    return undefined;
  }
  let envelope: unknown;
  try {
    envelope = JSON.parse(toolSourceReference);
  } catch {
    return undefined;
  }
  if (typeof envelope !== "object" || envelope === null || Array.isArray(envelope)) {
    return undefined;
  }
  const sourceName = (envelope as Record<string, unknown>)[sourceNameKey];
  const reference = (envelope as Record<string, unknown>)[toolSourceReferenceKey];
  if (typeof sourceName !== "string") {
    return undefined;
  }
  if (reference !== null && reference !== undefined && typeof reference !== "string") {
    return undefined;
  }
  return { sourceName, reference: reference ?? undefined };
}

/** The record as the source built it: its own reference instead of the router's envelope. */
function withToolSourceReference<Call extends ToolSourceRoutedCall>(toolCall: Call, reference: string | undefined): Call {
  const { toolSourceReference: _envelope, ...rest } = toolCall;
  return (reference === undefined ? rest : { ...rest, toolSourceReference: reference }) as Call;
}
