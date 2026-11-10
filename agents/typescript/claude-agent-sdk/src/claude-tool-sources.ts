// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentToolCall, AgentTurnContext, DeferredToolCall } from "@superdurable/dex-agents";
import type { SourceToolDefinition, ToolSource } from "@superdurable/dex-agents/stepwise";

const toolSourceMethodNames = ["listTools", "deferredToolCall", "executeDeferredToolCall", "pollToolTask"] as const;
// The adapter's envelope around a source's toolSourceReference: the building source's name.
const toolSourceNameKey = "claude_tool_source";
const toolSourceReferenceKey = "reference";

/** One tool that a source listed for this turn segment. */
export class ListedSourceTool {
  public constructor(
    public readonly source: ToolSource,
    public readonly definition: SourceToolDefinition,
  ) {}

  /** Asks the source for the deferred call and wraps its reference in the adapter's envelope. */
  public buildDeferredToolCall(turn: AgentTurnContext, toolCallId: string, toolArguments: string): DeferredToolCall {
    const built = this.source.deferredToolCall(turn, {
      toolCallId,
      toolName: this.definition.name,
      arguments: toolArguments,
      listedTool: this.definition,
    });
    return {
      ...built,
      toolSourceReference: JSON.stringify({
        [toolSourceNameKey]: this.source.sourceName,
        [toolSourceReferenceKey]: built.toolSourceReference ?? null,
      }),
    };
  }
}

/** A source and its own reference, unwrapped from a stored call. */
export interface RoutedSourceCall {
  readonly source: ToolSource;
  readonly reference: string | undefined;
}

/** Lists the runner's tool sources per segment and routes their stored calls back to them. */
export class ToolSourceRouter {
  private readonly sourcesByName: ReadonlyMap<string, ToolSource>;
  private readonly reservedToolNames: ReadonlySet<string>;

  public constructor(
    private readonly toolSources: readonly ToolSource[],
    private readonly listingTimeoutMs: number,
    reservedToolNames: Iterable<string>,
  ) {
    const sourcesByName = new Map<string, ToolSource>();
    for (const source of toolSources) {
      const missing = toolSourceMethodNames.find(
        (methodName) => typeof (source as unknown as Record<string, unknown>)[methodName] !== "function",
      );
      if (missing !== undefined) {
        throw new TypeError(`tool source ${String(source.sourceName)} has no ${missing} method`);
      }
      if (typeof source.sourceName !== "string" || source.sourceName.trim() === "") {
        throw new TypeError("a tool source needs a non-blank sourceName");
      }
      if (sourcesByName.has(source.sourceName)) {
        throw new TypeError(`two tool sources are named ${source.sourceName}`);
      }
      sourcesByName.set(source.sourceName, source);
    }
    if (!(listingTimeoutMs > 0)) {
      throw new RangeError("toolSourceListingTimeoutMs must be positive");
    }
    this.sourcesByName = sourcesByName;
    this.reservedToolNames = new Set(reservedToolNames);
    const declaredToolNames = new Set<string>();
    for (const source of toolSources) {
      const toolNames = [...(source.declaredToolNames?.() ?? [])].sort();
      for (const toolName of toolNames) {
        this.refuseToolName(toolName, declaredToolNames);
      }
      for (const toolName of toolNames) {
        declaredToolNames.add(toolName);
      }
    }
  }

  public get hasToolSources(): boolean {
    return this.toolSources.length > 0;
  }

  // A failing or slow source lists nothing; a colliding name throws a TypeError.
  public async listSourceTools(): Promise<ReadonlyMap<string, ListedSourceTool>> {
    const listed = new Map<string, ListedSourceTool>();
    for (const [index, source] of this.toolSources.entries()) {
      for (const definition of await this.listOneSource(index, source)) {
        this.refuseToolName(definition.name, listed);
        listed.set(definition.name, new ListedSourceTool(source, definition));
      }
    }
    return listed;
  }

  public isSourceCall(toolCall: Pick<AgentToolCall, "toolSourceReference">): boolean {
    return readToolSourceEnvelope(toolCall.toolSourceReference) !== undefined;
  }

  // Throws a RangeError when the source that built the call is not configured on this Worker.
  public route(toolCall: AgentToolCall): RoutedSourceCall | undefined {
    const envelope = readToolSourceEnvelope(toolCall.toolSourceReference);
    if (envelope === undefined) {
      return undefined;
    }
    const source = this.sourcesByName.get(envelope.sourceName);
    if (source === undefined) {
      throw new RangeError(
        `tool source ${envelope.sourceName}, which built the call of tool ${toolCall.toolName}, ` +
          "is not configured on this Worker",
      );
    }
    return { source, reference: envelope.reference };
  }

  private async listOneSource(index: number, source: ToolSource): Promise<readonly SourceToolDefinition[]> {
    let timer: NodeJS.Timeout | undefined;
    const timedOut = new Promise<"timed_out">((resolve) => {
      timer = setTimeout(() => resolve("timed_out"), this.listingTimeoutMs);
    });
    const listing = source.listTools();
    // A listing that outlives this segment keeps running; nobody else reads its error.
    listing.catch(() => undefined);
    try {
      const listed = await Promise.race([listing, timedOut]);
      if (listed === "timed_out") {
        console.warn(`tool source ${index} listed nothing within ${this.listingTimeoutMs} ms`);
        return [];
      }
      return listed;
    } catch (error: unknown) {
      console.warn(`tool source ${index} could not list its tools: ${String(error)}`);
      return [];
    } finally {
      clearTimeout(timer);
    }
  }

  private refuseToolName(toolName: string, otherSourceToolNames: { has(toolName: string): boolean }): void {
    if (this.reservedToolNames.has(toolName) || toolName.startsWith("mcp__")) {
      throw new TypeError(`tool ${toolName} of a tool source uses the name of a custom tool or a Claude Code tool`);
    }
    if (otherSourceToolNames.has(toolName)) {
      throw new TypeError(`two tool sources list a tool named ${toolName}`);
    }
  }
}

/** Returns the record as its source built it: the source's own reference instead of the envelope. */
export function withSourceReference<Call extends AgentToolCall>(toolCall: Call, reference: string | undefined): Call {
  const { toolSourceReference: _envelope, ...rest } = toolCall;
  return (reference === undefined ? rest : { ...rest, toolSourceReference: reference }) as Call;
}

function readToolSourceEnvelope(
  toolSourceReference: string | null | undefined,
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
  const sourceName = (envelope as Record<string, unknown>)[toolSourceNameKey];
  const reference = (envelope as Record<string, unknown>)[toolSourceReferenceKey];
  if (typeof sourceName !== "string" || (reference !== null && reference !== undefined && typeof reference !== "string")) {
    return undefined;
  }
  return { sourceName, reference: reference ?? undefined };
}
