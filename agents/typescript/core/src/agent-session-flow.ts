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
  agentMessageKey,
  createAgentSessionPersistence,
  defaultAgentEventsCapacityBytes,
  type AgentSessionPersistence,
} from "./agent-session-persistence.js";
import {
  AwaitInterruptResolutionStep,
  AwaitUserMessageStep,
  RecordTurnFailureStep,
  RunAgentTurnStep,
  StartAgentSessionStep,
  ToolCallExecutionStep,
  ToolCallFailureRecordingStep,
} from "./agent-session-steps.js";
import {
  AgentSessionTransitions,
  agentSessionStepTypes,
  type AgentSessionConfig,
} from "./agent-session-transitions.js";
import {
  emptyAgentUsage,
  type AgentInterrupt,
  type AgentMessage,
  type AgentMessagesPage,
  type AgentSessionStartInput,
  type AgentSnapshot,
  type AgentState,
  type AgentToolCall,
  type AgentUsage,
  type AgentUserMessage,
  type ListAgentMessagesInput,
  type ResolveInterruptInput,
  type ToolEffect,
} from "./agent-session-values.js";

const snapshotMessageLimit = 200;
const toolEffects: readonly ToolEffect[] = [
  "read",
  "idempotent_write",
  "non_idempotent_write",
  "unclassified",
];

/** Options of one agent session Flow, passed to {@link createAgentSessionFlow}. */
export interface AgentSessionFlowOptions {
  /**
   * Flow type registered for this agent, such as `"TriageAgent"`. It must be unique within the
   * Registry. Changing it for open sessions orphans them, so treat it as a stable name.
   */
  readonly flowType: string;
  /**
   * Policies by tool name. A policy overrides framework metadata and {@link defaultToolEffect}.
   * Defaults to no policies.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /** Effect of tools without a policy or framework metadata. Defaults to `"unclassified"`. */
  readonly defaultToolEffect?: ToolEffect;
  /** Byte budget of the `AgentEvents` Stream, shared by every session of this Flow type. Defaults to 10 MiB. */
  readonly agentEventsCapacityBytes?: number;
  /**
   * Milliseconds after an interrupt is raised at which `AwaitInterruptResolution` rejects it with
   * `"approval deadline passed"`. Defaults to no deadline: approvals wait indefinitely.
   */
  readonly interruptDeadlineMs?: number;
}

/**
 * Durable agent session Flow: one Flow execution is one agent session, and its Flow ID is the session ID.
 *
 * Create one per agent with {@link createAgentSessionFlow}, register it in the process `Registry`, and
 * call its RPCs with `Client.invokeRPC`. The RPC names are `send_user_message`, `resolve_interrupt`,
 * `get_agent_snapshot`, and `list_agent_messages`, the same as the Python core.
 *
 * @example
 * ```ts
 * const flow = createAgentSessionFlow(runner, { flowType: "SupportAgent" });
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
export interface AgentSessionFlow extends Flow<AgentSessionStartInput> {
  /** Persistence definitions owned by this Flow, for Client reads from outside the Flow. */
  readonly persistence: AgentSessionPersistence;
  /**
   * RPC `send_user_message`: queues one user message.
   *
   * The session consumes queued messages in FIFO order whenever it is idle in `AwaitUserMessage`.
   *
   * @param context - RPC Context supplied by Dex.
   * @param message - Message to queue.
   * @returns `true` when queued; `false` when the text is blank.
   */
  sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean>;
  /**
   * RPC `resolve_interrupt`: approves or rejects one pending interrupt.
   *
   * It locks `PendingInterrupts`, so a call that races `AwaitInterruptResolution` can fail with
   * `RpcLockConflictError`; retry it. Resolving an interrupt twice is safe: the second call returns `false`.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Interrupt ID and decision.
   * @returns `true` when the interrupt was pending and is now resolved; `false` otherwise.
   */
  resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean>;
  /**
   * RPC `get_agent_snapshot`: returns the durable session view a UI rebuilds from.
   *
   * It is a read-only RPC, so it also reads a retained closed session.
   *
   * @param context - RPC Context supplied by Dex.
   * @returns State, the last 200 messages, pending interrupts, the active turn's tool calls, usage,
   * and the queued message count.
   */
  getAgentSnapshot(context: Context): RPCResult<AgentSnapshot>;
  /**
   * RPC `list_agent_messages`: pages through stored messages from newest to oldest.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Exclusive upper sequence bound, or `null`, and a page size clamped to 1..200.
   * @returns Up to `limit` messages below `beforeSequence` in ascending order, and the bound of the
   * next older page or `null`.
   */
  listAgentMessages(context: Context, input: ListAgentMessagesInput): RPCResult<AgentMessagesPage>;
}

/**
 * Creates the shared agent session Flow for one agent and its framework runner.
 *
 * Each call creates a distinct Flow with its own Step instances, persistence definitions, and RPC
 * methods, so one Registry can hold several agents under different Flow types. Adapter packages wrap
 * this function; applications usually call the adapter's factory instead.
 *
 * @example
 * ```ts
 * const triage = createAgentSessionFlow(triageRunner, {
 *   flowType: "TriageAgent",
 *   toolPolicies: { lookup_customer: { effect: "read" } },
 *   interruptDeadlineMs: 24 * 60 * 60 * 1000,
 * });
 * const billing = createAgentSessionFlow(billingRunner, { flowType: "BillingAgent" });
 * const registry = new Registry([triage, billing]);
 * ```
 * @param runner - Framework runner; the Flow keeps it for its lifetime.
 * @param options - Flow type and tool, Stream, and interrupt settings.
 * @returns A new Flow to register in exactly one Registry.
 * @throws {TypeError} When the Flow type is blank or an option is invalid.
 * @throws {RangeError} When a numeric option is not positive.
 */
export function createAgentSessionFlow(
  runner: AgentFrameworkRunner,
  options: AgentSessionFlowOptions,
): AgentSessionFlow {
  const config = resolveAgentSessionConfig(runner, options);
  const persistence = createAgentSessionPersistence(config.agentEventsCapacityBytes);
  const transitions = new AgentSessionTransitions(persistence, runner, config);
  const rpcHandlers = new AgentSessionRpcHandlers(persistence);

  // A class per Flow gives each Flow its own RPC methods, which the Dex Client identifies by function.
  class AgentSessionFlowDefinition implements AgentSessionFlow {
    private readonly steps: StepList<AgentSessionStartInput>;

    public constructor(public readonly persistence: AgentSessionPersistence) {
      this.steps = StepList.startStep<AgentSessionStartInput>(
        new StartAgentSessionStep(transitions),
      ).otherSteps(
        new AwaitUserMessageStep(transitions),
        new RunAgentTurnStep(transitions),
        new AwaitInterruptResolutionStep(transitions),
        new ToolCallExecutionStep(
          transitions,
          agentSessionStepTypes.executeToolCall,
          3,
          agentSessionStepTypes.recordToolFailure,
        ),
        new ToolCallExecutionStep(
          transitions,
          agentSessionStepTypes.executeSingleAttemptToolCall,
          1,
          agentSessionStepTypes.recordToolOutcomeUnknown,
        ),
        new ToolCallFailureRecordingStep(
          transitions,
          agentSessionStepTypes.recordToolFailure,
          "failed",
        ),
        new ToolCallFailureRecordingStep(
          transitions,
          agentSessionStepTypes.recordToolOutcomeUnknown,
          "outcome_unknown",
        ),
        new RecordTurnFailureStep(transitions),
      );
    }

    public getFlowType(): string {
      return config.flowType;
    }

    public getSteps(): StepList<AgentSessionStartInput> {
      return this.steps;
    }

    public getPersistenceSchema(): PersistenceSchema {
      return {
        attributes: [
          persistence.agentState,
          persistence.agentMessages,
          persistence.frameworkRunState,
          persistence.agentToolCalls,
          persistence.pendingInterrupts,
          persistence.agentUsage,
        ],
        channels: [persistence.queuedUserMessages, persistence.interruptResolutions],
        streams: [persistence.agentEvents],
      };
    }

    @rpc<AgentUserMessage, boolean>({
      name: "send_user_message",
      inputCodec: agentUserMessageCodec,
      outputCodec: booleanCodec,
    })
    public sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean> {
      return rpcHandlers.sendUserMessage(context, message);
    }

    @rpc<ResolveInterruptInput, boolean>({
      name: "resolve_interrupt",
      inputCodec: resolveInterruptInputCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.pendingInterrupts.lock()],
    })
    public resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean> {
      return rpcHandlers.resolveInterrupt(context, input);
    }

    @rpc<void, AgentSnapshot>({
      name: "get_agent_snapshot",
      outputCodec: agentSnapshotCodec,
      loadAttributeMaps: [persistence.agentMessages, persistence.agentToolCalls],
    })
    public getAgentSnapshot(context: Context): RPCResult<AgentSnapshot> {
      return rpcHandlers.getAgentSnapshot(context);
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
      return rpcHandlers.listAgentMessages(context, input);
    }
  }

  return new AgentSessionFlowDefinition(persistence);
}

class AgentSessionRpcHandlers {
  public constructor(private readonly persistence: AgentSessionPersistence) {}

  public sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean> {
    if (message.text.trim() === "") {
      return { output: false };
    }
    this.persistence.queuedUserMessages.publish(context, { text: message.text });
    return { output: true };
  }

  public resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean> {
    const pendingInterrupts = this.readPendingInterrupts(context);
    if (!pendingInterrupts.some((interrupt) => interrupt.interruptId === input.interruptId)) {
      return { output: false };
    }
    this.persistence.pendingInterrupts.set(
      context,
      pendingInterrupts.filter((interrupt) => interrupt.interruptId !== input.interruptId),
    );
    this.persistence.interruptResolutions.publish(context, input.interruptId, {
      approved: input.approved,
      rejectionMessage: input.rejectionMessage,
    });
    return { output: true };
  }

  public getAgentSnapshot(context: Context): RPCResult<AgentSnapshot> {
    const state = this.readAgentState(context);
    const messages = this.readMessagesBelow(context, null, snapshotMessageLimit);
    const toolCalls = this.persistence.agentToolCalls;
    const activeTurnToolCalls =
      state.activeTurnId === null
        ? []
        : toolCalls
            .getAllInstanceKeys(context)
            .map((key): AgentToolCall => toolCalls.get(context, key))
            .filter((toolCall) => toolCall.turnId === state.activeTurnId);
    return {
      output: {
        state,
        messages: messages.messages,
        pendingInterrupts: this.readPendingInterrupts(context),
        toolCalls: activeTurnToolCalls,
        usage: (this.persistence.agentUsage.get(context) as AgentUsage | undefined) ?? emptyAgentUsage(),
        queuedMessageCount: context.channelSize(this.persistence.queuedUserMessages),
      },
    };
  }

  public listAgentMessages(
    context: Context,
    input: ListAgentMessagesInput,
  ): RPCResult<AgentMessagesPage> {
    const limit = Math.min(snapshotMessageLimit, Math.max(1, Math.trunc(input.limit)));
    return { output: this.readMessagesBelow(context, input.beforeSequence, limit) };
  }

  private readMessagesBelow(
    context: Context,
    beforeSequence: number | null,
    limit: number,
  ): AgentMessagesPage {
    const messages = this.persistence.agentMessages;
    const upperKey = beforeSequence === null ? undefined : agentMessageKey(beforeSequence);
    const keys = messages
      .getAllInstanceKeys(context)
      .filter((key) => upperKey === undefined || key < upperKey);
    const pageKeys = keys.slice(Math.max(0, keys.length - limit));
    const page: AgentMessage[] = pageKeys.map((key) => messages.get(context, key));
    const firstMessage = page[0];
    return {
      messages: page,
      nextBeforeSequence:
        firstMessage !== undefined && pageKeys.length < keys.length ? firstMessage.sequence : null,
    };
  }

  private readAgentState(context: Context): AgentState {
    return (
      (this.persistence.agentState.get(context) as AgentState | undefined) ?? {
        status: "waiting_for_user_message",
        turnCount: 0,
        activeTurnId: null,
        nextMessageSequence: 1,
        executingToolCallId: null,
        lastTurnError: null,
      }
    );
  }

  private readPendingInterrupts(context: Context): readonly AgentInterrupt[] {
    return (this.persistence.pendingInterrupts.get(context) as readonly AgentInterrupt[] | undefined) ?? [];
  }
}

function resolveAgentSessionConfig(
  runner: AgentFrameworkRunner,
  options: AgentSessionFlowOptions,
): AgentSessionConfig {
  if (typeof runner?.runTurn !== "function" || typeof runner.buildUserMessage !== "function") {
    throw new TypeError("createAgentSessionFlow requires an AgentFrameworkRunner");
  }
  if (typeof options.flowType !== "string" || options.flowType.trim() === "") {
    throw new TypeError("AgentSessionFlowOptions.flowType must be a non-blank string");
  }
  const defaultToolEffect = options.defaultToolEffect ?? "unclassified";
  requireToolEffect(defaultToolEffect, "AgentSessionFlowOptions.defaultToolEffect");
  const toolPolicies = new Map<string, ToolPolicy>();
  for (const [toolName, policy] of Object.entries(options.toolPolicies ?? {})) {
    requireToolEffect(policy.effect, `tool policy ${toolName}`);
    toolPolicies.set(toolName, policy);
  }
  const agentEventsCapacityBytes =
    options.agentEventsCapacityBytes ?? defaultAgentEventsCapacityBytes;
  if (!Number.isSafeInteger(agentEventsCapacityBytes) || agentEventsCapacityBytes <= 0) {
    throw new RangeError("AgentSessionFlowOptions.agentEventsCapacityBytes must be a positive integer");
  }
  const interruptDeadlineMs = options.interruptDeadlineMs;
  if (
    interruptDeadlineMs !== undefined &&
    (!Number.isFinite(interruptDeadlineMs) || interruptDeadlineMs <= 0)
  ) {
    throw new RangeError("AgentSessionFlowOptions.interruptDeadlineMs must be positive");
  }
  return {
    flowType: options.flowType,
    toolPolicies,
    defaultToolEffect,
    agentEventsCapacityBytes,
    interruptDeadlineMs,
  };
}

function requireToolEffect(effect: ToolEffect, description: string): void {
  if (!toolEffects.includes(effect)) {
    throw new TypeError(`${description} has unknown tool effect ${String(effect)}`);
  }
}
