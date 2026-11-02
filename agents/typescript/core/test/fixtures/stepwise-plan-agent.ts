// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";

import {
  createAgentSessionFlow,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type DeferredToolOutcome,
  type FrameworkMessage,
} from "../../src/index.js";
import {
  StepwiseAgentRunner,
  functionTool,
  type AgentDefinition,
  type ModelRequest,
  type PlanningToolChoice,
  type ModelToolResultItem,
} from "../../src/stepwise/index.js";
import {
  FileGate,
  InvocationLog,
  ScriptedModelClient,
  type ScriptedModelReply,
  type ScriptedToolCall,
} from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "./scripted-agent-runner.js";

/** Flow type of the Stepwise agent with plan mode. */
export const stepwisePlanAgentFlowType = "StepwisePlanAgent";
/** Environment variable naming the marker directory of a plan agent Worker child. */
export const planAgentMarkerDirectoryVariable = "PLAN_AGENT_MARKER_DIRECTORY";
/** Environment variable `<block|fail>:<text prefix>` that gates `buildUserMessage`. */
export const planAgentUserMessageGateVariable = "PLAN_AGENT_USER_MESSAGE_GATE";
/** FileGate that `buildUserMessage` waits at, or fails before, while the gate variable is set. */
export const userMessageGateName = "user-message";
/** Flow type of the Stepwise plan agent whose planning tool choice is `required`. */
export const requiredPlanningAgentFlowType = "StepwisePlanAgentRequired";
/** Flow type of the scripted framework runner with `write_todos` and no plan mode. */
export const scriptedPlanWriterFlowType = "ScriptedPlanWriterAgent";
/** Prefix of the user message of a turn that `execute_plan` started. */
export const approvedPlanPrefix = "[Dex] The user approved plan revision";

const planNotePrefix = "[Dex] The current plan";
const sleepCell = new Int32Array(new SharedArrayBuffer(4));

/** One model request the plan agent logged, read back with {@link readPlanModelRequests}. */
export interface LoggedPlanModelRequest {
  readonly flowId: string;
  readonly turnId: string;
  readonly tools: readonly string[];
  readonly toolChoice: string;
  readonly userTexts: readonly string[];
}

/** Waits at, or fails before, the user message whose text has a prefix, until the gate is released. */
class GatedPlanRunner extends StepwiseAgentRunner {
  private readonly gateMode: string;
  private readonly gatedPrefix: string;

  public constructor(
    definition: AgentDefinition,
    private readonly markerDirectory: string,
    gate: string | undefined,
  ) {
    super(definition);
    const separator = (gate ?? "").indexOf(":");
    this.gateMode = separator === -1 ? "" : (gate ?? "").slice(0, separator);
    this.gatedPrefix = separator === -1 ? "" : (gate ?? "").slice(separator + 1);
  }

  public override buildUserMessage(text: string): FrameworkMessage {
    if (this.gateMode !== "" && text.startsWith(this.gatedPrefix)) {
      const gate = new FileGate(this.markerDirectory, userMessageGateName);
      if (!gate.isReleased()) {
        gate.markReached();
        if (this.gateMode === "fail") {
          throw new Error("scripted buildUserMessage failure");
        }
        // The test kills this Worker here; nothing else needs its event loop.
        while (!gate.isReleased()) {
          Atomics.wait(sleepCell, 0, 0, 50);
        }
      }
    }
    return super.buildUserMessage(text);
  }
}

/**
 * Creates the Stepwise agent with plan mode, on a scripted model that reads the current turn's user text:
 * - `plan <task> | <task> ...` drafts those tasks with `write_todos`, then answers `drafted <n> tasks`.
 * - `plan-sneak <task>` calls `lookup_order` next to `write_todos`.
 * - `revise <task> | ...` writes those tasks as a new draft; `discuss` answers with the plan's task count.
 * - `todos <json>` calls `write_todos` with that `todos` list; `todos-twice <json>` calls it twice in one
 *   response; `todos-raw <json>` sends the raw arguments.
 * - `stall <gate>` waits at that gate, then answers `stalled at <gate>`.
 * - The approved plan's message makes the model look up the first task, then answer, or, when a task
 *   mentions `finish`, mark every task completed first. A first task `stall <gate>` waits at that gate.
 * Every model call is logged in `model-requests.log`, and every tool run in `invocations.log`.
 */
export function createStepwisePlanAgentFlow(
  markerDirectory: string,
  userMessageGate?: string,
  planningToolChoice: PlanningToolChoice = "auto",
): AgentSessionFlow<StepwiseAgentRunner> {
  const invocations = new InvocationLog(join(markerDirectory, "invocations.log"));
  const modelRequests = new InvocationLog(join(markerDirectory, "model-requests.log"));
  const respond = async (request: ModelRequest): Promise<ScriptedModelReply> => {
    const userTexts = ScriptedModelClient.userTexts(request);
    const command = userTexts.at(-1) ?? "";
    const results = ScriptedModelClient.toolResults(request);
    modelRequests.record(JSON.stringify(describeRequest(request, userTexts)));
    const separator = command.indexOf(" ");
    const verb = separator === -1 ? command : command.slice(0, separator);
    const rest = separator === -1 ? "" : command.slice(separator + 1);
    if (command.startsWith(approvedPlanPrefix)) {
      const tasks = readPlanNoteTasks(userTexts);
      const firstTask = tasks[0] ?? "";
      if (results.length === 0 && firstTask.startsWith("stall ")) {
        const gate = new FileGate(markerDirectory, firstTask.slice("stall ".length));
        gate.markReached();
        await gate.waitForRelease();
      }
      return executePlan(tasks, results);
    }
    if (results.length > 0) {
      if (verb === "plan") {
        return { text: `drafted ${splitTasks(rest).length} tasks` };
      }
      return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("; ") };
    }
    switch (verb) {
      case "plan":
      case "revise":
        return writeTodos(splitTasks(rest).map((content) => ({ content, status: "pending" })));
      case "plan-sneak":
        return {
          toolCalls: [
            { toolName: "lookup_order", arguments: { query: rest } },
            writeTodosCall([{ content: rest, status: "pending" }]),
          ],
        };
      case "discuss":
        return { text: `the plan has ${readPlanNoteTasks(userTexts).length} tasks` };
      case "todos":
        return writeTodos(JSON.parse(rest) as readonly Readonly<Record<string, string>>[]);
      case "todos-twice": {
        const todos = JSON.parse(rest) as readonly Readonly<Record<string, string>>[];
        return { toolCalls: [writeTodosCall(todos), writeTodosCall(todos)] };
      }
      case "todos-raw":
        return { toolCalls: [{ toolName: "write_todos", arguments: rest }] };
      case "stall": {
        const gate = new FileGate(markerDirectory, rest);
        gate.markReached();
        await gate.waitForRelease(request.cancellationSignal);
        return { text: `stalled at ${rest}` };
      }
      default:
        return { text: `said: ${command}` };
    }
  };
  const definition: AgentDefinition = {
    name: "plan-test",
    instructions: "You are a scripted planning agent.",
    model: new ScriptedModelClient(respond),
    tools: [
      functionTool<{ query: string }>({
        name: "lookup_order",
        description: "Look up an order.",
        parameters: { type: "object", properties: { query: { type: "string" } }, required: ["query"] },
        effect: "read",
        execute: ({ query }) => {
          invocations.record("lookup_order");
          return `found ${query}`;
        },
      }),
      functionTool<{ message: string }>({
        name: "publish_note",
        description: "Publish a note.",
        parameters: { type: "object", properties: { message: { type: "string" } }, required: ["message"] },
        effect: "non_idempotent_write",
        execute: ({ message }) => {
          invocations.record("publish_note");
          return `published ${message}`;
        },
      }),
    ],
    builtInTools: ["ask_user", "write_todos"],
    planModeEnabled: true,
    planningToolChoice,
  };
  return createAgentSessionFlow(
    new GatedPlanRunner(definition, markerDirectory, userMessageGate),
    planningToolChoice === "required" ? requiredPlanningAgentFlowType : stepwisePlanAgentFlowType,
    { builtInTools: definition.builtInTools ?? [], planModeEnabled: true },
  );
}

/**
 * Creates the Flows a plan agent Worker serves: the Stepwise plan agent, the scripted plan writer, and the
 * Stepwise plan agent whose planning tool choice is `required`.
 * @param markerDirectory - The marker directory.
 * @param userMessageGate - `<block|fail>:<text prefix>`, or `undefined` for no gate.
 * @returns Both Flows; a Client builds them with the same arguments.
 */
export function createPlanAgentWorkerFlows(
  markerDirectory: string,
  userMessageGate?: string,
): [
  AgentSessionFlow<StepwiseAgentRunner>,
  AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  AgentSessionFlow<StepwiseAgentRunner>,
] {
  return [
    createStepwisePlanAgentFlow(markerDirectory, userMessageGate),
    createScriptedAgentFlow(markerDirectory, { flowType: scriptedPlanWriterFlowType, builtInTools: ["write_todos"] }),
    createStepwisePlanAgentFlow(markerDirectory, userMessageGate, "required"),
  ];
}

/**
 * Reads every model request the plan agent logged, oldest first.
 * @param markerDirectory - The agent's marker directory.
 * @returns The logged requests.
 */
export function readPlanModelRequests(markerDirectory: string): LoggedPlanModelRequest[] {
  const path = join(markerDirectory, "model-requests.log");
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as LoggedPlanModelRequest);
}

function describeRequest(request: ModelRequest, userTexts: readonly string[]): LoggedPlanModelRequest {
  const toolChoice = request.toolChoice;
  return {
    flowId: request.metadata.flowId,
    turnId: request.metadata.turnId,
    tools: request.tools.map((tool) => tool.name),
    toolChoice: typeof toolChoice === "string" ? toolChoice : toolChoice.toolName,
    userTexts,
  };
}

function executePlan(tasks: readonly string[], results: readonly ModelToolResultItem[]): ScriptedModelReply {
  if (results.length === 0) {
    return { toolCalls: [{ toolName: "lookup_order", arguments: { query: tasks[0] ?? "" } }] };
  }
  if (tasks.some((task) => task.includes("finish")) && results.every((result) => result.toolName !== "write_todos")) {
    return writeTodos(tasks.map((content) => ({ content, status: "completed" })));
  }
  return { text: `executed ${tasks.length} tasks` };
}

function readPlanNoteTasks(userTexts: readonly string[]): string[] {
  const note = userTexts.find((text) => text.startsWith(planNotePrefix)) ?? "";
  return note
    .split("\n")
    .filter((line) => line.startsWith("- ["))
    .map((line) => line.slice(line.indexOf("] ") + 2));
}

function splitTasks(text: string): string[] {
  return text
    .split("|")
    .map((task) => task.trim())
    .filter((task) => task !== "");
}

function writeTodos(todos: readonly Readonly<Record<string, string>>[]): ScriptedModelReply {
  return { toolCalls: [writeTodosCall(todos)] };
}

function writeTodosCall(todos: readonly Readonly<Record<string, string>>[]): ScriptedToolCall {
  return { toolName: "write_todos", arguments: { todos } };
}
