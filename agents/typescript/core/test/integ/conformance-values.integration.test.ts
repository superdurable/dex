// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import {
  Attribute,
  AttributeMap,
  Channel,
  ChannelMap,
  Stream,
  booleanCodec,
  jsonCodec,
  stringCodec,
  voidCodec,
  type Codec,
  type Flow,
  type RPCOptions,
} from "@superdurable/dex";

import {
  buildDurableWaitCompletedOutput,
  buildPlanClearedOutput,
  buildPlanUpdatedOutput,
  buildQuestionAnsweredOutput,
  builtInToolDefinitions,
  validateAskUserArguments,
  validateDurableWaitArguments,
  validateWriteTodosArguments,
  writeTodosCalledMoreThanOnce,
} from "../../src/agent-built-in-tools.js";
import {
  buildPlanExecutionMessage,
  buildPlanNote,
  noPlanNote,
  planningTurnInstruction,
  planningTurnToolRejection,
} from "../../src/agent-plans.js";
import {
  decodeAgentImageReference,
  decodeAgentInterrupt,
  decodeAgentMessage,
  decodeAgentPlan,
  decodeAgentPlanTask,
  decodeAgentToolCall,
  decodeCompactContextInput,
  decodeContextSummary,
  decodeListAgentMessagesInput,
  decodePendingUserMessage,
  decodeQuestionAnswer,
  encodeAgentImageReference,
  encodeAgentInterrupt,
  encodeAgentPlanTask,
  encodeCompactContextInput,
  encodePendingUserMessage,
  encodeQuestionAnswer,
} from "../../src/agent-session-codecs.js";
import {
  defaultMaxDurableWaitMs,
  defaultMessageChunkMaxBytes,
  defaultMessageChunkSize,
  defaultMessageWindowSize,
  defaultRetainedMessageLimit,
  messagePageLimit,
  messageRolloverMaxBytesPerCommit,
  messageRolloverMaxChunksPerCommit,
  snapshotMessageLimit,
} from "../../src/agent-session-flow.js";
import { defaultAgentEventsCapacityBytes, messageSequenceKey, planExecutionKey } from "../../src/agent-session-persistence.js";
import {
  MAX_SESSION_SETTINGS_BYTES,
  SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
  checkStoredSessionSettings,
  sessionSettingsSize,
  validateSessionSettings,
} from "../../src/agent-session-settings.js";
import { questionInterruptId } from "../../src/agent-session-steps.js";
import { allPendingMessages } from "../../src/agent-session-transitions.js";
import {
  approvalDeadlinePassed,
  blankToolTaskId,
  buildToolCallNote,
  buildTurnFailureNote,
  defaultQuestionDeclinedToolResult,
  defaultRejectionToolResult,
  describeToolCallOutcome,
  invalidToolTaskPollInterval,
  toolCallNotRunErrorPrefix,
  toolTaskAbandonedError,
  isFinalToolCallStatus,
  outcomeUnknownToolResult,
  questionDeadlinePassed,
  toolCallCancelledBySteeringResult,
  toolCallCancelledByTurnCancellationResult,
  turnCancelledNoteHeader,
  turnFailureNoteFooter,
  turnFailureNoteHeader,
  turnSteeredNoteHeader,
  type AgentEventType,
  type AgentInterruptKind,
  type AgentMessageRole,
  type AgentSessionStatus,
  type AgentToolCallRecordStatus,
  type InteractionMode,
  type PlanStatus,
  type PlanTaskStatus,
  type QuestionResolution,
  type ToolCallCancellationReason,
  type ToolEffect,
  type TurnControlKind,
} from "../../src/agent-session-values.js";
import { modelCallRefusedErrorPrefix } from "../../src/agent-session-spend-limit.js";
import { buildContextSummaryPrompt, contextSummaryTranscriptHeader } from "../../src/agent-context-compaction.js";
import type { AgentEventWriter } from "../../src/agent-framework-runner.js";
import type { BuiltInTool } from "../../src/agent-built-in-tools.js";
import {
  buildContextSummaryNote,
  buildContextSummaryOmissionNote,
  buildToolCallLimitReachedResult,
  buildUnknownToolError,
  contextSummaryInstructions,
  contextSummaryNoteHeader,
  contextSummaryRequest,
  defaultMaxModelCallsPerTurn,
  readToolSourceEnvelope,
  writeToolSourceEnvelope,
} from "../../src/stepwise/stepwise-agent-runner.js";
import {
  buildResourceAndPromptToolDefinitions,
  readServerName,
  runResourceAndPromptTool,
  serializeToolOutputJson,
} from "../../src/mcp/mcp-resource-and-prompt-tools.js";
import { renderToolResultText } from "../../src/mcp/mcp-tool-source.js";
import { createStepwiseAgentSessionFlow } from "../../src/stepwise/index.js";
import { ScriptedModelClient } from "../../src/testing/index.js";
import {
  readConformanceJson,
  readGoldenValueDocuments,
  serializeCanonicalJson,
  type GoldenValueDocument,
} from "../fixtures/conformance-suite.js";

interface NamedType {
  readonly name: string;
  readonly input?: string | null;
  readonly output?: string;
  readonly value_type?: string;
}

interface NamesManifest {
  readonly step_types: readonly (NamedType & { readonly input: string; readonly is_start_step: boolean })[];
  readonly rpcs: readonly (NamedType & {
    readonly output: string;
    readonly is_transactional: boolean;
    readonly locks: readonly string[];
    readonly load_attribute_maps: readonly string[];
    readonly load_channels: readonly string[];
  })[];
  readonly persistence: readonly (NamedType & { readonly kind: string; readonly value_type: string })[];
  readonly agent_events_default_capacity_bytes: number;
  readonly event_types: readonly string[];
  readonly enums: Readonly<Record<string, readonly string[]>>;
  readonly final_tool_call_statuses: readonly string[];
  readonly limits: Readonly<Record<string, number>>;
  readonly mcp_resource_and_prompt_tool_names: readonly string[];
  readonly session_settings_sizes: {
    readonly examples: readonly {
      readonly name: string;
      readonly settings: Readonly<Record<string, unknown>>;
      readonly bytes: number;
      readonly bytes_by_core?: Readonly<Record<string, number>>;
    }[];
    readonly limit: readonly {
      readonly name: string;
      readonly note_length: number;
      readonly bytes: number;
      readonly is_accepted: boolean;
      readonly is_accepted_by_session: boolean;
    }[];
  };
  readonly stepwise_tool_source_reference: {
    readonly cases: readonly {
      readonly source_name: string;
      readonly reference: string | null;
      readonly written: readonly string[];
      readonly canonical: string;
    }[];
  };
  readonly keys: {
    readonly AgentMessages: { readonly examples: Readonly<Record<string, string>> };
    readonly AgentMessageChunks: { readonly examples: Readonly<Record<string, string>> };
    readonly PlanExecutions: { readonly examples: Readonly<Record<string, string>> };
  };
}

interface TextCase {
  readonly input: Readonly<Record<string, unknown>>;
  readonly text: string | null;
}

interface TextTable {
  readonly constants: Readonly<Record<string, string>>;
  readonly templates: Readonly<Record<string, readonly TextCase[]>>;
  readonly built_in_tool_definitions: readonly { readonly name: string; readonly description: string; readonly parameters_json: string }[];
  readonly built_in_tool_argument_errors: Readonly<Record<string, readonly { readonly arguments: string; readonly error: string | null }[]>>;
  readonly mcp_resource_and_prompt_tools: {
    readonly server_names: readonly string[];
    readonly definitions: readonly { readonly name: string; readonly description: string; readonly input_schema_json: string }[];
    readonly argument_errors: readonly {
      readonly tool_name: string;
      readonly arguments: Readonly<Record<string, unknown>>;
      readonly error: string;
    }[];
  };
}

// The SDK keeps registeredRPCs internal; importing the same module file shares its decorator registry.
interface RegisteredRPC {
  readonly name: string;
  readonly options: RPCOptions<unknown, unknown>;
  readonly hasInput: boolean;
}

const { registeredRPCs } = (await import(
  new URL("../../../node_modules/@superdurable/dex/dist/src/rpc.js", import.meta.url).href
)) as { registeredRPCs: (flow: Flow<unknown>) => readonly RegisteredRPC[] };

const names = readConformanceJson<NamesManifest>("names.json");
const texts = readConformanceJson<TextTable>("texts.json");
const documents = new Map(readGoldenValueDocuments().map((document) => [document.type, document]));

const flow = createStepwiseAgentSessionFlow(
  { name: "conformance-manifest", instructions: "Describe the session Flow.", model: new ScriptedModelClient(() => ({ text: "ok" })) },
  "ConformanceAgent",
);
const steps = [...flow.getSteps()];
const schema = flow.getPersistenceSchema?.() ?? {};
const persistence = new Map(
  [...(schema.attributes ?? []), ...(schema.channels ?? []), ...(schema.streams ?? [])].map((definition) => [
    definition.name,
    definition,
  ]),
);
const rpcs = registeredRPCs(flow as Flow<unknown>);

function codecOfFunctions<Value>(typeName: string, encode: (value: Value) => unknown, decode: (value: unknown) => Value): Codec<unknown> {
  return jsonCodec<Value>({ typeName, encode, decode }) as Codec<unknown>;
}

// Types that only appear inside other values have no definition of their own.
const embeddedTypeCodecs: ReadonlyMap<string, Codec<unknown>> = new Map<string, Codec<unknown>>([
  ["QuestionAnswer", codecOfFunctions("QuestionAnswer", encodeQuestionAnswer, decodeQuestionAnswer)],
  [
    "AgentImageReference",
    codecOfFunctions("AgentImageReference", encodeAgentImageReference, decodeAgentImageReference),
  ],
  ["AgentInterrupt", codecOfFunctions("AgentInterrupt", encodeAgentInterrupt, decodeAgentInterrupt)],
  ["PendingUserMessage", codecOfFunctions("PendingUserMessage", encodePendingUserMessage, decodePendingUserMessage)],
  ["AgentPlanTask", codecOfFunctions("AgentPlanTask", encodeAgentPlanTask, decodeAgentPlanTask)],
  ["CompactContextInput", codecOfFunctions("CompactContextInput", encodeCompactContextInput, decodeCompactContextInput)],
]);

function requireDocument(typeName: string): GoldenValueDocument {
  const document = documents.get(typeName);
  assert.ok(document, `agents/conformance/values has no ${typeName}.json`);
  return document;
}

function wireValue(document: GoldenValueDocument, text: string): { kind: string; data: unknown } {
  switch (document.wire_kind) {
    case "json":
    case "string":
      return { kind: document.wire_kind, data: text };
    case "bool":
      return { kind: "bool", data: text === "true" };
    case "int64":
      return { kind: "int64", data: BigInt(text) };
  }
}

/** Event payloads are JSON text the contract does not fix byte for byte, so they compare canonically too. */
function renderWireText(typeName: string, value: { kind: string; data: unknown }): string {
  if (value.kind !== "json") {
    return String(value.data);
  }
  const parsed = JSON.parse(String(value.data)) as unknown;
  if (typeName === "AgentEvent") {
    const event = parsed as Record<string, unknown>;
    event["payload"] = serializeCanonicalJson(JSON.parse(String(event["payload"])));
  }
  return serializeCanonicalJson(parsed);
}

/** Decodes every golden case of a type with a codec and asserts the canonical re-encoding. */
function assertCodecConforms(typeName: string, codec: Codec<unknown>, where: string): void {
  const document = requireDocument(typeName);
  assert.equal(codec.wireKind, document.wire_kind, `${where} wire kind`);
  for (const goldenCase of document.cases) {
    for (const text of [goldenCase.wire, goldenCase.canonical]) {
      const decoded = codec.decode(wireValue(document, text) as never);
      assert.equal(
        renderWireText(typeName, codec.encode(decoded) as { kind: string; data: unknown }),
        goldenCase.canonical,
        `${where}: ${typeName}/${goldenCase.name}`,
      );
    }
  }
}

function codecForType(typeName: string): Codec<unknown> {
  const embedded = embeddedTypeCodecs.get(typeName);
  if (embedded !== undefined) {
    return embedded;
  }
  const fromPersistence = names.persistence.find((entry) => entry.value_type === typeName);
  if (fromPersistence !== undefined) {
    return (persistence.get(fromPersistence.name) as { codec: Codec<unknown> }).codec;
  }
  const fromStep = names.step_types.find((entry) => entry.input === typeName);
  if (fromStep !== undefined) {
    return steps.find((definition) => definition.step.getStepType() === fromStep.name)?.step.inputCodec as Codec<unknown>;
  }
  const fromRpc = rpcs.find((entry) => entry.options.inputCodec !== undefined && names.rpcs.find((named) => named.name === entry.name)?.input === typeName);
  if (fromRpc !== undefined) {
    return fromRpc.options.inputCodec as Codec<unknown>;
  }
  const output = rpcs.find((entry) => names.rpcs.find((named) => named.name === entry.name)?.output === typeName);
  assert.ok(output?.options.outputCodec, `no TypeScript codec for ${typeName}`);
  return output.options.outputCodec as Codec<unknown>;
}

describe("the conformance suite's golden values", () => {
  test("every value type decodes and re-encodes to the canonical bytes", () => {
    for (const typeName of documents.keys()) {
      assertCodecConforms(typeName, codecForType(typeName), typeName);
    }
  });

  test("every Step type has the manifest's name and input codec", () => {
    assert.deepEqual(
      steps.map((definition) => ({ name: definition.step.getStepType(), is_start_step: definition.isStartStep })),
      names.step_types.map((entry) => ({ name: entry.name, is_start_step: entry.is_start_step })),
    );
    for (const entry of names.step_types) {
      const step = steps.find((definition) => definition.step.getStepType() === entry.name)?.step;
      assertCodecConforms(entry.input, (step?.inputCodec ?? jsonCodec()) as Codec<unknown>, `Step ${entry.name}`);
    }
  });

  test("every persistence name has the manifest's kind and value codec", () => {
    const kinds: readonly [string, new (...parameters: never[]) => unknown][] = [
      ["attribute_map", AttributeMap],
      ["channel_map", ChannelMap],
      ["attribute", Attribute],
      ["channel", Channel],
      ["stream", Stream],
    ];
    assert.deepEqual([...persistence.keys()].sort(), names.persistence.map((entry) => entry.name).sort());
    for (const entry of names.persistence) {
      const definition = persistence.get(entry.name) as { codec: Codec<unknown> };
      assert.equal(kinds.find(([, definitionClass]) => definition instanceof definitionClass)?.[0], entry.kind, entry.name);
      assertCodecConforms(entry.value_type, definition.codec, entry.name);
    }
    assert.equal(defaultAgentEventsCapacityBytes, names.agent_events_default_capacity_bytes);
  });

  test("every RPC has the manifest's name, codecs, locks, and loads", () => {
    assert.deepEqual(rpcs.map((entry) => entry.name).sort(), names.rpcs.map((entry) => entry.name).sort());
    for (const entry of names.rpcs) {
      const registered = rpcs.find((candidate) => candidate.name === entry.name);
      assert.ok(registered);
      const options = registered.options;
      assert.equal(registered.hasInput, entry.input !== null, `${entry.name} input`);
      if (entry.input !== null && entry.input !== undefined) {
        assertCodecConforms(entry.input, (options.inputCodec ?? jsonCodec()) as Codec<unknown>, `RPC ${entry.name} input`);
      }
      assertCodecConforms(entry.output, (options.outputCodec ?? jsonCodec()) as Codec<unknown>, `RPC ${entry.name} output`);
      assert.equal(options.isTransactional ?? false, entry.is_transactional, `${entry.name} transactional`);
      assert.deepEqual((options.lockAttributes ?? []).map((lock) => lock.attribute.name).sort(), entry.locks, `${entry.name} locks`);
      assert.deepEqual((options.loadAttributeMaps ?? []).map((map) => map.name).sort(), entry.load_attribute_maps, `${entry.name} maps`);
      assert.deepEqual((options.loadChannels ?? []).map((channel) => channel.name).sort(), entry.load_channels, `${entry.name} channels`);
    }
  });

  test("the scalar codecs match the SDK's", () => {
    assertCodecConforms("string", stringCodec as Codec<unknown>, "stringCodec");
    assertCodecConforms("bool", booleanCodec as Codec<unknown>, "booleanCodec");
    assertCodecConforms("null", voidCodec as Codec<unknown>, "voidCodec");
  });
});

describe("the conformance suite's names manifest", () => {
  // Each Record is exhaustive over its TypeScript type, so a missing or extra value fails to compile.
  const eventTypes: Record<AgentEventType, true> = {
    turn_started: true,
    turn_completed: true,
    turn_failed: true,
    turn_steered: true,
    turn_cancelled: true,
    text_delta: true,
    reasoning_delta: true,
    tool_call_started: true,
    tool_call_finished: true,
    tool_call_deferred: true,
    interrupt_raised: true,
    interrupt_resolved: true,
    usage_recorded: true,
    timer_started: true,
    plan_updated: true,
    plan_execution_started: true,
    tool_task_started: true,
    compaction_started: true,
    compaction_finished: true,
  };
  const enums: Readonly<Record<string, Readonly<Record<string, true>>>> = {
    AgentSessionStatus: {
      waiting_for_user_message: true,
      running_model: true,
      running_tools: true,
      compacting_context: true,
      failed: true,
    } satisfies Record<AgentSessionStatus, true>,
    MessageRole: { user: true, assistant: true, tool: true } satisfies Record<AgentMessageRole, true>,
    ToolEffect: {
      read: true,
      idempotent_write: true,
      non_idempotent_write: true,
      unclassified: true,
    } satisfies Record<ToolEffect, true>,
    ToolCallStatus: {
      awaiting_approval: true,
      approved: true,
      awaiting_answer: true,
      waiting_for_timer: true,
      waiting_for_task: true,
      rejected: true,
      succeeded: true,
      failed: true,
      outcome_unknown: true,
    } satisfies Record<AgentToolCallRecordStatus, true>,
    InterruptKind: { tool_approval: true, question: true } satisfies Record<AgentInterruptKind, true>,
    InteractionMode: { chat: true, planning: true, executing: true } satisfies Record<InteractionMode, true>,
    PlanStatus: { draft: true, active: true, completed: true, cleared: true } satisfies Record<PlanStatus, true>,
    PlanTaskStatus: { pending: true, in_progress: true, completed: true } satisfies Record<PlanTaskStatus, true>,
    QuestionResolution: { answered: true, declined: true, deadline_passed: true } satisfies Record<QuestionResolution, true>,
    ToolCallCancellationReason: { steered: true, turn_cancelled: true } satisfies Record<ToolCallCancellationReason, true>,
    TurnControlKind: { steer: true, cancel_turn: true } satisfies Record<TurnControlKind, true>,
    BuiltInTool: { ask_user: true, durable_wait: true, write_todos: true } satisfies Record<BuiltInTool, true>,
  };

  test("event types and enum values match the TypeScript types", () => {
    assert.deepEqual(Object.keys(eventTypes).sort(), [...names.event_types].sort());
    assert.deepEqual(Object.keys(enums).sort(), Object.keys(names.enums).sort());
    for (const [enumName, values] of Object.entries(enums)) {
      assert.deepEqual(Object.keys(values).sort(), [...(names.enums[enumName] ?? [])].sort(), enumName);
    }
    assert.deepEqual(
      builtInToolDefinitions.map((definition) => definition.name),
      names.enums["BuiltInTool"],
    );
  });

  test("session settings sizes and the limit match the TypeScript core", () => {
    for (const example of names.session_settings_sizes.examples) {
      const expected = example.bytes_by_core?.["typescript"] ?? example.bytes;
      assert.equal(sessionSettingsSize(example.settings), expected, example.name);
    }
    for (const limitCase of names.session_settings_sizes.limit) {
      const settings = { note: "x".repeat(limitCase.note_length) };
      assert.equal(sessionSettingsSize(settings), limitCase.bytes, limitCase.name);
      for (const [check, isAccepted] of [
        [validateSessionSettings, limitCase.is_accepted],
        [checkStoredSessionSettings, limitCase.is_accepted_by_session],
      ] as const) {
        if (isAccepted) {
          check(settings);
        } else {
          assert.throws(() => check(settings), RangeError, limitCase.name);
        }
      }
    }
  });

  test("limits and final statuses match the TypeScript constants", () => {
    assert.deepEqual(names.limits, {
      snapshot_message_limit: snapshotMessageLimit,
      list_agent_messages_default_limit: decodeListAgentMessagesInput({ before_sequence: null }).limit,
      list_agent_messages_maximum_limit: messagePageLimit,
      default_max_durable_wait_seconds: defaultMaxDurableWaitMs / 1_000,
      turn_control_at_most: allPendingMessages,
      stepwise_default_max_model_calls_per_turn: defaultMaxModelCallsPerTurn,
      max_session_settings_bytes: MAX_SESSION_SETTINGS_BYTES,
      session_settings_size_tolerance_bytes: SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
      default_message_window_size: defaultMessageWindowSize,
      default_message_chunk_size: defaultMessageChunkSize,
      default_message_chunk_max_bytes: defaultMessageChunkMaxBytes,
      message_rollover_max_chunks_per_commit: messageRolloverMaxChunksPerCommit,
      message_rollover_max_bytes_per_commit: messageRolloverMaxBytesPerCommit,
      default_retained_message_limit: defaultRetainedMessageLimit,
    });
    assert.deepEqual(
      Object.keys(enums["ToolCallStatus"] ?? {}).filter((status) =>
        isFinalToolCallStatus(status as AgentToolCallRecordStatus),
      ).sort(),
      [...names.final_tool_call_statuses].sort(),
    );
  });

  test("map instance keys and interrupt IDs follow the manifest's formats", () => {
    for (const [sequence, key] of Object.entries(names.keys.AgentMessages.examples)) {
      assert.equal(messageSequenceKey(Number(sequence)), key);
    }
    for (const [sequence, key] of Object.entries(names.keys.AgentMessageChunks.examples)) {
      assert.equal(messageSequenceKey(Number(sequence)), key);
    }
    for (const [revision, key] of Object.entries(names.keys.PlanExecutions.examples)) {
      assert.equal(planExecutionKey(Number(revision)), key);
    }
    const toolCall = decodeAgentToolCall(JSON.parse(requireDocument("AgentToolCall").cases[0]?.canonical ?? "null"));
    assert.equal(questionInterruptId({ ...toolCall, inputQuestion: null }), toolCall.toolCallId);
    assert.equal(questionInterruptId({ ...toolCall, inputQuestion: "Which?", inputAnswers: [] }), `${toolCall.toolCallId}-input-1`);
  });
});

describe("the conformance suite's tool source and MCP additions", () => {
  test("every core's tool source envelope reads back, and this core's canonicalizes", () => {
    for (const envelopeCase of names.stepwise_tool_source_reference.cases) {
      for (const written of envelopeCase.written) {
        assert.deepEqual(readToolSourceEnvelope(written), {
          sourceName: envelopeCase.source_name,
          reference: envelopeCase.reference ?? undefined,
        });
      }
      const own = writeToolSourceEnvelope(envelopeCase.source_name, envelopeCase.reference ?? undefined);
      assert.equal(serializeCanonicalJson(JSON.parse(own)), envelopeCase.canonical);
    }
  });

  test("MCP resource and prompt tools have the same names, definitions, and argument errors", async () => {
    const mcpTools = texts.mcp_resource_and_prompt_tools;
    const definitions = buildResourceAndPromptToolDefinitions(mcpTools.server_names);
    assert.deepEqual(
      definitions.map((definition) => definition.name),
      names.mcp_resource_and_prompt_tool_names,
    );
    assert.deepEqual(
      definitions.map((definition) => ({
        name: definition.name,
        description: definition.description,
        input_schema_json: JSON.stringify(definition.inputSchema),
      })),
      mcpTools.definitions,
    );
    for (const errorCase of mcpTools.argument_errors) {
      // Every case fails before the client is used.
      await assert.rejects(
        async () => {
          readServerName(errorCase.arguments);
          await runResourceAndPromptTool(undefined as never, errorCase.tool_name, errorCase.arguments, { timeout: 1_000 });
        },
        { message: errorCase.error },
        `${errorCase.tool_name} ${JSON.stringify(errorCase.arguments)}`,
      );
    }
  });
});

describe("the conformance suite's text table", () => {
  test("every constant matches", () => {
    const constants: Readonly<Record<string, string>> = {
      outcome_unknown_tool_result: outcomeUnknownToolResult,
      default_rejection_tool_result: defaultRejectionToolResult,
      approval_deadline_passed: approvalDeadlinePassed,
      question_deadline_passed: questionDeadlinePassed,
      question_declined_tool_result: defaultQuestionDeclinedToolResult,
      turn_failure_note_header: turnFailureNoteHeader,
      turn_failure_note_footer: turnFailureNoteFooter,
      turn_steered_note_header: turnSteeredNoteHeader,
      turn_cancelled_note_header: turnCancelledNoteHeader,
      tool_call_cancelled_by_steering_result: toolCallCancelledBySteeringResult,
      tool_call_cancelled_by_turn_cancellation_result: toolCallCancelledByTurnCancellationResult,
      write_todos_called_more_than_once: writeTodosCalledMoreThanOnce,
      planning_turn_tool_rejection: planningTurnToolRejection,
      no_plan_note: noPlanNote,
      planning_turn_instruction: planningTurnInstruction,
      blank_tool_task_id: blankToolTaskId,
      invalid_tool_task_poll_interval: invalidToolTaskPollInterval,
      tool_task_abandoned_error: toolTaskAbandonedError,
      tool_call_not_run_error_prefix: toolCallNotRunErrorPrefix,
      model_call_refused_error_prefix: modelCallRefusedErrorPrefix,
      stepwise_context_summary_note_header: contextSummaryNoteHeader,
      stepwise_context_summary_instructions: contextSummaryInstructions,
      stepwise_context_summary_request: contextSummaryRequest,
      adapter_context_summary_transcript_header: contextSummaryTranscriptHeader,
    };
    assert.deepEqual(constants, texts.constants);
  });

  test("every template renders the same text", () => {
    const records = (input: Readonly<Record<string, unknown>>) => (input["records"] as unknown[]).map(decodeAgentToolCall);
    const renderers: Readonly<Record<string, (input: Readonly<Record<string, unknown>>) => string | null>> = {
      tool_call_outcome: (input) => describeToolCallOutcome(decodeAgentToolCall(input["record"])),
      turn_failure_note: (input) => buildTurnFailureNote(records(input)),
      turn_steered_note: (input) => buildToolCallNote(turnSteeredNoteHeader, records(input)),
      turn_cancelled_note: (input) => buildToolCallNote(turnCancelledNoteHeader, records(input)),
      plan_execution_message: (input) => buildPlanExecutionMessage(input["revision"] as number),
      plan_note: (input) =>
        buildPlanNote(input["plan"] === null ? null : decodeAgentPlan(input["plan"]), input["interaction_mode"] as InteractionMode) ??
        null,
      question_answered_output: (input) => buildQuestionAnsweredOutput(input["answer"] as string),
      durable_wait_completed_output: (input) =>
        buildDurableWaitCompletedOutput(input["waited_seconds"] as number, input["reason"] as string),
      plan_updated_output: (input) => buildPlanUpdatedOutput(input["revision"] as number, input["task_count"] as number),
      plan_cleared_output: (input) => buildPlanClearedOutput(input["revision"] as number),
      stepwise_tool_call_limit_reached_result: (input) => buildToolCallLimitReachedResult(input["max_model_calls_per_turn"] as number),
      stepwise_context_summary_omission_note: (input) => buildContextSummaryOmissionNote(input["omitted_count"] as number),
      stepwise_context_summary_note: (input) => buildContextSummaryNote(decodeContextSummary(input["summary"])),
      adapter_context_summary_prompt: (input) =>
        buildContextSummaryPrompt({
          flowId: "session-1",
          turnId: "turn-4",
          attempt: 1,
          stepExecutionId: "CompactContext-1",
          previousSummary: input["previous_summary"] === null ? null : decodeContextSummary(input["previous_summary"]),
          messages: (input["messages"] as unknown[]).map(decodeAgentMessage),
          throughSequence: 0,
          // The prompt builder writes no events.
          events: {} as AgentEventWriter,
          cancellationSignal: new AbortController().signal,
          deadline: undefined,
          ...(input["input_token_budget"] === null ? {} : { inputTokenBudget: input["input_token_budget"] as number }),
          heartbeat: async () => undefined,
        }),
      stepwise_unknown_tool_error: (input) => buildUnknownToolError(input["tool_name"] as string),
      mcp_tool_output_json: (input) => serializeToolOutputJson(JSON.parse(input["value"] as string)),
      mcp_tool_result_text: (input) => renderToolResultText(input["result"] as Parameters<typeof renderToolResultText>[0]),
    };
    assert.deepEqual(Object.keys(renderers).sort(), Object.keys(texts.templates).sort());
    for (const [name, cases] of Object.entries(texts.templates)) {
      for (const [index, textCase] of cases.entries()) {
        assert.equal(renderers[name]?.(textCase.input), textCase.text, `${name}[${index}]`);
      }
    }
  });

  test("built-in tool definitions and argument errors match", () => {
    assert.deepEqual(
      builtInToolDefinitions.map((definition) => ({
        name: definition.name,
        description: definition.description,
        parameters_json: JSON.stringify(definition.parameters),
      })),
      texts.built_in_tool_definitions,
    );
    const maxDurableWaitSeconds = names.limits["default_max_durable_wait_seconds"] ?? 0;
    const validators: Readonly<Record<string, (argumentsText: string) => { isValid: boolean; error?: string }>> = {
      ask_user: validateAskUserArguments,
      durable_wait: (argumentsText) => validateDurableWaitArguments(argumentsText, maxDurableWaitSeconds),
      write_todos: validateWriteTodosArguments,
    };
    for (const [tool, cases] of Object.entries(texts.built_in_tool_argument_errors)) {
      for (const errorCase of cases) {
        const validation = validators[tool]?.(errorCase.arguments);
        assert.equal(validation?.isValid ? null : validation?.error, errorCase.error, `${tool} ${errorCase.arguments}`);
      }
    }
  });
});
