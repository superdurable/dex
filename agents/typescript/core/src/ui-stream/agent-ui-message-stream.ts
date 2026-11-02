// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Client, StreamMessage } from "@superdurable/dex";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "../agent-framework-runner.js";
import type { AgentSessionFlow } from "../agent-session-flow.js";
import {
  describeToolCallOutcome,
  outcomeUnknownToolResult,
  type AgentEvent,
} from "../agent-session-values.js";
import {
  findAgentEventResumeTokenBeforeTurn,
  parseTurnNumber,
  probeAgentEventResumeToken,
  readNextAgentEvent,
  readStepType,
  requireAgentEventPollTimeoutMs,
  resumeTokenProbeTimeoutMs,
} from "./agent-event-stream-positions.js";
import { convertAgentEventToJson } from "./agent-event-json.js";
import { AgentSessionRecordReader } from "./agent-session-record-reader.js";
import {
  agentAssistantUIMessageId,
  agentEventDataPartType,
  agentResumeTokenDataPartType,
  type AgentUIMessageChunk,
  type AgentUIMessageFinishReason,
} from "./agent-ui-message-types.js";
import {
  cloneAgentUIStreamCursor,
  createAgentUIStreamCursor,
  decodeAgentUIStreamCursor,
  encodeAgentUIStreamCursor,
  type AgentUIStreamCursor,
} from "./agent-ui-stream-cursor.js";

const modelCallStepType = "RunModel";
const failedToolCallText = "The tool call failed.";

/**
 * Selects the agent session and Stream position of an AI SDK UI message stream.
 *
 * Pass it to {@link streamAgentUIMessageChunks} or {@link createAgentUIMessageStreamResponse}. The Client's
 * Registry must contain `flow`, the same object the Worker registered.
 */
export interface AgentUIMessageStreamOptions {
  /** Client used for every Stream read, Attribute read, and RPC. The caller keeps ownership. */
  readonly client: Client;
  /** The agent session Flow, such as the value an adapter factory returns. */
  readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  /**
   * Session ID: the Flow ID of the agent session. The value usually comes from the browser, so the route
   * must check that the caller may read this session before it streams.
   */
  readonly flowId: string;
  /**
   * UI stream resume token from the latest `data-agent-resume-token` part, or from
   * {@link readAgentSessionUIMessages}. It is not a Dex Stream resume token; pass it back unchanged.
   *
   * By default the stream replays the token's turn from its first retained event, starting with a `start`
   * part. Omit the token to replay the session's active turn instead.
   */
  readonly resumeToken?: string | undefined;
  /**
   * Whether a stream started with `resumeToken` continues exactly after the last part the browser received,
   * without a `start` part, instead of replaying the turn. Defaults to `false`.
   *
   * Pass `true` only while the browser still holds the parts the token continues: with `useChat`, while
   * `status` is `"error"` after a dropped connection. After a clean close or a page reload the browser
   * starts from an empty message, so a continuation would show only the rest of the turn.
   */
  readonly continueAfterResumeToken?: boolean;
  /**
   * Long-poll duration of one `AgentEvents` read, in milliseconds. It must be a whole number of seconds.
   * After each empty poll the stream checks whether the turn ended without its end event, whether the
   * session Flow closed, and whether the turn waits only for approvals. Defaults to 10,000.
   */
  readonly pollTimeoutMs?: number;
  /**
   * Whether the stream finishes with reason `tool-calls` when the turn can only continue after
   * `resolve_interrupt`. `useChat` sends tool approvals only after its response stream ended, so keep the
   * default `true` with `sendAutomaticallyWhen`. Pass `false` when the application resolves approvals
   * through its own endpoint and keeps one stream open for the whole turn.
   */
  readonly finishWhenAwaitingApproval?: boolean;
  /** Ends the stream before its next Stream read once aborted, such as the request's `signal`. */
  readonly signal?: AbortSignal | undefined;
}

/**
 * Translates one agent turn from the session's `AgentEvents` Stream into AI SDK UI message stream parts.
 *
 * The parts follow one turn. Without a resume token they replay the session's active turn from its start.
 * With one they replay the token's turn, or continue it exactly with `continueAfterResumeToken`. The
 * generator returns after the turn completes (`finish` with `stop`), fails (`error`, then `finish` with
 * `error`), or waits only for tool approvals (`finish` with `tool-calls`). It also returns when the turn
 * ended without its end event on the Stream, or the session Flow closed (`finish` with `other`); the
 * session's committed state decides this, so the stream ends even when the Stream lost every event. On
 * `other`, reload the stored messages with {@link readAgentSessionUIMessages}.
 *
 * Every `RunModel` Step execution is one AI SDK step. When a new attempt of the same Step execution starts,
 * for example after a Worker crash, the generator emits `reset-step`, so the browser drops the partial
 * text of the interrupted attempt. Tool calls become dynamic tool parts. A tool call that needs approval
 * gets a `tool-approval-request` whose `approvalId` is the `interrupt_id` for `resolve_interrupt`. A
 * `question` interrupt is not an approval: its `interrupt_raised` and `interrupt_resolved` events arrive as
 * agent event data parts, and the browser answers it with `answer_question`. A
 * transient `data-agent-resume-token` part follows every part; send the latest one back to resume.
 *
 * The generator reads tool arguments and results from the committed `AgentToolCalls` records, waiting up
 * to 3 seconds for the commit that follows each event, and from the stored messages once the turn deleted
 * its records. It also waits up to 3 seconds for a turn's end to commit before it finishes. Events are
 * best effort: a part whose event the Stream lost or trimmed is missing until the stored messages replace
 * the streamed message.
 *
 * Stop in the browser only closes the HTTP response. The turn keeps running in Dex; a durable turn cancel
 * is not available yet.
 *
 * @example
 * ```ts
 * import { createUIMessageStreamResponse } from "ai";
 * import { streamAgentUIMessageChunks } from "@superdurable/dex-agents/ui-stream";
 *
 * export async function GET(request: Request, sessionId: string): Promise<Response> {
 *   const chunks = streamAgentUIMessageChunks({
 *     client, flow, flowId: sessionId,
 *     resumeToken: request.headers.get("x-agent-resume-token") ?? undefined, signal: request.signal,
 *   });
 *   return createUIMessageStreamResponse({ stream: ReadableStream.from(chunks) });
 * }
 * ```
 * @param options - Session, start position, and polling.
 * @returns An async generator of UI message stream parts. It yields nothing when the token's turn already
 * finished, or when the stream would replay a turn that is not active or not on the Stream.
 * @throws {TypeError} When `resumeToken` is not a UI stream resume token.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {DexServiceError} When a Stream read, Attribute read, or RPC fails.
 */
export async function* streamAgentUIMessageChunks(
  options: AgentUIMessageStreamOptions,
): AsyncGenerator<AgentUIMessageChunk, void, undefined> {
  const pollTimeoutMs = requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  const start = await prepareAgentUIStreamStart(options);
  if (start === undefined) {
    return;
  }
  for await (const chunks of readAgentUIMessageChunkBatches(options, start, pollTimeoutMs)) {
    if (chunks !== undefined) {
      yield* chunks;
    }
  }
}

/** Where a stream starts, with the first event already read when one was waiting. */
export interface AgentUIStreamStart {
  readonly cursor: AgentUIStreamCursor;
  readonly firstMessage: StreamMessage<AgentEvent> | undefined;
}

/**
 * Resolves where a stream starts and checks the Dex resume token with one short read.
 *
 * A token that Dex rejects, such as a forged one, restarts the stream from the turn's first retained
 * event, which the Server lists itself.
 * @returns The start, or `undefined` when there is nothing to stream.
 * @throws {TypeError} When the resume token is invalid.
 */
export async function prepareAgentUIStreamStart(
  options: AgentUIMessageStreamOptions,
): Promise<AgentUIStreamStart | undefined> {
  const cursor = await resolveAgentUIStreamStartCursor(options);
  if (cursor === undefined) {
    return undefined;
  }
  return probeAgentUIStreamStart(options.client, options.flow, options.flowId, cursor);
}

/** Checks a cursor's Dex resume token, replacing a rejected one with the turn's listed start. */
export async function probeAgentUIStreamStart(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  flowId: string,
  cursor: AgentUIStreamCursor,
): Promise<AgentUIStreamStart | undefined> {
  const probe = await probeAgentEventResumeToken(client, flow, flowId, cursor.eventResumeToken);
  if (probe.isAccepted) {
    return { cursor, firstMessage: probe.firstMessage };
  }
  if (cursor.turnId === null) {
    return undefined;
  }
  const replayCursor = await createTurnReplayCursor(client, flow, flowId, cursor.turnId, null);
  if (replayCursor === undefined) {
    return undefined;
  }
  const replayProbe = await probeAgentEventResumeToken(client, flow, flowId, replayCursor.eventResumeToken);
  return replayProbe.isAccepted ? { cursor: replayCursor, firstMessage: replayProbe.firstMessage } : undefined;
}

/**
 * Returns the cursor where a stream starts, or `undefined` when there is nothing to stream.
 * @throws {TypeError} When the resume token is invalid.
 */
export async function resolveAgentUIStreamStartCursor(
  options: AgentUIMessageStreamOptions,
): Promise<AgentUIStreamCursor | undefined> {
  const { client, flow, flowId } = options;
  if (options.resumeToken !== undefined) {
    const cursor = decodeAgentUIStreamCursor(options.resumeToken);
    if (cursor.isTurnFinished) {
      return undefined;
    }
    if (options.continueAfterResumeToken === true || cursor.turnId === null) {
      return cursor;
    }
    // The browser may hold any part of the turn, so replay it and let the start part reset the message.
    return (
      (await createTurnReplayCursor(client, flow, flowId, cursor.turnId, cursor.turnStartResumeToken)) ??
      restartTurnAtCursor(cursor)
    );
  }
  const [state] = await client.getAttributes(flowId, flow.agentState);
  const activeTurnId = state?.activeTurnId ?? null;
  return activeTurnId === null
    ? undefined
    : createTurnReplayCursor(client, flow, flowId, activeTurnId, null);
}

/**
 * Returns a cursor that replays a turn from its first retained event, or `undefined` when none is retained.
 * @param knownTurnStartResumeToken - The turn's start position recorded in a cursor, or `null` to search.
 */
export async function createTurnReplayCursor(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  flowId: string,
  turnId: string,
  knownTurnStartResumeToken: string | null,
): Promise<AgentUIStreamCursor | undefined> {
  const eventResumeToken =
    knownTurnStartResumeToken ?? (await findAgentEventResumeTokenBeforeTurn(client, flow, flowId, turnId));
  if (eventResumeToken === undefined) {
    return undefined;
  }
  const cursor = createAgentUIStreamCursor(eventResumeToken, turnId, false);
  cursor.turnStartResumeToken = eventResumeToken;
  return cursor;
}

/** Restarts the message at the cursor's position when the turn's start is no longer on the Stream. */
function restartTurnAtCursor(cursor: AgentUIStreamCursor): AgentUIStreamCursor {
  const restarted = createAgentUIStreamCursor(cursor.eventResumeToken, cursor.turnId, false);
  restarted.skippedChunkCount = 0;
  return restarted;
}

/**
 * Reads events from the cursor and yields the parts of each one.
 * Yields `undefined` after an empty long poll, so a response can send a keep-alive comment.
 */
export async function* readAgentUIMessageChunkBatches(
  options: AgentUIMessageStreamOptions,
  start: AgentUIStreamStart,
  pollTimeoutMs: number,
): AsyncGenerator<AgentUIMessageChunk[] | undefined, void, undefined> {
  const { client, flow, flowId } = options;
  const cursor = start.cursor;
  const translator = new AgentUIMessageChunkTranslator(
    new AgentSessionRecordReader(client, flow, flowId),
    cursor,
    options.finishWhenAwaitingApproval ?? true,
  );
  let pendingMessage = start.firstMessage;
  while (!translator.isComplete && options.signal?.aborted !== true) {
    const message =
      pendingMessage ??
      (await readNextAgentEvent(client, flow, flowId, cursor.eventResumeToken, pollTimeoutMs));
    pendingMessage = undefined;
    if (message !== undefined) {
      yield await translator.translateEvent(message);
      continue;
    }
    const idleDecision = await translator.decideAfterIdle();
    if (idleDecision !== "wait") {
      // Events reach the Stream before their commit; read once more before ending on committed state.
      pendingMessage = await readNextAgentEvent(
        client,
        flow,
        flowId,
        cursor.eventResumeToken,
        resumeTokenProbeTimeoutMs,
      );
      if (pendingMessage !== undefined) {
        continue;
      }
    }
    const chunks = translator.applyIdleDecision(idleDecision);
    yield chunks.length > 0 ? chunks : undefined;
  }
}

/** What an empty long poll means: keep waiting, end the turn's message, or pause for approvals. */
type IdleDecision = "wait" | "end" | "pause";

/** Marks where the final resume token goes, before the `error` and `finish` parts. */
const finalResumeTokenMarker = Symbol("final resume token");

/** Translates the events of the followed turn into parts, keeping the cursor in step with the browser. */
class AgentUIMessageChunkTranslator {
  private chunks: (AgentUIMessageChunk | typeof finalResumeTokenMarker)[] = [];
  private isStreamEnded = false;

  public constructor(
    private readonly records: AgentSessionRecordReader,
    private readonly cursor: AgentUIStreamCursor,
    private readonly isFinishedWhenAwaitingApproval: boolean,
  ) {}

  public get isComplete(): boolean {
    return this.isStreamEnded || this.cursor.isTurnFinished;
  }

  public async translateEvent(message: StreamMessage<AgentEvent>): Promise<AgentUIMessageChunk[]> {
    const skippedChunkCount = this.cursor.skippedChunkCount;
    this.cursor.skippedChunkCount = 0;
    const cursorBeforeEvent = cloneAgentUIStreamCursor(this.cursor);
    this.cursor.eventResumeToken = message.resumeToken;
    const event = message.value;
    if (this.isFollowedTurnEvent(event, cursorBeforeEvent.eventResumeToken)) {
      this.startMessage();
      await this.translateFollowedTurnEvent(event);
      if (
        !this.isComplete &&
        this.isFinishedWhenAwaitingApproval &&
        this.cursor.modelStep?.isUsageRecorded === true &&
        (await this.isConfirmedWaitingOnlyForApprovals())
      ) {
        this.endStream("tool-calls");
      }
    }
    return this.takeChunks(cursorBeforeEvent, skippedChunkCount);
  }

  /** Decides from committed state what an empty long poll means; it may start following a turn. */
  public async decideAfterIdle(): Promise<IdleDecision> {
    const turnId = this.cursor.turnId;
    const state = await this.records.readAgentState();
    if (turnId === null) {
      const afterTurnNumber = this.cursor.afterTurnNumber;
      const activeTurnNumber =
        state?.activeTurnId == null ? undefined : parseTurnNumber(state.activeTurnId);
      if (
        state?.activeTurnId != null &&
        activeTurnNumber !== undefined &&
        (afterTurnNumber === null || activeTurnNumber > afterTurnNumber)
      ) {
        // The turn started, but its turn_started event never reached this stream.
        this.cursor.turnId = state.activeTurnId;
        return "wait";
      }
      if (afterTurnNumber !== null && state !== undefined && state.turnCount > afterTurnNumber) {
        return "end";
      }
      return (await this.records.isFlowRunning()) ? "wait" : "end";
    }
    if (state?.activeTurnId !== turnId || !(await this.records.isFlowRunning())) {
      return "end";
    }
    return this.isFinishedWhenAwaitingApproval && (await this.isConfirmedWaitingOnlyForApprovals())
      ? "pause"
      : "wait";
  }

  public applyIdleDecision(idleDecision: IdleDecision): AgentUIMessageChunk[] {
    if (idleDecision === "end") {
      this.endStream("other");
    } else if (idleDecision === "pause") {
      this.endStream("tool-calls");
    }
    return this.takeChunks(undefined, 0);
  }

  private isFollowedTurnEvent(event: AgentEvent, eventResumeTokenBefore: string): boolean {
    if (this.cursor.turnId !== null) {
      return event.turnId === this.cursor.turnId;
    }
    // A cursor without a turn follows the next turn that starts after its position.
    if (event.type !== "turn_started" || event.turnId === null) {
      return false;
    }
    const turnNumber = parseTurnNumber(event.turnId);
    const afterTurnNumber = this.cursor.afterTurnNumber;
    if (afterTurnNumber !== null && (turnNumber === undefined || turnNumber <= afterTurnNumber)) {
      return false;
    }
    this.cursor.turnId = event.turnId;
    this.cursor.turnStartResumeToken = eventResumeTokenBefore;
    return true;
  }

  private startMessage(): void {
    if (this.cursor.isMessageStarted || this.cursor.turnId === null) {
      return;
    }
    this.chunks.push({ type: "start", messageId: agentAssistantUIMessageId(this.cursor.turnId) });
    this.cursor.isMessageStarted = true;
  }

  private async translateFollowedTurnEvent(event: AgentEvent): Promise<void> {
    const payload = parseEventPayload(event.payload);
    if (payload === undefined) {
      this.pushAgentEventData(event);
      return;
    }
    switch (event.type) {
      case "turn_started":
        return;
      case "text_delta":
      case "reasoning_delta": {
        const text = readPayloadString(payload, "text");
        const stepType = readStepType(event.stepExecutionId);
        // Text from a tool Step belongs to no model call, so it travels as event data.
        if (text === undefined || (stepType !== modelCallStepType && stepType !== "")) {
          this.pushAgentEventData(event);
          return;
        }
        if (this.enterModelStep(event)) {
          this.appendDelta(event.type === "text_delta" ? "text" : "reasoning", text);
        }
        return;
      }
      case "tool_call_deferred": {
        const toolCallId = readPayloadString(payload, "tool_call_id");
        const toolName = readPayloadString(payload, "tool_name");
        if (toolCallId === undefined || toolName === undefined) {
          this.pushAgentEventData(event);
          return;
        }
        if (this.enterModelStep(event)) {
          await this.introduceToolCall(toolCallId, toolName);
        }
        return;
      }
      case "interrupt_raised": {
        const interruptId = readPayloadString(payload, "interrupt_id");
        const toolCallId = readPayloadString(payload, "tool_call_id");
        // A question is answered with answer_question, not approved, so it stays an event data part.
        if (interruptId === undefined || toolCallId === undefined || payload["kind"] === "question") {
          this.pushAgentEventData(event);
          return;
        }
        if (this.enterModelStep(event)) {
          await this.requestToolApproval(toolCallId, readPayloadString(payload, "tool_name"), interruptId);
        }
        return;
      }
      case "usage_recorded":
        if (this.enterModelStep(event) && this.cursor.modelStep !== null) {
          this.cursor.modelStep.isUsageRecorded = true;
        }
        this.pushAgentEventData(event);
        return;
      case "tool_call_started": {
        const toolCallId = readPayloadString(payload, "tool_call_id");
        if (toolCallId !== undefined) {
          await this.introduceToolCall(toolCallId, readPayloadString(payload, "tool_name"));
        }
        this.pushAgentEventData(event);
        return;
      }
      case "interrupt_resolved": {
        const interruptId = readPayloadString(payload, "interrupt_id");
        const isApproved = payload["approved"];
        if (interruptId === undefined || typeof isApproved !== "boolean" || payload["kind"] === "question") {
          this.pushAgentEventData(event);
          return;
        }
        await this.recordToolApproval(interruptId, isApproved, readPayloadString(payload, "rejection_message"));
        return;
      }
      case "tool_call_finished": {
        const toolCallId = readPayloadString(payload, "tool_call_id");
        const status = readPayloadString(payload, "status");
        if (toolCallId === undefined || status === undefined) {
          this.pushAgentEventData(event);
          return;
        }
        await this.finishToolCall(toolCallId, readPayloadString(payload, "tool_name"), status, event);
        return;
      }
      case "turn_completed":
        if (this.enterModelStep(event) && (await this.records.waitForTurnToEnd(this.requireTurnId()))) {
          this.endStream("stop");
        }
        return;
      case "turn_failed":
        if (await this.records.waitForTurnToEnd(this.requireTurnId())) {
          this.endStream("error", readPayloadString(payload, "error") ?? "the agent turn failed");
        }
        return;
      default:
        this.pushAgentEventData(event);
    }
  }

  /**
   * Moves to the event's model call, opening a new step or resetting an interrupted attempt.
   * @returns `false` for an event of an attempt older than the one the browser shows, which is dropped.
   */
  private enterModelStep(event: AgentEvent): boolean {
    const modelStep = this.cursor.modelStep;
    if (modelStep === null || modelStep.stepExecutionId !== event.stepExecutionId) {
      this.closeOpenPart();
      if (modelStep !== null) {
        this.chunks.push({ type: "finish-step" });
      }
      this.chunks.push({ type: "start-step" });
      this.cursor.modelStep = {
        stepExecutionId: event.stepExecutionId,
        attempt: event.attempt,
        openedPartCount: 0,
        isUsageRecorded: false,
      };
      // Every tool call of the previous model call finished before this one started.
      this.cursor.toolCalls.clear();
      return true;
    }
    if (event.attempt < modelStep.attempt) {
      return false;
    }
    if (event.attempt > modelStep.attempt) {
      // The browser removes every part of the current step, open parts included.
      this.chunks.push({ type: "reset-step" });
      this.cursor.openPart = null;
      this.cursor.toolCalls.clear();
      modelStep.attempt = event.attempt;
      modelStep.openedPartCount = 0;
      modelStep.isUsageRecorded = false;
    }
    return true;
  }

  private appendDelta(kind: "text" | "reasoning", text: string): void {
    const modelStep = this.cursor.modelStep;
    if (text === "" || modelStep === null) {
      return;
    }
    let openPart = this.cursor.openPart;
    if (openPart !== null && openPart.kind !== kind) {
      this.closeOpenPart();
      openPart = null;
    }
    if (openPart === null) {
      const id = `${modelStep.stepExecutionId}.${modelStep.attempt}.${modelStep.openedPartCount}`;
      modelStep.openedPartCount += 1;
      openPart = { kind, id };
      this.cursor.openPart = openPart;
      this.chunks.push(kind === "text" ? { type: "text-start", id } : { type: "reasoning-start", id });
    }
    this.chunks.push(
      kind === "text"
        ? { type: "text-delta", id: openPart.id, delta: text }
        : { type: "reasoning-delta", id: openPart.id, delta: text },
    );
  }

  private closeOpenPart(): void {
    const openPart = this.cursor.openPart;
    if (openPart === null) {
      return;
    }
    this.chunks.push(
      openPart.kind === "text"
        ? { type: "text-end", id: openPart.id }
        : { type: "reasoning-end", id: openPart.id },
    );
    this.cursor.openPart = null;
  }

  /**
   * Adds the dynamic tool part of a tool call the browser does not show yet.
   * @returns Whether the browser shows the tool call's part afterwards.
   */
  private async introduceToolCall(toolCallId: string, toolName: string | undefined): Promise<boolean> {
    if (this.cursor.toolCalls.has(toolCallId)) {
      return true;
    }
    const details = await this.records.readToolCallDetails(this.requireTurnId(), toolCallId, () => true);
    const resolvedToolName = toolName ?? details.toolName;
    if (resolvedToolName === undefined) {
      return false;
    }
    this.closeOpenPart();
    this.chunks.push({
      type: "tool-input-available",
      toolCallId,
      toolName: resolvedToolName,
      // JSON drops undefined, and the AI SDK requires the field.
      input: parseToolArguments(details.toolArguments) ?? null,
      dynamic: true,
    });
    this.cursor.toolCalls.set(toolCallId, "input");
    return true;
  }

  private async requestToolApproval(
    toolCallId: string,
    toolName: string | undefined,
    interruptId: string,
  ): Promise<void> {
    if (!(await this.introduceToolCall(toolCallId, toolName))) {
      return;
    }
    if (this.cursor.toolCalls.get(toolCallId) !== "input") {
      return;
    }
    this.chunks.push({ type: "tool-approval-request", approvalId: interruptId, toolCallId });
    this.cursor.toolCalls.set(toolCallId, "approval_requested");
  }

  private async recordToolApproval(
    interruptId: string,
    isApproved: boolean,
    rejectionMessage: string | undefined,
  ): Promise<void> {
    // A tool approval's interrupt ID is its tool call ID.
    const toolCallId = interruptId;
    await this.requestToolApproval(toolCallId, undefined, interruptId);
    if (this.cursor.toolCalls.get(toolCallId) !== "approval_requested") {
      return;
    }
    this.chunks.push({
      type: "tool-approval-response",
      approvalId: interruptId,
      approved: isApproved,
      ...(rejectionMessage === undefined ? {} : { reason: rejectionMessage }),
    });
    this.cursor.toolCalls.set(toolCallId, "approval_resolved");
  }

  private async finishToolCall(
    toolCallId: string,
    toolName: string | undefined,
    status: string,
    event: AgentEvent,
  ): Promise<void> {
    if (status === "rejected") {
      await this.recordToolApproval(toolCallId, false, undefined);
      if (this.cursor.toolCalls.has(toolCallId)) {
        this.chunks.push({ type: "tool-output-denied", toolCallId });
        this.cursor.toolCalls.set(toolCallId, "finished");
      }
      return;
    }
    if (status !== "succeeded" && status !== "failed" && status !== "outcome_unknown") {
      this.pushAgentEventData(event);
      return;
    }
    if (!(await this.introduceToolCall(toolCallId, toolName))) {
      return;
    }
    if (status === "outcome_unknown") {
      this.chunks.push({ type: "tool-output-error", toolCallId, errorText: outcomeUnknownToolResult, dynamic: true });
    } else {
      const details = await this.records.readToolCallDetails(
        this.requireTurnId(),
        toolCallId,
        (toolCall) => toolCall.status === status,
      );
      const record = details.record?.status === status ? details.record : undefined;
      if (status === "succeeded") {
        this.chunks.push({
          type: "tool-output-available",
          toolCallId,
          output: record?.output ?? details.storedToolOutput ?? null,
          dynamic: true,
        });
      } else {
        this.chunks.push({
          type: "tool-output-error",
          toolCallId,
          errorText:
            record === undefined
              ? (details.storedToolOutput ?? failedToolCallText)
              : describeToolCallOutcome(record),
          dynamic: true,
        });
      }
    }
    this.cursor.toolCalls.set(toolCallId, "finished");
  }

  /**
   * Reports whether the turn waits only for approvals, judged by the committed records and
   * `PendingInterrupts`: a replay reaches an approval request before the events that answered it.
   */
  private async isConfirmedWaitingOnlyForApprovals(): Promise<boolean> {
    if (!this.isWaitingOnlyForApprovals()) {
      return false;
    }
    const requestedApprovalToolCallIds: string[] = [];
    for (const [toolCallId, state] of this.cursor.toolCalls) {
      if (state !== "approval_requested") {
        continue;
      }
      const record = await this.records.readCommittedToolCall(this.requireTurnId(), toolCallId);
      if (record?.status !== "awaiting_approval") {
        return false;
      }
      requestedApprovalToolCallIds.push(toolCallId);
    }
    // resolve_interrupt removes the interrupt before the approval Step commits the record.
    const pendingApprovalToolCallIds = await this.records.readPendingApprovalToolCallIds();
    return requestedApprovalToolCallIds.every((toolCallId) => pendingApprovalToolCallIds.has(toolCallId));
  }

  private isWaitingOnlyForApprovals(): boolean {
    let hasRequestedApproval = false;
    for (const state of this.cursor.toolCalls.values()) {
      if (state === "approval_requested") {
        hasRequestedApproval = true;
      } else if (state !== "finished") {
        return false;
      }
    }
    return hasRequestedApproval;
  }

  private endStream(finishReason: AgentUIMessageFinishReason, errorText?: string): void {
    this.closeOpenPart();
    if (finishReason === "tool-calls") {
      this.isStreamEnded = true;
    } else {
      if (this.cursor.modelStep !== null) {
        this.chunks.push({ type: "finish-step" });
      }
      this.cursor.isTurnFinished = true;
    }
    this.chunks.push(finalResumeTokenMarker);
    if (errorText !== undefined) {
      this.chunks.push({ type: "error", errorText });
    }
    this.chunks.push({ type: "finish", finishReason });
  }

  private pushAgentEventData(event: AgentEvent): void {
    this.chunks.push({ type: agentEventDataPartType, data: convertAgentEventToJson(event), transient: true });
  }

  /**
   * Returns the event's parts after the ones the browser already holds, each followed by a resume token.
   * A token inside the event names the state before it plus the parts already sent, so a cut between
   * two parts of one event resumes without repeating either. Parts of no event, `cursorBeforeEvent`
   * `undefined`, get only the final token.
   */
  private takeChunks(
    cursorBeforeEvent: AgentUIStreamCursor | undefined,
    skippedChunkCount: number,
  ): AgentUIMessageChunk[] {
    const chunks = this.chunks;
    this.chunks = [];
    let lastChunkIndex = chunks.length - 1;
    while (lastChunkIndex >= 0 && chunks[lastChunkIndex] === finalResumeTokenMarker) {
      lastChunkIndex -= 1;
    }
    const markerIndex = chunks.indexOf(finalResumeTokenMarker);
    const translatedChunks: AgentUIMessageChunk[] = [];
    let chunkCount = 0;
    for (const [index, chunk] of chunks.entries()) {
      if (chunk === finalResumeTokenMarker) {
        translatedChunks.push(this.createResumeTokenChunk(this.cursor));
        continue;
      }
      chunkCount += 1;
      if (chunkCount <= skippedChunkCount) {
        continue;
      }
      translatedChunks.push(chunk);
      if (markerIndex >= 0 && index > markerIndex) {
        continue;
      }
      if (index === lastChunkIndex || index + 1 === markerIndex) {
        if (markerIndex < 0) {
          translatedChunks.push(this.createResumeTokenChunk(this.cursor));
        }
        continue;
      }
      if (cursorBeforeEvent !== undefined) {
        translatedChunks.push(
          this.createResumeTokenChunk({ ...cursorBeforeEvent, skippedChunkCount: chunkCount }),
        );
      }
    }
    return translatedChunks;
  }

  private createResumeTokenChunk(cursor: AgentUIStreamCursor): AgentUIMessageChunk {
    return {
      type: agentResumeTokenDataPartType,
      data: encodeAgentUIStreamCursor(cursor),
      transient: true,
    };
  }

  private requireTurnId(): string {
    if (this.cursor.turnId === null) {
      throw new Error("the agent UI stream translated an event before it followed a turn");
    }
    return this.cursor.turnId;
  }
}

function parseEventPayload(payload: string): Record<string, unknown> | undefined {
  try {
    const value: unknown = JSON.parse(payload);
    return typeof value === "object" && value !== null && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : undefined;
  } catch {
    return undefined;
  }
}

function readPayloadString(payload: Record<string, unknown>, key: string): string | undefined {
  const value = payload[key];
  return typeof value === "string" ? value : undefined;
}

/** Parses tool arguments JSON text; text that is not JSON stays a string. */
export function parseToolArguments(toolArguments: string | null | undefined): unknown {
  if (toolArguments === null || toolArguments === undefined) {
    return undefined;
  }
  try {
    return JSON.parse(toolArguments) as unknown;
  } catch {
    return toolArguments;
  }
}
