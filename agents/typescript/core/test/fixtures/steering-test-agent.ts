// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import {
  createAgentSessionFlow,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type FrameworkMessage,
} from "../../src/index.js";
import {
  StepwiseAgentRunner,
  functionTool,
  type AgentDefinition,
  type ModelRequest,
} from "../../src/stepwise/index.js";
import {
  FileGate,
  InvocationLog,
  ScriptedModelClient,
  type ScriptedModelReply,
} from "../../src/testing/index.js";
import { ScriptedAgentRunner, createScriptedAgentFlow } from "./scripted-agent-runner.js";

/** Flow type of the Stepwise steering test agent. */
export const steeringTestAgentFlowType = "SteeringTestAgent";
/** Flow type of the steering test agent whose runner stores no unfinished-turn messages. */
export const steeringNoteAgentFlowType = "SteeringNoteAgent";
/** Environment variable naming the marker directory of a steering test Worker child. */
export const steeringMarkerDirectoryVariable = "STEERING_AGENT_MARKER_DIRECTORY";
/** Environment variable naming a FileGate that `buildUnfinishedTurnMessages` waits at, blocking the Worker. */
export const steeringBuilderGateVariable = "STEERING_AGENT_BUILDER_GATE";

/** Options of the steering test agent. */
export interface SteeringTestAgentOptions {
  /** FileGate that `buildUnfinishedTurnMessages` waits at synchronously before it returns. */
  readonly builderGate?: string;
}

/**
 * Creates the Stepwise steering test agent on a scripted model; tests and Worker children build it identically.
 *
 * The model reads the current turn's last user text, as the Python core's Stepwise test agent does:
 * - `say <text>` answers `said: <text>`; `stall <gate>` waits at the gate, then answers `stalled at <gate>`.
 * - `lookup-gated <gate> <query>` calls `lookup_order`, a read that waits at the gate.
 * - `publish <message>` calls `publish_note`, a write that needs approval; `publish-as <id> <message>` does the
 *   same with a fixed tool call ID.
 * - `slow-write <gate> <message>` calls `slow_write`, a single-attempt write that waits at the gate.
 * - `stall-then-call <gate> <query> | <message>` waits at the gate in the first model call, then calls
 *   `lookup_order` and `publish_note` in one response; `parallel <gate> <query> | <message>` calls both at once,
 *   and the lookup waits at the gate.
 * - `ask <question> | <choice> ...` calls `ask_user`; `wait <seconds> <reason>` calls `durable_wait`.
 * - `transfer-late-ask <amount> <gate>` calls `transfer_funds`, which waits at the gate, then asks which account.
 *
 * After its tool results arrive, the model answers `<tool>: <output>` per result, joined with `; `. Model calls
 * and tools record `<flowId> <name>` in `invocations.log` on every run.
 */
export function createSteeringTestAgentFlow(
  markerDirectory: string,
  options: SteeringTestAgentOptions = {},
): AgentSessionFlow<StepwiseAgentRunner> {
  const definition = createSteeringAgentDefinition(markerDirectory);
  return createAgentSessionFlow(
    new GatedUnfinishedTurnRunner(definition, markerDirectory, options.builderGate),
    steeringTestAgentFlowType,
    { builtInTools: definition.builtInTools ?? [] },
  );
}

/**
 * Creates a scripted adapter-style agent whose turn items live in its run state and that implements no
 * `buildUnfinishedTurnMessages`, so a steered or cancelled turn ends with a note.
 */
export function createSteeringNoteAgentFlow(
  markerDirectory: string,
): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>> {
  return createScriptedAgentFlow(markerDirectory, {
    flowType: steeringNoteAgentFlowType,
    runner: new RunnerWithoutUnfinishedTurnMessages(new ScriptedAgentRunner(markerDirectory)),
  });
}

/** Reads the steering test agent's tool and model invocations. */
export function readSteeringInvocations(markerDirectory: string): InvocationLog {
  return new InvocationLog(join(markerDirectory, "invocations.log"));
}

class GatedUnfinishedTurnRunner extends StepwiseAgentRunner {
  public constructor(
    definition: AgentDefinition,
    private readonly markerDirectory: string,
    private readonly builderGate: string | undefined,
  ) {
    super(definition);
  }

  public override buildUnfinishedTurnMessages(turn: AgentTurnContext): readonly FrameworkMessage[] {
    if (this.builderGate !== undefined) {
      const gate = new FileGate(this.markerDirectory, this.builderGate);
      gate.markReached();
      // The method is synchronous, so the Worker blocks here until the test kills it.
      const pause = new Int32Array(new SharedArrayBuffer(4));
      while (!gate.isReleased()) {
        Atomics.wait(pause, 0, 0, 50);
      }
    }
    return super.buildUnfinishedTurnMessages(turn);
  }
}

class RunnerWithoutUnfinishedTurnMessages implements AgentFrameworkRunner<DeferredToolOutcome> {
  public constructor(private readonly runner: ScriptedAgentRunner) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return this.runner.buildUserMessage(text);
  }

  public runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    return this.runner.runModelStep(turn);
  }

  public executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    return this.runner.executeDeferredToolCall(call);
  }
}

function createSteeringAgentDefinition(markerDirectory: string): AgentDefinition {
  const invocations = readSteeringInvocations(markerDirectory);
  const waitAtGate = async (name: string, signal: AbortSignal): Promise<void> => {
    const gate = new FileGate(markerDirectory, name);
    gate.markReached();
    await gate.waitForRelease(signal);
  };
  const respond = async (request: ModelRequest): Promise<ScriptedModelReply> => {
    const command = ScriptedModelClient.userTexts(request).at(-1) ?? "";
    const results = ScriptedModelClient.toolResults(request);
    invocations.record(`${request.metadata.flowId} model:${request.metadata.turnId}:${results.length + 1}`);
    const [verb = "", ...words] = command.split(" ");
    const rest = words.join(" ");
    if (results.length > 0) {
      return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("; ") };
    }
    switch (verb) {
      case "say":
        return { text: `said: ${rest}` };
      case "stall":
        await waitAtGate(rest, request.cancellationSignal);
        return { text: `stalled at ${rest}` };
      case "lookup-gated": {
        const [gate = "", query = ""] = splitOnce(rest);
        return { toolCalls: [{ toolName: "lookup_order", arguments: { query, gate } }] };
      }
      case "publish":
        return { toolCalls: [{ toolName: "publish_note", arguments: { message: rest } }] };
      case "publish-as": {
        const [toolCallId = "", message = ""] = splitOnce(rest);
        return { toolCalls: [{ toolName: "publish_note", arguments: { message }, toolCallId }] };
      }
      case "slow-write": {
        const [gate = "", message = ""] = splitOnce(rest);
        return { toolCalls: [{ toolName: "slow_write", arguments: { message, gate } }] };
      }
      case "stall-then-call":
      case "parallel": {
        const [gate = "", body = ""] = splitOnce(rest);
        const [query = "", message = ""] = body.split("|").map((part) => part.trim());
        if (verb === "stall-then-call") {
          await waitAtGate(gate, request.cancellationSignal);
        }
        return {
          toolCalls: [
            { toolName: "lookup_order", arguments: verb === "parallel" ? { query, gate } : { query } },
            { toolName: "publish_note", arguments: { message } },
          ],
        };
      }
      case "ask": {
        const [question = "", ...choices] = rest.split("|").map((part) => part.trim());
        return {
          text: "I need to ask you something.",
          toolCalls: [{ toolName: "ask_user", arguments: choices.length === 0 ? { question } : { question, choices } }],
        };
      }
      case "wait": {
        const [seconds = "0", reason = ""] = splitOnce(rest);
        return { toolCalls: [{ toolName: "durable_wait", arguments: { duration_seconds: Number(seconds), reason } }] };
      }
      case "transfer-late-ask": {
        const [amount = "0", askGate = ""] = splitOnce(rest);
        return { toolCalls: [{ toolName: "transfer_funds", arguments: { amount: Number(amount), ask_gate: askGate } }] };
      }
      default:
        throw new Error(`unknown scripted command ${JSON.stringify(command)}`);
    }
  };
  const stringParameters = (...names: string[]) => ({
    type: "object",
    properties: Object.fromEntries(names.map((name) => [name, { type: "string" }])),
    required: names.filter((name) => name !== "gate"),
    additionalProperties: false,
  });
  return {
    name: "steering-test",
    instructions: "You are a scripted test agent.",
    model: new ScriptedModelClient(respond),
    tools: [
      functionTool<{ query: string; gate?: string }>({
        name: "lookup_order",
        description: "Look up an order.",
        parameters: stringParameters("query", "gate"),
        effect: "read",
        execute: async ({ query, gate }, context) => {
          invocations.record(`${context.flowId} lookup_order`);
          if (gate !== undefined) {
            await waitAtGate(gate, context.cancellationSignal);
          }
          return `found ${query}`;
        },
      }),
      functionTool<{ message: string }>({
        name: "publish_note",
        description: "Publish a note.",
        parameters: stringParameters("message"),
        effect: "non_idempotent_write",
        execute: ({ message }, context) => {
          invocations.record(`${context.flowId} publish_note`);
          return { published: message };
        },
      }),
      functionTool<{ message: string; gate: string }>({
        name: "slow_write",
        description: "Write slowly.",
        parameters: { ...stringParameters("message", "gate"), required: ["message", "gate"] },
        effect: "non_idempotent_write",
        requiresApproval: false,
        execute: async ({ message, gate }, context) => {
          invocations.record(`${context.flowId} slow_write`);
          await waitAtGate(gate, context.cancellationSignal);
          return `wrote ${message}`;
        },
      }),
      functionTool<{ amount: number; ask_gate: string }>({
        name: "transfer_funds",
        description: "Move money between accounts.",
        parameters: {
          type: "object",
          properties: { amount: { type: "integer" }, ask_gate: { type: "string" } },
          required: ["amount", "ask_gate"],
          additionalProperties: false,
        },
        effect: "non_idempotent_write",
        requiresApproval: false,
        execute: async ({ amount, ask_gate }, context) => {
          if (context.inputAnswers.length === 0) {
            await waitAtGate(ask_gate, context.cancellationSignal);
            invocations.record(`${context.flowId} transfer_funds:ask`);
            return {
              kind: "tool_input_requested",
              question: "Which account?",
              choices: ["checking", "savings"],
              allowsFreeText: false,
              continuation: JSON.stringify({ amount }),
            };
          }
          invocations.record(`${context.flowId} transfer_funds:run`);
          return `moved ${amount} from ${context.inputAnswers.at(-1)?.answer}`;
        },
      }),
    ],
    builtInTools: ["ask_user", "durable_wait"],
  };
}

function splitOnce(text: string): [string, string] {
  const index = text.indexOf(" ");
  return index === -1 ? [text, ""] : [text.slice(0, index), text.slice(index + 1)];
}
