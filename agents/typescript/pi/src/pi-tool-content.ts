// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ToolEffect } from "@superdurable/dex-agents";
import { functionTool, type FunctionTool, type ToolCallContext } from "@superdurable/dex-agents/stepwise";

/**
 * One part of a Pi tool result's content: text, or an image the model cannot see on Dex yet.
 *
 * @since 0.5.0
 */
export type PiToolContentPart =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "image"; readonly data: string; readonly mimeType: string };

/**
 * `AgentEvent.type` of the progress text a Pi tool reports through `onUpdate`. Its payload has `tool_call_id`,
 * `tool_name`, and `text`.
 *
 * @since 0.5.0
 */
export const piToolProgressEventType = "pi.tool_progress";

/** The model-visible text of Pi tool content: text parts joined, each image replaced by a note. */
export function describePiToolContent(content: readonly PiToolContentPart[] | undefined): string {
  return (content ?? [])
    .map((part) => (part.type === "text" ? part.text : `[image omitted: ${part.mimeType}]`))
    .join("\n");
}

/** Writes Pi progress text as a `pi.tool_progress` event; empty text writes nothing. */
export function writePiToolProgress(context: ToolCallContext, toolName: string, text: string): void {
  if (text === "") {
    return;
  }
  context.events.writeEvent(piToolProgressEventType, {
    tool_call_id: context.toolCallId,
    tool_name: toolName,
    text,
  });
}

/** Fields shared by the Pi tool wrappers, checked by `functionTool`. */
export interface PiFunctionToolParts {
  readonly name: string;
  readonly description: string;
  readonly parameters: unknown;
  readonly effect: ToolEffect;
  readonly requiresApproval: boolean | undefined;
  readonly execute: (args: unknown, context: ToolCallContext) => Promise<string>;
}

/** Builds the Dex function tool, so a Pi tool gets every check `functionTool` makes. */
export function buildPiFunctionTool(parts: PiFunctionToolParts): FunctionTool {
  return functionTool<unknown>({
    name: parts.name,
    description: parts.description,
    parameters: parts.parameters as Readonly<Record<string, unknown>>,
    effect: parts.effect,
    ...(parts.requiresApproval === undefined ? {} : { requiresApproval: parts.requiresApproval }),
    execute: parts.execute,
  });
}

/** Rethrows an error caused by Dex cancelling the attempt, so it is not reported to the model as a tool failure. */
export function rethrowIfCancelled(context: ToolCallContext, error: unknown): void {
  if (context.cancellationSignal.aborted) {
    throw error;
  }
}
