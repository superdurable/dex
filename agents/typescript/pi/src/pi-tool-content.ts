// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { validateToolArguments, type JsonObject } from "@earendil-works/pi-ai";
import {
  ImageValidationError,
  ToolExecutionError,
  type AgentImageReference,
  type ToolEffect,
} from "@superdurable/dex-agents";
import {
  functionTool,
  functionToolResult,
  type FunctionTool,
  type FunctionToolResult,
  type PreparedToolArguments,
  type ToolCallContext,
} from "@superdurable/dex-agents/builtin-loop";

/**
 * One part of a Pi tool result's content: text, or an image.
 *
 * With a media store on the session Flow, an image part reaches the model after the result's text. Without one, or
 * when the store refuses the image's type, it becomes the note `[image omitted: <mimeType>]`.
 *
 * @since 0.5.0
 */
export type PiToolContentPart =
  | {
      /** Discriminator: a text part. */
      readonly type: "text";
      /** Text the model sees. */
      readonly text: string;
    }
  | {
      /** Discriminator: an image part. */
      readonly type: "image";
      /** Image bytes, base64-encoded. Dex uploads them to the media store and keeps only the reference. */
      readonly data: string;
      /** MIME type of the image, such as `image/png`. */
      readonly mimeType: string;
    };

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

/** Text parts plus images uploaded to the call's store; a refused image, or no store, keeps its note. */
export async function toPiFunctionToolResult(
  context: ToolCallContext,
  content: readonly PiToolContentPart[] | undefined,
): Promise<string | FunctionToolResult> {
  const mediaStore = context.mediaStore;
  const parts = content ?? [];
  if (mediaStore === undefined || !parts.some((part) => part.type === "image")) {
    return describePiToolContent(parts);
  }
  const texts: string[] = [];
  const images: AgentImageReference[] = [];
  for (const part of parts) {
    if (part.type === "text") {
      texts.push(part.text);
      continue;
    }
    try {
      images.push(await mediaStore.putImage(new Uint8Array(Buffer.from(part.data, "base64"))));
    } catch (error) {
      if (!(error instanceof ImageValidationError)) {
        throw error;
      }
      texts.push(`[image omitted: ${part.mimeType}]`);
    }
  }
  return functionToolResult({ output: texts.join("\n"), images });
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
  /** The Pi tool's argument repair, which the runner calls before the schema check. */
  readonly prepareArguments: ((args: unknown) => unknown) | undefined;
  readonly execute: (args: unknown, context: ToolCallContext) => Promise<string | FunctionToolResult>;
}

/** Builds the Dex function tool, so a Pi tool gets every check `functionTool` makes. */
export function buildPiFunctionTool(parts: PiFunctionToolParts): FunctionTool {
  const prepareArguments = parts.prepareArguments;
  return functionTool<unknown>({
    name: parts.name,
    description: parts.description,
    parameters: parts.parameters as Readonly<Record<string, unknown>>,
    effect: parts.effect,
    ...(parts.requiresApproval === undefined ? {} : { requiresApproval: parts.requiresApproval }),
    // The runner refuses a thenable result at run time, so the cast keeps the core contract.
    ...(prepareArguments === undefined
      ? {}
      : { prepareArguments: (args: unknown) => prepareArguments(args) as PreparedToolArguments }),
    execute: parts.execute,
  });
}

/** Validates converted arguments with pi-ai, as Pi's agent loop does; pi-ai's validation coerces them. */
export function validatePiToolArguments(
  tool: { readonly name: string; readonly description: string; readonly parameters: object },
  args: unknown,
  toolCallId: string,
): unknown {
  return validateToolArguments(
    { name: tool.name, description: tool.description, parameters: tool.parameters },
    { type: "toolCall", id: toolCallId, name: tool.name, arguments: args as JsonObject },
  );
}

/** Converts a failure before the tool ran, such as invalid arguments, into a failure the model sees. */
export function throwPiToolArgumentsFailure(context: ToolCallContext, error: unknown): never {
  if (context.cancellationSignal.aborted || error instanceof ToolExecutionError) {
    throw error;
  }
  throw new ToolExecutionError(error instanceof Error ? error.message : String(error), { cause: error });
}

/** A plain error propagates unless the call's effect after policies is `read` or `idempotent_write`. */
export function throwPiToolExecutionFailure(context: ToolCallContext, error: unknown): never {
  // An absent effect is unknown, so the call may have written.
  const isRetrySafe = context.effect === "read" || context.effect === "idempotent_write";
  if (!isRetrySafe && !(error instanceof ToolExecutionError)) {
    throw error;
  }
  throwPiToolArgumentsFailure(context, error);
}
