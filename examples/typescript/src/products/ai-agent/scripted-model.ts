/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { setTimeout as delay } from "node:timers/promises";

import {
  ScriptedModelClient,
  contextSummaryInstructions,
  type ModelRequest,
  type ScriptedModelReply,
  type ScriptedToolCall,
} from "@superdurable/dex-agents/stepwise";

export const demoReplyPrefix = "Local demo response: ";
const approvedPlanPrefix = "[Dex] The user approved plan revision";
const demoCommandUsage =
  "Usage: /tool <name> <json>, /ask <question>, /choose <question> | <choice> | <choice>, " +
  "/wait <seconds> <reason>, /slow <seconds> <text>, /plan-stop <objective>, /plan-clear, or /model.";

/**
 * A `mock/<name>` model, the same demo model as the Python example. It replies with the user's text and
 * reads slash commands, so every feature works without a provider key; `/model` names the model.
 */
export function createScriptedModelClient(model: string): ScriptedModelClient {
  return new ScriptedModelClient((request) => replyAsDemoModel(request, model));
}

async function replyAsDemoModel(request: ModelRequest, model: string): Promise<ScriptedModelReply> {
  if (request.instructions === contextSummaryInstructions) {
    return { text: `Summary of ${request.inputItems.length} earlier items.` };
  }
  const userTexts = ScriptedModelClient.userTexts(request);
  const objective = [...userTexts].reverse().find((text) => !text.startsWith("[Dex]")) ?? "";
  let rounds: ScriptedToolCall[][];
  try {
    if (request.tools.length === 1 && request.tools[0]?.name === "write_todos") {
      rounds = planningRounds(objective);
    } else if (userTexts.at(-1)?.startsWith(approvedPlanPrefix)) {
      rounds = objective.startsWith("/plan-stop") ? [] : [[call("write_todos", { todos: planTasks(objective, "completed") })]];
    } else {
      rounds = await chatRounds(objective);
    }
  } catch {
    return { text: demoCommandUsage };
  }
  const results = ScriptedModelClient.toolResults(request);
  let answeredCount = results.length;
  for (const toolCalls of rounds) {
    if (answeredCount === 0) {
      return { toolCalls };
    }
    answeredCount -= toolCalls.length;
  }
  const outputs = results.map((result) => result.output).join("; ");
  const reply = objective === "/model" ? model : objective;
  return { text: demoReplyPrefix + (results.length === 0 ? reply : outputs) };
}

function planningRounds(objective: string): ScriptedToolCall[][] {
  const todos = objective.startsWith("/plan-clear") ? [] : planTasks(objective, "pending");
  return [[call("write_todos", { todos })]];
}

async function chatRounds(objective: string): Promise<ScriptedToolCall[][]> {
  const [command = "", ...rest] = objective.split(" ");
  const argument = rest.join(" ");
  switch (command) {
    case "/tool": {
      const [toolName = "", ...json] = rest;
      return [[{ toolName, arguments: JSON.parse(json.join(" ") || "{}") as Record<string, unknown> }]];
    }
    case "/ask":
      return [[call("ask_user", { question: argument })]];
    case "/choose": {
      const [question, ...choices] = argument.split("|").map((part) => part.trim());
      if (choices.length === 0) throw new SyntaxError("no choices");
      return [[call("ask_user", { question, choices, allows_free_text: false })]];
    }
    case "/wait":
      return [[call("durable_wait", { duration_seconds: requireSeconds(rest[0]), reason: rest.slice(1).join(" ") || "a wait" })]];
    case "/slow":
      await delay(requireSeconds(rest[0]) * 1_000);
      return [];
    default:
      return [];
  }
}

function requireSeconds(text: string | undefined): number {
  const seconds = Number(text);
  if (!Number.isInteger(seconds) || seconds <= 0) throw new SyntaxError("seconds must be a positive integer");
  return seconds;
}

function planTasks(objective: string, status: string): { content: string; status: string }[] {
  return [
    { content: `Work on: ${objective}`, status },
    { content: "Report the result", status },
  ];
}

function call(toolName: string, toolArguments: Record<string, unknown>): ScriptedToolCall {
  return { toolName, arguments: toolArguments };
}
