// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Code written against the published @superdurable/dex-agents 0.1.0 API; `npm run typecheck` must accept it.
// The one documented break: snapshot and stored records are AgentToolCallRecord, not AgentToolCall.

import {
  ToolExecutionError,
  addAgentUsage,
  approvalDeadlinePassed,
  buildTurnFailureNote,
  createAgentSessionFlow,
  defaultAgentEventsCapacityBytes,
  defaultRejectionToolResult,
  describeToolCallOutcome,
  emptyAgentUsage,
  isFinalToolCallStatus,
  messageSequenceKey,
  outcomeUnknownToolResult,
  resolveToolPolicy,
  toolEffectFromMcpToolAnnotations,
  turnFailureNoteFooter,
  turnFailureNoteHeader,
  type AgentEvent,
  type AgentEventType,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentInterrupt,
  type AgentInterruptResolution,
  type AgentMessagesPage,
  type AgentSessionFlow,
  type AgentSessionOptions,
  type AgentSnapshot,
  type AgentToolCall,
  type AgentToolCallStatus,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ResolveInterruptInput,
  type ResolvedAgentSessionOptions,
  type ToolExecutionResult,
} from "../../src/index.js";
import { AgentSessionTestClient, type AgentTestEnvironment } from "../../src/testing/index.js";

const silentEvents: AgentEventWriter = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};

const approvedRecord: AgentToolCall = {
  toolCallId: "call-1",
  turnId: "turn-1",
  toolName: "send_email",
  arguments: "{}",
  effect: "unclassified",
  requiresApproval: true,
  status: "approved",
  output: null,
  error: null,
  rejectionMessage: null,
  deferralOrder: 0,
};

const interrupt: AgentInterrupt = {
  interruptId: "call-1",
  kind: "tool_approval",
  toolCallId: "call-1",
  toolName: "send_email",
  arguments: "{}",
  createdAt: new Date(0).toISOString(),
  deadline: null,
};

const resolution: AgentInterruptResolution = { approved: false, rejectionMessage: "no" };
const resolveInput: ResolveInterruptInput = { interruptId: "call-1", approved: true, rejectionMessage: null };

/** An exhaustive mapping of runner-visible statuses, as the Vercel AI SDK adapter writes one. */
function describeStatus(status: AgentToolCallStatus): string {
  switch (status) {
    case "awaiting_approval":
    case "approved":
      return "open";
    case "rejected":
    case "succeeded":
    case "failed":
    case "outcome_unknown":
      return "final";
  }
}

class EchoRunner implements AgentFrameworkRunner {
  public buildUserMessage(text: string): FrameworkMessage {
    return { role: "user", text, toolCallId: null, toolName: null, toolArguments: null, toolOutput: null,
      frameworkItem: JSON.stringify({ text }) };
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const note = buildTurnFailureNote(turn.toolCalls);
    const record = turn.recordedToolCall("call-1");
    const outcome = record !== undefined && isFinalToolCallStatus(record.status) ? describeToolCallOutcome(record) : note;
    const toolCall: DeferredToolCall = {
      toolCallId: "call-2",
      toolName: "send_email",
      arguments: "{}",
      policy: turn.resolveToolPolicy("send_email", toolEffectFromMcpToolAnnotations({ readOnlyHint: true })),
    };
    return turn.isResuming
      ? { kind: "completed", newMessages: [this.buildUserMessage(outcome)], usage: emptyAgentUsage() }
      : { kind: "tool_calls_requested", frameworkRunState: "{}", toolCalls: [toolCall], usage: emptyAgentUsage() };
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    if (call.toolCall.status !== "approved") {
      throw new ToolExecutionError("not approved");
    }
    return { output: describeStatus(call.toolCall.status) };
  }
}

const options: AgentSessionOptions = { toolPolicies: { send_email: { effect: "read" } }, interruptDeadlineMs: 1_000 };
export const flow: AgentSessionFlow = createAgentSessionFlow(new EchoRunner(), "CompatibilityAgent", options);
const resolvedOptions: ResolvedAgentSessionOptions = {
  toolPolicies: {},
  defaultToolEffect: "unclassified",
  eventStreamCapacityBytes: defaultAgentEventsCapacityBytes,
  interruptDeadlineMs: undefined,
};

export async function useFlowAsIn010(environment: AgentTestEnvironment, snapshot: AgentSnapshot): Promise<string[]> {
  const result: ToolExecutionResult = await flow.runner.executeDeferredToolCall({
    flowId: "f",
    toolCall: approvedRecord,
    idempotencyKey: "f/turn-1/call-1",
    attempt: 1,
    events: silentEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: async () => undefined,
  });
  const sessions = new AgentSessionTestClient(environment.client, flow);
  const page: AgentMessagesPage = await sessions.listAgentMessages("session", null, 10);
  const events: AgentEvent[] = await sessions.listAgentEvents("session");
  const eventType: AgentEventType = "interrupt_raised";
  const statuses = snapshot.toolCalls.map((toolCall) => (toolCall.status === "approved" ? "approved" : "other"));
  const usage = addAgentUsage(snapshot.usage, emptyAgentUsage());
  const policy = resolveToolPolicy("send_email", resolvedOptions.toolPolicies, resolvedOptions.defaultToolEffect);
  return [
    result.output,
    String(page.messages.length + events.length + usage.modelCalls),
    eventType,
    ...statuses,
    String(policy.requiresApproval),
    interrupt.interruptId,
    String(resolution.approved && resolveInput.approved),
    messageSequenceKey(1),
    approvalDeadlinePassed,
    defaultRejectionToolResult,
    outcomeUnknownToolResult,
    turnFailureNoteHeader,
    turnFailureNoteFooter,
  ];
}
