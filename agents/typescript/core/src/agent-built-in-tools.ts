// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentPlanTask, PlanTaskStatus } from "./agent-session-values.js";

/**
 * Name of a built-in tool that the session itself handles.
 *
 * - `ask_user`: asks the user a question. The session raises a `question` interrupt, waits in
 *   `AwaitQuestionAnswer`, and the answer becomes the tool result, so the model continues the same turn.
 * - `durable_wait`: suspends the turn on a Dex Timer in `AwaitTimer`. No model call, coroutine, or
 *   Worker holds the wait.
 * - `write_todos`: replaces the session's {@link AgentPlan} with a new revision. `RunModel` records the
 *   call in its own commit; no tool Step runs.
 *
 * Enable built-in tools with {@link AgentSessionOptions.builtInTools}. A runner offers the enabled ones
 * to its model, from {@link builtInToolDefinitions}, like any other tool, and returns their calls in
 * {@link ToolCallsRequested} like any other call. The session routes a call whose name is an enabled
 * built-in tool to that tool's handling instead of a tool Step. An enabled built-in name is reserved.
 * A disabled built-in name is an ordinary tool name.
 */
export type BuiltInTool = "ask_user" | "durable_wait" | "write_todos";

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
  Object.freeze({
    name: "write_todos",
    description:
      "Replace the plan with a complete, ordered list of tasks. Send every task each time, with its " +
      "status. An empty list clears the plan. Keep the statuses current: mark a task in_progress when you " +
      "start it and completed when it is done.",
    parameters: Object.freeze({
      type: "object",
      properties: {
        todos: {
          type: "array",
          description: "Every task of the plan, in order.",
          items: {
            type: "object",
            properties: {
              content: { type: "string", description: "What the task does, in one short sentence." },
              status: {
                type: "string",
                enum: ["pending", "in_progress", "completed"],
                description: "Where the task stands.",
              },
            },
            required: ["content", "status"],
            additionalProperties: false,
          },
        },
      },
      required: ["todos"],
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

/** Error of every `write_todos` call of a model response that has more than one; the same text as the Python core. */
export const writeTodosCalledMoreThanOnce = "write_todos was called more than once in one response";

const planTaskStatuses: readonly PlanTaskStatus[] = ["pending", "in_progress", "completed"];

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

/** Validates `write_todos` arguments; the error is `invalid todos: <reason>`, as in the Python core. */
export function validateWriteTodosArguments(argumentsText: string): BuiltInArgumentsValidation<readonly AgentPlanTask[]> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText);
  } catch {
    return invalidTodos("arguments must be a JSON object");
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return invalidTodos("arguments must be a JSON object");
  }
  const [unknownName] = sortByCodePoint(Object.keys(parsed).filter((name) => name !== "todos"));
  if (unknownName !== undefined) {
    return invalidTodos(`unknown argument ${unknownName}`);
  }
  const todos = (parsed as Readonly<Record<string, unknown>>)["todos"];
  if (!Array.isArray(todos)) {
    return invalidTodos("todos must be a list");
  }
  const tasks: AgentPlanTask[] = [];
  for (const [index, todo] of todos.entries()) {
    if (typeof todo !== "object" || todo === null || Array.isArray(todo)) {
      return invalidTodos(`todos[${index}] must be an object`);
    }
    const [unknownField] = sortByCodePoint(Object.keys(todo).filter((name) => name !== "content" && name !== "status"));
    if (unknownField !== undefined) {
      return invalidTodos(`todos[${index}] has unknown field ${unknownField}`);
    }
    const { content, status } = todo as Readonly<Record<string, unknown>>;
    if (typeof content !== "string" || isBlankText(content)) {
      return invalidTodos(`todos[${index}].content must be a non-empty string`);
    }
    if (typeof status !== "string" || !(planTaskStatuses as readonly string[]).includes(status)) {
      return invalidTodos(`todos[${index}].status must be pending, in_progress, or completed`);
    }
    tasks.push({ content, status: status as PlanTaskStatus });
  }
  return { isValid: true, arguments: tasks };
}

/** Tool output after `write_todos` wrote tasks: `{"status":"updated","revision":<n>,"task_count":<k>}`. */
export function buildPlanUpdatedOutput(revision: number, taskCount: number): string {
  return JSON.stringify({ status: "updated", revision, task_count: taskCount });
}

/** Tool output after `write_todos` cleared the plan: `{"status":"cleared","revision":<n>}`. */
export function buildPlanClearedOutput(revision: number): string {
  return JSON.stringify({ status: "cleared", revision });
}

/** Tool output after the user answered `ask_user`: `{"status":"answered","answer":"<answer>"}`. */
export function buildQuestionAnsweredOutput(answer: string): string {
  return JSON.stringify({ status: "answered", answer });
}

/** Tool output after a `durable_wait` Timer ended: `{"status":"completed","waited_seconds":<n>,"reason":"<reason>"}`. */
export function buildDurableWaitCompletedOutput(waitedSeconds: number, reason: string): string {
  return JSON.stringify({ status: "completed", waited_seconds: waitedSeconds, reason });
}

function invalidTodos(reason: string): { readonly isValid: false; readonly error: string } {
  return { isValid: false, error: `invalid todos: ${reason}` };
}

/** Sorted by code point, as Python's `sorted` orders strings, so both cores report the same name. */
function sortByCodePoint(names: readonly string[]): string[] {
  return [...names].sort(compareByCodePoint);
}

/** JavaScript's `<` compares UTF-16 code units, which orders characters above U+FFFF before U+E000. */
function compareByCodePoint(left: string, right: string): number {
  const leftCodePoints = Array.from(left, (character) => character.codePointAt(0) ?? 0);
  const rightCodePoints = Array.from(right, (character) => character.codePointAt(0) ?? 0);
  const sharedLength = Math.min(leftCodePoints.length, rightCodePoints.length);
  for (let index = 0; index < sharedLength; index += 1) {
    const difference = (leftCodePoints[index] ?? 0) - (rightCodePoints[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return leftCodePoints.length - rightCodePoints.length;
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
  const [unknownName] = sortByCodePoint(Object.keys(parsed).filter((name) => !allowedNames.includes(name)));
  if (unknownName !== undefined) {
    return invalidArguments(builtInTool, `unknown argument ${unknownName}`);
  }
  return { isValid: true, arguments: parsed as Readonly<Record<string, unknown>> };
}

function invalidArguments(builtInTool: BuiltInTool, reason: string): { readonly isValid: false; readonly error: string } {
  return { isValid: false, error: `invalid ${builtInTool} arguments: ${reason}` };
}
