// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Name of a built-in tool that the session itself handles.
 *
 * - `ask_user`: asks the user a question. The session raises a `question` interrupt, waits in
 *   `AwaitQuestionAnswer`, and the answer becomes the tool result, so the model continues the same turn.
 * - `durable_wait`: suspends the turn on a Dex Timer in `AwaitTimer`. No model call, coroutine, or
 *   Worker holds the wait.
 *
 * Enable built-in tools with {@link AgentSessionOptions.builtInTools}. A runner offers the enabled ones
 * to its model, from {@link builtInToolDefinitions}, like any other tool, and returns their calls in
 * {@link ToolCallsRequested} like any other call. The session routes a call whose name is an enabled
 * built-in tool to that tool's handling instead of a tool Step. An enabled built-in name is reserved.
 * A disabled built-in name is an ordinary tool name.
 */
export type BuiltInTool = "ask_user" | "durable_wait";

/** Model-visible definition of one built-in tool, from {@link builtInToolDefinitions}. */
export interface BuiltInToolDefinition {
  /** Tool name as the model calls it. */
  readonly name: BuiltInTool;
  /** Tool description for the model; the same text as the Python core. */
  readonly description: string;
  /** JSON Schema of the tool arguments; the same schema as the Python core. */
  readonly parameters: Readonly<Record<string, unknown>>;
}

/**
 * Definitions of every built-in tool, with the same names, descriptions, and JSON Schemas as the Python
 * core's `BUILT_IN_TOOL_DEFINITIONS`.
 *
 * A runner offers the definitions of {@link AgentTurnContext.enabledBuiltInTools} to its model. Adapters
 * register them as tools without an implementation, which every adapter already does for deferred
 * application tools.
 *
 * @example
 * ```ts
 * const offeredBuiltInTools = builtInToolDefinitions.filter((definition) =>
 *   turn.enabledBuiltInTools?.has(definition.name) === true);
 * ```
 */
export const builtInToolDefinitions: readonly BuiltInToolDefinition[] = Object.freeze([
  Object.freeze({
    name: "ask_user",
    description:
      "Ask the user one question and wait for the answer, which becomes this tool's result. Use it only " +
      "when you cannot continue without information or a decision from the user. The wait can last hours " +
      "or days.",
    parameters: Object.freeze({
      type: "object",
      properties: {
        question: { type: "string", description: "One concise question for the user." },
        choices: {
          type: "array",
          items: { type: "string" },
          description: "Answers the user can pick. Omit for a free-text answer.",
        },
        allows_free_text: {
          type: "boolean",
          description: "Whether the user may answer with text that is not one of the choices. Defaults to true.",
        },
      },
      required: ["question"],
      additionalProperties: false,
    }),
  }),
  Object.freeze({
    name: "durable_wait",
    description:
      "Pause this turn for a number of whole seconds, then continue. Nothing runs during the wait. Use it " +
      "to wait for something outside the conversation, such as a job that needs time to finish.",
    parameters: Object.freeze({
      type: "object",
      properties: {
        duration_seconds: { type: "integer", minimum: 1, description: "Whole seconds to wait." },
        reason: { type: "string", description: "Why the turn waits, shown to the user." },
      },
      required: ["duration_seconds", "reason"],
      additionalProperties: false,
    }),
  }),
]);

/** Every built-in tool name, in definition order. */
export const builtInToolNames: readonly BuiltInTool[] = Object.freeze(
  builtInToolDefinitions.map((definition) => definition.name),
);

/** Parsed arguments of an `ask_user` call. */
export interface AskUserArguments {
  readonly question: string;
  readonly choices: readonly string[];
  readonly allowsFreeText: boolean;
}

/** Parsed arguments of a `durable_wait` call. */
export interface DurableWaitArguments {
  readonly durationSeconds: number;
  readonly reason: string;
}

/** Result of validating a built-in call's arguments: the parsed arguments or the model-visible error. */
export type BuiltInArgumentsValidation<Arguments> =
  | { readonly isValid: true; readonly arguments: Arguments }
  | { readonly isValid: false; readonly error: string };

// The characters Python's str.isspace() accepts, so blank checks match the Python core exactly.
const pythonWhitespaceOnly = /^[\t\n\v\f\r\x1c-\x1f \x85\xa0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

/** Reports whether text is empty or only whitespace, as Python's `not text.strip()` does. */
export function isBlankText(text: string): boolean {
  return pythonWhitespaceOnly.test(text);
}

export function isBuiltInTool(name: string): name is BuiltInTool {
  return (builtInToolNames as readonly string[]).includes(name);
}

export function validateAskUserArguments(argumentsText: string): BuiltInArgumentsValidation<AskUserArguments> {
  const parsed = parseArgumentsObject("ask_user", argumentsText);
  if (!parsed.isValid) {
    return parsed;
  }
  const record = parsed.arguments;
  const question = record["question"];
  if (typeof question !== "string" || isBlankText(question)) {
    return invalidArguments("ask_user", "question must be a non-empty string");
  }
  const choices = record["choices"] ?? [];
  if (!Array.isArray(choices) || !choices.every((choice) => typeof choice === "string" && !isBlankText(choice))) {
    return invalidArguments("ask_user", "choices must be a list of non-empty strings");
  }
  const allowsFreeText = record["allows_free_text"] ?? true;
  if (typeof allowsFreeText !== "boolean") {
    return invalidArguments("ask_user", "allows_free_text must be a boolean");
  }
  return {
    isValid: true,
    arguments: { question, choices: choices as string[], allowsFreeText: allowsFreeText || choices.length === 0 },
  };
}

/** A JSON number without a fraction, such as `2.0`, counts as an integer, as JSON Schema defines it. */
export function validateDurableWaitArguments(
  argumentsText: string,
  maxDurableWaitSeconds: number,
): BuiltInArgumentsValidation<DurableWaitArguments> {
  const parsed = parseArgumentsObject("durable_wait", argumentsText);
  if (!parsed.isValid) {
    return parsed;
  }
  const record = parsed.arguments;
  const durationSeconds = record["duration_seconds"];
  if (typeof durationSeconds !== "number" || !Number.isInteger(durationSeconds)) {
    return invalidArguments("durable_wait", "duration_seconds must be an integer");
  }
  if (durationSeconds < 1 || durationSeconds > maxDurableWaitSeconds) {
    return invalidArguments("durable_wait", `duration_seconds must be between 1 and ${maxDurableWaitSeconds}`);
  }
  const reason = record["reason"];
  if (typeof reason !== "string") {
    return invalidArguments("durable_wait", "reason must be a string");
  }
  return { isValid: true, arguments: { durationSeconds, reason } };
}

function parseArgumentsObject(
  builtInTool: BuiltInTool,
  argumentsText: string,
): BuiltInArgumentsValidation<Readonly<Record<string, unknown>>> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText);
  } catch {
    return invalidArguments(builtInTool, "arguments must be a JSON object");
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return invalidArguments(builtInTool, "arguments must be a JSON object");
  }
  const definition = builtInToolDefinitions.find((candidate) => candidate.name === builtInTool);
  const allowedNames = Object.keys((definition?.parameters["properties"] ?? {}) as object);
  // Sorted by code point, as the Python core sorts, so both report the same name.
  const [unknownName] = Object.keys(parsed)
    .filter((name) => !allowedNames.includes(name))
    .sort((left, right) => (left < right ? -1 : left > right ? 1 : 0));
  if (unknownName !== undefined) {
    return invalidArguments(builtInTool, `unknown argument ${unknownName}`);
  }
  return { isValid: true, arguments: parsed as Readonly<Record<string, unknown>> };
}

function invalidArguments(builtInTool: BuiltInTool, reason: string): { readonly isValid: false; readonly error: string } {
  return { isValid: false, error: `invalid ${builtInTool} arguments: ${reason}` };
}
