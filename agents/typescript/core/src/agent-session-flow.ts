// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  StepList,
  booleanCodec,
  rpc,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

import type { AgentFrameworkRunner, ToolPolicy } from "./agent-framework-runner.js";
import {
  agentMessagesPageCodec,
  agentSnapshotCodec,
  agentUserMessageCodec,
  listAgentMessagesInputCodec,
  resolveInterruptInputCodec,
} from "./agent-session-codecs.js";
import {
  createAgentSessionPersistence,
  defaultAgentEventsCapacityBytes,
  type AgentSessionPersistence,
} from "./agent-session-persistence.js";
import {
  AwaitToolApprovalStep,
  AwaitUserMessageStep,
  RecordTurnFailureStep,
  ResumeTurnStep,
  RunModelStep,
  StartAgentSessionStep,
  ToolCallFailureRecordingStep,
  ToolCallStep,
} from "./agent-session-steps.js";
import { AgentSessionTracer } from "./agent-session-tracer.js";
import {
  AgentSessionTransitions,
  agentSessionStepTypes,
  type AgentSessionConfig,
} from "./agent-session-transitions.js";
import type {
  AgentMessagesPage,
  AgentSessionStartInput,
  AgentSnapshot,
  AgentUserMessage,
  ListAgentMessagesInput,
  ResolveInterruptInput,
  ToolEffect,
} from "./agent-session-values.js";
import {
  genAISemanticConventionsSchemaUrls,
  type AgentTracingOptions,
  type ResolvedAgentTracingOptions,
} from "./agent-tracing-options.js";

const snapshotMessageLimit = 200;
const messagePageLimit = 200;
const toolEffects: readonly ToolEffect[] = [
  "read",
  "idempotent_write",
  "non_idempotent_write",
  "unclassified",
];

/**
 * Tool policies, live events, approval deadline, and tracing of one agent, passed to
 * {@link createAgentSessionFlow}.
 */
export interface AgentSessionOptions {
  /**
   * Policies by tool name. A policy overrides framework metadata and {@link defaultToolEffect}.
   * Defaults to none.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /**
   * Effect of a tool without a policy or framework metadata. Defaults to `"unclassified"`, so unknown
   * tools run once and need approval.
   */
  readonly defaultToolEffect?: ToolEffect;
  /**
   * Approximate byte budget of the `AgentEvents` Stream, shared by every session of the Flow type.
   * Defaults to 10 MiB.
   */
  readonly eventStreamCapacityBytes?: number;
  /**
   * Milliseconds an approval may stay pending before `AwaitToolApproval` rejects it with
   * `"approval deadline passed"`. Defaults to no deadline: approvals wait indefinitely.
   */
  readonly interruptDeadlineMs?: number;
  /**
   * OpenTelemetry GenAI span options. Defaults to spans through the global tracer provider when
   * `@opentelemetry/api` is installed, and nothing otherwise. See {@link AgentTracingOptions}.
   */
  readonly tracing?: AgentTracingOptions;
}

/** Options applied by an {@link AgentSessionFlow}, with every default filled in. */
export interface ResolvedAgentSessionOptions {
  /** Policies by tool name. */
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  /** Effect of a tool without a policy or framework metadata. */
  readonly defaultToolEffect: ToolEffect;
  /** Byte budget of the `AgentEvents` Stream. */
  readonly eventStreamCapacityBytes: number;
  /** Approval deadline in milliseconds, or `undefined` for none. */
  readonly interruptDeadlineMs: number | undefined;
  /** Tracing options. */
  readonly tracing: ResolvedAgentTracingOptions;
}

/**
 * Durable agent session Flow: one Flow execution is one agent session, and its Flow ID is the session ID.
 *
 * Create one per agent with {@link createAgentSessionFlow}, register it in the process `Registry`, and
 * call its RPCs with `Client.invokeRPC`. The RPC names are `send_user_message`, `resolve_interrupt`,
 * `get_agent_snapshot`, and `list_agent_messages`, the same as the Python core. The session never
 * completes on its own: it waits for the next user message after every turn. Stop it with
 * `Client.stopFlow`.
 *
 * The Flow's persistence definitions are properties of the Flow; pass them to `Client.getAttributes`
 * or the Stream reads.
 *
 * @example
 * ```ts
 * const flow = createAgentSessionFlow(runner, "SupportAgent");
 * const registry = new Registry([flow]);
 * // ... start a Worker and a Client with this registry ...
 * await client.startFlow(flow, sessionId, { initialMessage: null });
 * await client.invokeRPC(flow.sendUserMessage, sessionId, { text: "Where is order 42?" });
 * const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
 * for (const interrupt of snapshot.pendingInterrupts) {
 *   await client.invokeRPC(flow.resolveInterrupt, sessionId, {
 *     interruptId: interrupt.interruptId, approved: true, rejectionMessage: null });
 * }
 * ```
 */
export interface AgentSessionFlow extends Flow<AgentSessionStartInput>, AgentSessionPersistence {
  /** The framework runner passed to {@link createAgentSessionFlow}. */
  readonly runner: AgentFrameworkRunner;
  /** The options this Flow applies, with defaults filled in. */
  readonly options: ResolvedAgentSessionOptions;
  /**
   * RPC `send_user_message`: queues one user message.
   *
   * The session starts one turn per queued message, in order, after the current turn ends.
   * The RPC requires a running session.
   *
   * @param context - RPC Context supplied by Dex.
   * @param message - Message to queue.
   * @returns `true` when queued; `false` when the text is blank.
   */
  sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean>;
  /**
   * RPC `resolve_interrupt`: approves or rejects one pending interrupt.
   *
   * It removes the interrupt from `PendingInterrupts` and publishes the answer in one commit, so only
   * one call succeeds per interrupt. It locks `PendingInterrupts`, so a call that races
   * `AwaitToolApproval` can fail with `RpcLockConflictError`, which is safe to retry. The
   * rejection message is dropped when `approved` is `true`.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Interrupt ID and decision.
   * @returns `true` when the interrupt was pending and is now resolved; `false` when it is unknown,
   * already resolved, or past its deadline.
   */
  resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean>;
  /**
   * RPC `get_agent_snapshot`: returns the durable session view a UI renders.
   *
   * It reads committed state without locks, so it also serves a retained closed session. It loads the
   * whole `AgentMessages` map.
   *
   * @param context - RPC Context supplied by Dex.
   * @returns State, the last 200 messages, pending interrupts, the active turn's tool calls, usage,
   * and the queued message count. Before `StartAgentSession` commits, `state` is `null` and the lists
   * are empty.
   */
  getAgentSnapshot(context: Context): RPCResult<AgentSnapshot>;
  /**
   * RPC `list_agent_messages`: returns one page of stored messages, oldest first.
   *
   * Start with `beforeSequence` `null` for the newest page, then pass each page's
   * `nextBeforeSequence` to walk back. It also serves a retained closed session.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Exclusive upper sequence bound, or `null`, and a page size clamped to 1..200.
   * @returns Up to `limit` messages below `beforeSequence` in ascending order, and the bound of the
   * next older page, or `null` when the page starts at the first message.
   */
  listAgentMessages(context: Context, input: ListAgentMessagesInput): RPCResult<AgentMessagesPage>;
}

/**
 * Creates the shared agent session Flow for one agent and its framework runner.
 *
 * Construct it once per agent at application startup and pass the same object to the Registry and
 * every Client call. Each call creates a distinct Flow with its own Step instances, persistence
 * definitions, and RPC methods, so one Registry can hold several agents under different Flow types.
 * Adapter packages wrap this function; applications usually call the adapter's factory instead.
 *
 * It is a function rather than a class because the Dex Client identifies an RPC by its method
 * function, and two Flows of one class would share their RPC methods.
 *
 * @example
 * ```ts
 * const triage = createAgentSessionFlow(triageRunner, "TriageAgent", {
 *   toolPolicies: { lookup_customer: { effect: "read" } },
 *   interruptDeadlineMs: 24 * 60 * 60 * 1000,
 * });
 * const billing = createAgentSessionFlow(billingRunner, "BillingAgent");
 * const registry = new Registry([triage, billing]);
 * ```
 * @param runner - Framework runner; the Flow owns no other dependency.
 * @param flowType - Flow type, unique within the Registry, such as `"TriageAgent"`. Keep it stable:
 * running sessions resume by it.
 * @param options - Tool policies, Stream capacity, approval deadline, and tracing. Defaults to all
 * defaults.
 * @returns A new Flow to register in one Registry.
 * @throws {TypeError} When `flowType` is blank, the runner is incomplete, an effect is unknown, or a
 * tracing option is invalid.
 * @throws {RangeError} When `eventStreamCapacityBytes` or `interruptDeadlineMs` is not positive.
 */
export function createAgentSessionFlow(
  runner: AgentFrameworkRunner,
  flowType: string,
  options: AgentSessionOptions = {},
): AgentSessionFlow {
  const config = resolveAgentSessionConfig(runner, flowType, options);
  const persistence = createAgentSessionPersistence(config.eventStreamCapacityBytes);
  const transitions = new AgentSessionTransitions(
    persistence,
    runner,
    config,
    new AgentSessionTracer(config.tracing, persistence),
  );
  const resolvedOptions: ResolvedAgentSessionOptions = Object.freeze({
    toolPolicies: config.toolPolicies,
    defaultToolEffect: config.defaultToolEffect,
    eventStreamCapacityBytes: config.eventStreamCapacityBytes,
    interruptDeadlineMs: config.interruptDeadlineMs,
    tracing: config.tracing,
  });

  // A class per Flow gives each Flow its own RPC methods, which the Dex Client identifies by function.
  class AgentSessionFlowDefinition implements AgentSessionFlow {
    public readonly runner = runner;
    public readonly options = resolvedOptions;
    public readonly agentState = persistence.agentState;
    public readonly agentMessages = persistence.agentMessages;
    public readonly frameworkRunState = persistence.frameworkRunState;
    public readonly agentToolCalls = persistence.agentToolCalls;
    public readonly pendingInterrupts = persistence.pendingInterrupts;
    public readonly agentUsage = persistence.agentUsage;
    public readonly queuedUserMessages = persistence.queuedUserMessages;
    public readonly interruptResolutions = persistence.interruptResolutions;
    public readonly toolCallResults = persistence.toolCallResults;
    public readonly agentEvents = persistence.agentEvents;
    public readonly agentTraceContext = persistence.agentTraceContext;
    private readonly steps = StepList.startStep<AgentSessionStartInput>(
      new StartAgentSessionStep(transitions),
    ).otherSteps(
      new AwaitUserMessageStep(transitions),
      new RunModelStep(transitions),
      new AwaitToolApprovalStep(transitions),
      new ToolCallStep(transitions, agentSessionStepTypes.callTool),
      new ToolCallStep(transitions, agentSessionStepTypes.callSingleAttemptTool),
      new ToolCallFailureRecordingStep(transitions, agentSessionStepTypes.recordToolFailure, true),
      new ToolCallFailureRecordingStep(
        transitions,
        agentSessionStepTypes.recordToolOutcomeUnknown,
        false,
      ),
      new ResumeTurnStep(transitions),
      new RecordTurnFailureStep(transitions),
    );

    public getFlowType(): string {
      return config.flowType;
    }

    public getSteps(): StepList<AgentSessionStartInput> {
      return this.steps;
    }

    // AgentTraceContext is always registered, so Workers with and without tracing serve one Flow type.
    public getPersistenceSchema(): PersistenceSchema {
      return {
        attributes: [
          persistence.agentState,
          persistence.agentMessages,
          persistence.frameworkRunState,
          persistence.agentToolCalls,
          persistence.pendingInterrupts,
          persistence.agentUsage,
          persistence.agentTraceContext,
        ],
        channels: [
          persistence.queuedUserMessages,
          persistence.interruptResolutions,
          persistence.toolCallResults,
        ],
        streams: [persistence.agentEvents],
      };
    }

    @rpc<AgentUserMessage, boolean>({
      name: "send_user_message",
      inputCodec: agentUserMessageCodec,
      outputCodec: booleanCodec,
    })
    public sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean> {
      if (message.text.trim() === "") {
        return { output: false };
      }
      persistence.queuedUserMessages.publish(context, { text: message.text });
      return { output: true };
    }

    @rpc<ResolveInterruptInput, boolean>({
      name: "resolve_interrupt",
      inputCodec: resolveInterruptInputCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.pendingInterrupts.lock()],
    })
    public resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean> {
      const pendingInterrupts = transitions.readPendingInterrupts(context);
      const interrupt = pendingInterrupts.find((pending) => pending.interruptId === input.interruptId);
      // After the deadline, AwaitToolApproval rejects the call; a late answer must not win.
      if (
        interrupt === undefined ||
        (interrupt.deadline !== null && Date.parse(interrupt.deadline) <= Date.now())
      ) {
        return { output: false };
      }
      persistence.pendingInterrupts.set(
        context,
        pendingInterrupts.filter((interrupt) => interrupt.interruptId !== input.interruptId),
      );
      persistence.interruptResolutions.publish(context, input.interruptId, {
        approved: input.approved,
        rejectionMessage: input.approved ? null : input.rejectionMessage,
      });
      return { output: true };
    }

    @rpc<void, AgentSnapshot>({
      name: "get_agent_snapshot",
      outputCodec: agentSnapshotCodec,
      loadAttributeMaps: [persistence.agentMessages, persistence.agentToolCalls],
    })
    public getAgentSnapshot(context: Context): RPCResult<AgentSnapshot> {
      const state = transitions.readOptionalAgentState(context);
      if (state === null) {
        return {
          output: {
            state: null,
            messages: [],
            pendingInterrupts: [],
            toolCalls: [],
            usage: transitions.readAgentUsage(context),
            queuedMessageCount: 0,
          },
        };
      }
      const end = state.nextMessageSequence;
      return {
        output: {
          state,
          messages: transitions.readMessages(context, end - snapshotMessageLimit, end),
          pendingInterrupts: transitions.readPendingInterrupts(context),
          toolCalls: transitions.readTurnToolCalls(context, state.activeTurnId),
          usage: transitions.readAgentUsage(context),
          queuedMessageCount: context.channelSize(persistence.queuedUserMessages),
        },
      };
    }

    @rpc<ListAgentMessagesInput, AgentMessagesPage>({
      name: "list_agent_messages",
      inputCodec: listAgentMessagesInputCodec,
      outputCodec: agentMessagesPageCodec,
      loadAttributeMaps: [persistence.agentMessages],
    })
    public listAgentMessages(
      context: Context,
      input: ListAgentMessagesInput,
    ): RPCResult<AgentMessagesPage> {
      const state = transitions.readOptionalAgentState(context);
      if (state === null) {
        return { output: { messages: [], nextBeforeSequence: null } };
      }
      const limit = Math.max(1, Math.min(input.limit, messagePageLimit));
      let end = state.nextMessageSequence;
      if (input.beforeSequence !== null) {
        end = Math.max(1, Math.min(input.beforeSequence, end));
      }
      const start = Math.max(1, end - limit);
      return {
        output: {
          messages: transitions.readMessages(context, start, end),
          nextBeforeSequence: start > 1 ? start : null,
        },
      };
    }
  }

  return new AgentSessionFlowDefinition();
}

function resolveAgentSessionConfig(
  runner: AgentFrameworkRunner,
  flowType: string,
  options: AgentSessionOptions,
): AgentSessionConfig {
  if (
    typeof runner?.buildUserMessage !== "function" ||
    typeof runner.runModelStep !== "function" ||
    typeof runner.executeDeferredToolCall !== "function"
  ) {
    throw new TypeError("createAgentSessionFlow requires an AgentFrameworkRunner");
  }
  if (typeof flowType !== "string" || flowType.trim() === "") {
    throw new TypeError("flowType must be a non-blank string");
  }
  const defaultToolEffect = options.defaultToolEffect ?? "unclassified";
  requireToolEffect(defaultToolEffect, "defaultToolEffect");
  const toolPolicies: Record<string, ToolPolicy> = {};
  for (const [toolName, policy] of Object.entries(options.toolPolicies ?? {})) {
    requireToolEffect(policy.effect, `tool policy ${toolName}`);
    toolPolicies[toolName] = Object.freeze({ ...policy });
  }
  const eventStreamCapacityBytes =
    options.eventStreamCapacityBytes ?? defaultAgentEventsCapacityBytes;
  if (!Number.isSafeInteger(eventStreamCapacityBytes) || eventStreamCapacityBytes <= 0) {
    throw new RangeError("eventStreamCapacityBytes must be a positive integer");
  }
  const interruptDeadlineMs = options.interruptDeadlineMs;
  if (
    interruptDeadlineMs !== undefined &&
    (!Number.isFinite(interruptDeadlineMs) || interruptDeadlineMs <= 0)
  ) {
    throw new RangeError("interruptDeadlineMs must be positive");
  }
  return {
    flowType,
    toolPolicies: Object.freeze(toolPolicies),
    defaultToolEffect,
    eventStreamCapacityBytes,
    interruptDeadlineMs,
    tracing: resolveAgentTracingOptions(flowType, options.tracing ?? {}),
  };
}

function resolveAgentTracingOptions(
  flowType: string,
  tracing: AgentTracingOptions,
): ResolvedAgentTracingOptions {
  const agentName = tracing.agentName ?? flowType;
  if (typeof agentName !== "string" || agentName.trim() === "") {
    throw new TypeError("tracing.agentName must be a non-blank string");
  }
  const semanticConventionsVersion = tracing.semanticConventionsVersion ?? "1.42.0-dev";
  if (!Object.hasOwn(genAISemanticConventionsSchemaUrls, semanticConventionsVersion)) {
    throw new TypeError(`tracing.semanticConventionsVersion ${String(semanticConventionsVersion)} is unknown`);
  }
  const tracerProvider = tracing.tracerProvider;
  if (tracerProvider !== undefined && typeof tracerProvider?.getTracer !== "function") {
    throw new TypeError("tracing.tracerProvider must be an OpenTelemetry TracerProvider");
  }
  return Object.freeze({
    isEnabled: tracing.isEnabled ?? true,
    tracerProvider,
    agentName,
    shouldCaptureMessageContent: tracing.shouldCaptureMessageContent ?? false,
    semanticConventionsVersion,
  });
}

function requireToolEffect(effect: ToolEffect, description: string): void {
  if (!toolEffects.includes(effect)) {
    throw new TypeError(`${description} has unknown tool effect ${String(effect)}`);
  }
}
