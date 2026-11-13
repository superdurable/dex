// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import type { Codec, Flow, Step, StepOptions } from "@superdurable/dex";

import { renderSessionFlowGraph, sessionFlowDefinitionDocument } from "../../src/index.js";
import type { AgentSessionPersistence } from "../../src/agent-session-persistence.js";
import { toolStepOptions, toolTaskPollMovement } from "../../src/agent-session-steps.js";
import { agentSessionStepTypes } from "../../src/agent-session-step-types.js";
import type { AgentToolCallRecord, ToolEffect } from "../../src/agent-session-values.js";
import { createBuiltinAgentSessionFlow } from "../../src/builtin-loop/index.js";
import { ScriptedModelClient } from "../../src/testing/index.js";
import { conformanceSuiteDirectory, readConformanceJson } from "../fixtures/conformance-suite.js";
import { SessionFlowSource } from "../fixtures/session-flow-source.js";

interface GraphNode {
  readonly id: string;
  readonly kind: string;
  readonly name: string;
  readonly parentId?: string;
  readonly phase?: string;
  readonly start?: boolean;
  readonly wait?: {
    readonly type: string;
    readonly conditions: readonly { readonly kind: string; readonly label: string }[];
  };
  readonly decision?: {
    readonly type: string;
    readonly cancellations?: readonly { readonly stepId: string }[];
  };
  readonly resource?: { readonly map?: boolean };
}

interface GraphEdge {
  readonly kind: string;
  readonly from: string;
  readonly to: string;
}

interface GraphAction {
  readonly rpcName: string;
  readonly requiredPermission: string;
  readonly condition?: unknown;
  readonly input: { readonly fields: readonly { readonly fieldName: string; readonly required: boolean }[] };
}

interface SessionGraph {
  readonly flow: { readonly name: string; readonly startStepId: string };
  readonly nodes: readonly GraphNode[];
  readonly edges: readonly GraphEdge[];
  readonly v2: { readonly actions: readonly GraphAction[] };
}

interface RegisteredRPC {
  readonly name: string;
  readonly methodName?: string;
  readonly options: {
    readonly inputCodec?: Codec<unknown>;
    readonly lockAttributes?: readonly { readonly attribute: { readonly name: string } }[];
  };
}

const { registeredRPCs } = (await import(
  new URL("../../../node_modules/@superdurable/dex/dist/src/rpc.js", import.meta.url).href
)) as { registeredRPCs: (flow: Flow<unknown>) => readonly RegisteredRPC[] };

const graph = readConformanceJson<SessionGraph>("session_graph.json");
const renderings = readConformanceJson<{
  readonly cases: readonly { readonly flow_type: string; readonly action_permission: string; readonly sha256: string }[];
  readonly generated_at: string;
  readonly template_sha256: string;
}>("session_graph_renderings.json");
const flow = createBuiltinAgentSessionFlow(
  { name: "graph-manifest", instructions: "Describe the session Flow.", model: new ScriptedModelClient(() => ({ text: "ok" })) },
  "ConformanceAgent",
);
const steps = [...flow.getSteps()];
const rpcs = registeredRPCs(flow as Flow<unknown>);
// The Flow exposes every persistence definition under its AgentSessionPersistence name.
const persistence = flow as unknown as AgentSessionPersistence;
const source = new SessionFlowSource();
const stepTypeOfKey = agentSessionStepTypes as Readonly<Record<string, string>>;

function nodesOf(kind: string): readonly GraphNode[] {
  return graph.nodes.filter((node) => node.kind === kind);
}

function classNameOf(step: Step<unknown>): string {
  return step.constructor.name;
}

function channelNameOf(persistenceField: string): string {
  return `${persistenceField.charAt(0).toUpperCase()}${persistenceField.slice(1)}`;
}

function stepTypeOfTarget(target: unknown): string {
  const reference = target as { readonly stepType?: string; getStepType?(): string };
  return reference.stepType ?? reference.getStepType?.() ?? String(target);
}

function failureTargetOf(options: StepOptions | undefined): string | undefined {
  return options?.executeFailure === undefined ? undefined : stepTypeOfTarget(options.executeFailure.step);
}

function transitionsFrom(ownerId: string): { targets: string[]; endings: string[] } {
  const decisions = graph.nodes.filter((node) => node.parentId === ownerId && node.kind === "decision");
  const decisionIds = new Set(decisions.map((node) => node.id));
  return {
    targets: graph.edges
      .filter((edge) => edge.kind === "transition" && decisionIds.has(edge.from))
      .map((edge) => edge.to.replace("step:", ""))
      .sort(),
    endings: decisions
      .map((node) => node.decision?.type ?? "")
      .filter((type) => type !== "goTo")
      .sort(),
  };
}

function publishesOf(ownerId: string): string[] {
  return graph.edges
    .filter((edge) => edge.kind === "resource_publish" && edge.from === ownerId)
    .map((edge) => edge.to.replace("resource:channel:", ""))
    .sort();
}

function locksOf(ownerId: string): string[] {
  return graph.edges
    .filter((edge) => edge.kind === "resource_lock" && edge.from === ownerId)
    .map((edge) => edge.to.replace("resource:attribute:", ""))
    .sort();
}

function toolCallRecord(effect: ToolEffect): AgentToolCallRecord {
  return {
    toolCallId: "call",
    turnId: "turn",
    toolName: "tool",
    arguments: "{}",
    effect,
    requiresApproval: false,
    status: "approved",
    deferralOrder: 0,
    inputAnswers: [],
    inputChoices: [],
    inputAllowsFreeText: true,
    taskAnswerPending: false,
  } as unknown as AgentToolCallRecord;
}

describe("the session Flow Definition Graph", () => {
  test("the package ships the suite's graph byte for byte", () => {
    const shipped = fileURLToPath(new URL("../../../src/agent-session-flow-graph.json", import.meta.url));
    assert.equal(readFileSync(shipped, "utf8"), readFileSync(`${conformanceSuiteDirectory}session_graph.json`, "utf8"));
  });

  test("the rendered text matches every core's", () => {
    for (const renderingCase of renderings.cases) {
      const text = renderSessionFlowGraph({
        flowType: renderingCase.flow_type,
        actionPermission: renderingCase.action_permission,
      });
      assert.equal(createHash("sha256").update(text).digest("hex"), renderingCase.sha256, renderingCase.flow_type);
    }
  });

  test("the Flow definition document carries the rendered graph and the template's time", () => {
    for (const renderingCase of renderings.cases) {
      const options = { flowType: renderingCase.flow_type, actionPermission: renderingCase.action_permission };
      const document = sessionFlowDefinitionDocument(options);
      assert.equal(document.flowType, renderingCase.flow_type);
      assert.equal(document.content, renderSessionFlowGraph(options));
      assert.equal(createHash("sha256").update(document.content).digest("hex"), renderingCase.sha256);
      assert.equal(document.generatedAt.toISOString(), renderings.generated_at);
    }
    const template = readFileSync(`${conformanceSuiteDirectory}session_graph.json`, "utf8");
    assert.equal(createHash("sha256").update(template).digest("hex"), renderings.template_sha256);
    const first = sessionFlowDefinitionDocument({ flowType: "A", actionPermission: "a" });
    first.generatedAt.setTime(0);
    assert.equal(
      sessionFlowDefinitionDocument({ flowType: "A", actionPermission: "a" }).generatedAt.toISOString(),
      renderings.generated_at,
    );
    assert.throws(() => sessionFlowDefinitionDocument({ flowType: "A", actionPermission: "Bad Permission" }), TypeError);
  });

  test("the renderer fills in the Flow type and the permission, and rejects bad ones", () => {
    const rendered = JSON.parse(
      renderSessionFlowGraph({ flowType: "SupportAgent", actionPermission: "support-agent.operate" }),
    ) as SessionGraph;
    assert.equal(rendered.flow.name, "SupportAgent");
    assert.deepEqual(
      rendered.v2.actions.map((action) => [action.rpcName, action.requiredPermission, action.condition]),
      ["resolve_interrupt", "answer_question", "steer_user_message", "cancel_turn"].map((name) => [
        name,
        "support-agent.operate",
        undefined,
      ]),
    );
    assert.throws(() => renderSessionFlowGraph({ flowType: " ", actionPermission: "a" }), TypeError);
    assert.throws(() => renderSessionFlowGraph({ flowType: "A", actionPermission: "Bad Permission" }), TypeError);
  });

  test("the command writes the same text and refuses bad arguments", () => {
    const command = fileURLToPath(new URL("../../src/agent-session-flow-graph-cli.js", import.meta.url));
    const options = ["--flow-type", "SupportAgent", "--permission", "support-agent.operate"];
    const run = spawnSync(process.execPath, [command, ...options], { encoding: "utf8" });
    assert.equal(run.status, 0, run.stderr);
    assert.equal(
      run.stdout,
      renderSessionFlowGraph({ flowType: "SupportAgent", actionPermission: "support-agent.operate" }),
    );
    assert.equal(spawnSync(process.execPath, [command], { encoding: "utf8" }).status, 2);
    const invalid = ["--flow-type", "SupportAgent", "--permission", "Bad Permission"];
    assert.equal(spawnSync(process.execPath, [command, ...invalid], { encoding: "utf8" }).status, 2);
  });

  test("Steps match the registered Flow in order, start, phase, and locks", () => {
    assert.deepEqual(
      nodesOf("step").map((node) => node.name),
      steps.map((definition) => definition.step.getStepType()),
    );
    for (const definition of steps) {
      const stepType = definition.step.getStepType();
      const node = nodesOf("step").find((candidate) => candidate.name === stepType);
      assert.ok(node);
      assert.equal(node.start === true, definition.isStartStep, stepType);
      const hasWaitFor = source.hasWaitFor(classNameOf(definition.step));
      assert.equal(node.phase, hasWaitFor ? "wait_for+execute" : "execute", stepType);
      const locks = (definition.step.getStepOptions?.()?.executeLockAttributes ?? []).map((lock) => lock.attribute.name);
      assert.deepEqual(locksOf(`step:${stepType}`), locks.sort(), stepType);
    }
  });

  test("RPCs match the registered Flow in name, locks, and publishes", () => {
    assert.deepEqual(
      nodesOf("rpc").map((node) => node.name),
      rpcs.map((rpc) => rpc.name).sort(),
    );
    for (const rpc of rpcs) {
      assert.deepEqual(
        locksOf(`rpc:${rpc.name}`),
        (rpc.options.lockAttributes ?? []).map((lock) => lock.attribute.name).sort(),
        rpc.name,
      );
      const methodName = rpc.name.replace(/_(\w)/g, (_, letter: string) => letter.toUpperCase());
      assert.deepEqual(
        publishesOf(`rpc:${rpc.name}`),
        [...source.scanPublishes("AgentSessionFlowDefinition", methodName)].map(channelNameOf).sort(),
        rpc.name,
      );
    }
  });

  test("persistence names match the registered Flow", () => {
    const schema = flow.getPersistenceSchema?.() ?? {};
    const registered = [...(schema.attributes ?? []), ...(schema.channels ?? []), ...(schema.streams ?? [])];
    assert.deepEqual(
      graph.nodes
        .filter((node) => node.resource !== undefined)
        .map((node) => node.name)
        .sort(),
      registered.map((definition) => definition.name).sort(),
    );
  });

  test("wait conditions match the source of every waiting Step", () => {
    for (const definition of steps) {
      const stepType = definition.step.getStepType();
      const className = classNameOf(definition.step);
      const node = nodesOf("wait").find((candidate) => candidate.parentId === `step:${stepType}`);
      assert.equal(node !== undefined, source.hasWaitFor(className), stepType);
      if (node === undefined) {
        continue;
      }
      const scanned = source.scanWait(className);
      assert.deepEqual(
        node.wait?.conditions.map((condition) => condition.label).sort(),
        [...scanned.conditions].map((name) => (name === "Timer" ? name : channelNameOf(name))).sort(),
        stepType,
      );
      assert.ok(scanned.waitTypes.has(node.wait?.type ?? ""), `${stepType} waits with ${node.wait?.type}`);
    }
  });

  test("transitions, endings, and publishes match the source of every Step", () => {
    for (const definition of steps) {
      const stepType = definition.step.getStepType();
      const className = classNameOf(definition.step);
      const scanned = source.scanExecute(className);
      const expected = transitionsFrom(`step:${stepType}`);
      assert.deepEqual(
        [...scanned.targets].map((key) => stepTypeOfKey[key] ?? key).sort(),
        expected.targets,
        `${stepType} targets`,
      );
      assert.deepEqual([...scanned.endings].sort(), expected.endings, `${stepType} endings`);
      assert.deepEqual(
        [...source.scanPublishes(className, "execute")].map(channelNameOf).sort(),
        publishesOf(`step:${stepType}`),
        `${stepType} publishes`,
      );
    }
  });

  test("failure routes match every Step's options and movements", () => {
    const routes = new Set<string>();
    for (const definition of steps) {
      const target = failureTargetOf(definition.step.getStepOptions?.());
      if (target !== undefined) {
        routes.add(`${definition.step.getStepType()}>${target}`);
      }
    }
    for (const effect of ["read", "idempotent_write", "non_idempotent_write", "unclassified"] as const) {
      const movement = toolTaskPollMovement(persistence, toolCallRecord(effect));
      routes.add(`PollToolTask>${failureTargetOf(movement.options)}`);
    }
    for (const stepType of [agentSessionStepTypes.callTool, agentSessionStepTypes.callSingleAttemptTool]) {
      routes.add(`${stepType}>${failureTargetOf(toolStepOptions(persistence, stepType, "call"))}`);
    }
    assert.deepEqual(
      graph.edges
        .filter((edge) => edge.kind === "failure_transition")
        .map((edge) => `${edge.from.replace("step:", "")}>${edge.to.replace("step:", "")}`)
        .sort(),
      [...routes].sort(),
    );
  });

  test("cancel_turn starts and cancels the Steps the graph names", () => {
    const decision = graph.nodes.find((node) => node.id === "decision:rpc:cancel_turn:rpcResult");
    assert.deepEqual(
      decision?.decision?.cancellations?.map((cancellation) => cancellation.stepId.replace("step:", "")).sort(),
      [...source.scanCancelingSteps()].map((key) => stepTypeOfKey[key] ?? key).sort(),
    );
    assert.deepEqual(transitionsFrom("rpc:cancel_turn").targets, ["FinishTurnCancellation"]);
  });

  test("Action inputs match the fields each RPC's codec writes", () => {
    const fullInputs: Readonly<Record<string, unknown>> = {
      resolve_interrupt: { interruptId: "a", approved: true, rejectionMessage: null },
      answer_question: { interruptId: "a", answer: "b" },
      steer_user_message: { messageId: null, text: "b", senderName: "c" },
      cancel_turn: { turnId: null },
    };
    for (const action of graph.v2.actions) {
      const codec = rpcs.find((rpc) => rpc.name === action.rpcName)?.options.inputCodec;
      assert.ok(codec, action.rpcName);
      const wire = codec.encode(fullInputs[action.rpcName]) as { data: string | Uint8Array };
      const text = typeof wire.data === "string" ? wire.data : Buffer.from(wire.data).toString("utf8");
      assert.deepEqual(
        Object.keys(JSON.parse(text) as object).sort(),
        action.input.fields.map((field) => field.fieldName).sort(),
        action.rpcName,
      );
    }
  });
});
