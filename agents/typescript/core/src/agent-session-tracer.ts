// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createRequire } from "node:module";

import type * as OpenTelemetryApi from "@opentelemetry/api";
import type { Context } from "@superdurable/dex";

import type { AgentTurnContext, AgentTurnOutcome } from "./agent-framework-runner.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import {
  genAISemanticConventionsSchemaUrls,
  type ResolvedAgentTracingOptions,
} from "./agent-tracing-options.js";
import {
  describeToolCallOutcome,
  type AgentMessage,
  type AgentToolCall,
  type AgentToolCallRecord,
  type AgentUsage,
  type FrameworkMessage,
} from "./agent-session-values.js";

type OpenTelemetryApiModule = typeof OpenTelemetryApi;
type ChatMessage = { readonly role: string; readonly parts: readonly Record<string, unknown>[] };

const tracerName = "@superdurable/dex-agents";
const toolExecutionErrorType = "ToolExecutionError";
const traceparentPattern = /^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/;
const requireFromCore = createRequire(import.meta.url);
let loadedOpenTelemetryApi: OpenTelemetryApiModule | null | undefined;
let hasLoggedTracingFailure = false;

/** The root span of an activation, as the Step that starts it sees it. */
export interface ActivationSpan {
  /** The root's W3C `traceparent` when its span context is valid, sampled or not; otherwise `null`. */
  readonly traceparent: string | null;
}

/** Keeps the outcome of one `RunModel` attempt for its `chat` span, emitted when the attempt ends. */
export interface ModelCallSpan {
  recordOutcome(outcome: AgentTurnOutcome, appendedMessageKeys: readonly string[]): void;
}

/** Records the final record of one tool Step attempt on its `execute_tool` span. */
export interface ToolCallSpan {
  recordFinished(finishedToolCall: AgentToolCall | AgentToolCallRecord): void;
}

const unrecordedActivation: ActivationSpan = Object.freeze({ traceparent: null });
const disabledModelCallSpan: ModelCallSpan = { recordOutcome: () => undefined };
const disabledToolCallSpan: ToolCallSpan = { recordFinished: () => undefined };

/**
 * Emits the spans of one agent session Flow type, or nothing when tracing is off.
 *
 * An activation's root `invoke_agent` span stores its `traceparent` in `AgentTraceContext`. `RunModel`
 * reads it to parent its `chat` span and copies it onto the tool call records it creates; an approval
 * replaces a record's copy with its own root. Tool Steps parent their spans from the record.
 * `@opentelemetry/api` loads at the first Step that emits a span, never at import. Tracing never
 * fails a Step: every OpenTelemetry call is guarded, logged, and skipped on error.
 */
export class AgentSessionTracer {
  private tracer: OpenTelemetryApi.Tracer | null | undefined;
  private readonly isLatestVersion: boolean;
  private readonly shouldCaptureContent: boolean;

  public constructor(
    private readonly options: ResolvedAgentTracingOptions,
    private readonly persistence: AgentSessionPersistence,
  ) {
    this.isLatestVersion = options.semanticConventionsVersion === "1.42.0-dev";
    this.shouldCaptureContent = options.shouldCaptureMessageContent && this.isLatestVersion;
  }

  /** Runs a Step that starts a turn with a user message inside an activation root span. */
  public traceUserMessageActivation<Result>(
    context: Context,
    stepType: string,
    turnId: string,
    userMessageKey: string,
    userMessageText: string,
    run: (activation: ActivationSpan) => Result,
  ): Result {
    return this.traceActivation(
      context,
      stepType,
      turnId,
      { "dex.agent.activation.trigger": "user_message", "dex.agent.user_message.key": userMessageKey },
      userMessageText,
      run,
    );
  }

  /** Runs a Step that applies an interrupt answer or deadline inside an activation root span. */
  public traceInterruptActivation<Result>(
    context: Context,
    stepType: string,
    turnId: string,
    interruptId: string,
    isApproved: boolean,
    isDeadlinePassed: boolean,
    run: (activation: ActivationSpan) => Result,
  ): Result {
    return this.traceActivation(
      context,
      stepType,
      turnId,
      {
        "dex.agent.activation.trigger": isDeadlinePassed ? "interrupt_deadline" : "interrupt_resolution",
        "dex.agent.interrupt.id": interruptId,
        "dex.agent.interrupt.approved": isApproved,
      },
      null,
      run,
    );
  }

  /** Returns the stored root `traceparent` that new tool call records carry. */
  public readActivationTraceparent(context: Context): string | null {
    return callSafely("read the activation trace context", () => this.readTraceparent(context)) ?? null;
  }

  /**
   * Runs one `RunModel` attempt and emits its `chat` span when the attempt ends.
   *
   * The span starts after the runner returns, because its kind depends on the reported `modelCalls`.
   * Meanwhile the activation root is the active span, so model client instrumentation nests under it.
   */
  public async traceModelCall<Result>(
    context: Context,
    stepType: string,
    turn: AgentTurnContext,
    run: (modelCallSpan: ModelCallSpan) => Promise<Result>,
  ): Promise<Result> {
    const tracing = this.resolveTracing();
    if (tracing === null) {
      return run(disabledModelCallSpan);
    }
    const startTime = performance.timeOrigin + performance.now();
    const parent = callSafely("read the activation trace context", () =>
      activationParentContext(tracing.api, this.readTraceparent(context)),
    );
    const modelCallSpan = new RecordingModelCallSpan(this.isLatestVersion, this.shouldCaptureContent);
    let error: unknown;
    let hasFailed = false;
    try {
      return await runInContext(tracing.api, parent, () => run(modelCallSpan));
    } catch (raised: unknown) {
      hasFailed = true;
      error = raised;
      throw raised;
    } finally {
      if (parent !== undefined) {
        callSafely("emit the chat span", () =>
          modelCallSpan.emit(tracing, parent, startTime, context, stepType, turn, hasFailed, error),
        );
      }
    }
  }

  /** Runs one tool Step attempt inside an `execute_tool` span of the record's activation. */
  public async traceToolCall<Result>(
    context: Context,
    stepType: string,
    toolCall: AgentToolCall | AgentToolCallRecord,
    run: (toolCallSpan: ToolCallSpan) => Promise<Result>,
  ): Promise<Result> {
    const tracing = this.resolveTracing();
    const span =
      tracing === null
        ? undefined
        : callSafely("start the execute_tool span", () =>
            tracing.tracer.startSpan(
              `execute_tool ${toolCall.toolName}`,
              {
                kind: tracing.api.SpanKind.INTERNAL,
                attributes: {
                  "gen_ai.operation.name": "execute_tool",
                  "gen_ai.tool.name": toolCall.toolName,
                  "gen_ai.agent.name": this.options.agentName,
                },
              },
              activationParentContext(tracing.api, toolCall.traceContext ?? null),
            ),
          );
    if (tracing === null || span === undefined) {
      return run(disabledToolCallSpan);
    }
    callSafely("set the execute_tool span attributes", () => {
      if (!span.isRecording()) {
        return;
      }
      span.setAttributes(stepAttributes(context, stepType, toolCall.turnId));
      span.setAttribute("gen_ai.tool.call.id", toolCall.toolCallId);
      span.setAttribute("dex.agent.tool.effect", toolCall.effect);
      if (this.shouldCaptureContent) {
        span.setAttribute("gen_ai.tool.call.arguments", toolCall.arguments);
      }
    });
    const toolCallSpan = new RecordingToolCallSpan(tracing.api, span, this.shouldCaptureContent);
    return runInSpanAsync(tracing.api, span, () => run(toolCallSpan));
  }

  private traceActivation<Result>(
    context: Context,
    stepType: string,
    turnId: string,
    activationAttributes: OpenTelemetryApi.Attributes,
    userMessageText: string | null,
    run: (activation: ActivationSpan) => Result,
  ): Result {
    const storedTraceparent =
      callSafely("read the activation trace context", () => this.readTraceparent(context)) ?? null;
    const tracing = this.resolveTracing();
    const span =
      tracing === null
        ? undefined
        : callSafely("start the invoke_agent span", () =>
            startActivationSpan(tracing, this.options.agentName, storedTraceparent),
          );
    let activation = unrecordedActivation;
    let result: Result;
    if (tracing === null || span === undefined) {
      result = run(activation);
    } else {
      callSafely("set the invoke_agent span attributes", () => {
        if (!span.isRecording()) {
          return;
        }
        span.setAttributes(stepAttributes(context, stepType, turnId));
        span.setAttributes(activationAttributes);
        if (this.shouldCaptureContent && userMessageText !== null) {
          span.setAttribute(
            "gen_ai.input.messages",
            JSON.stringify([{ role: "user", parts: [textPart(userMessageText)] }]),
          );
        }
      });
      // An unsampled root stores its context too, so its children follow its sampling decision.
      const traceparent = callSafely("read the invoke_agent span context", () =>
        formatTraceparent(tracing.api, span.spanContext()),
      );
      activation = traceparent == null ? unrecordedActivation : { traceparent };
      const recordedActivation = activation;
      result = runInSpanSync(tracing.api, span, () => run(recordedActivation));
    }
    // A root without a valid context must not leave the previous activation's context behind.
    const traceparent = activation.traceparent;
    callSafely("store the activation trace context", () => {
      if (traceparent !== null) {
        if (traceparent !== storedTraceparent) {
          this.persistence.agentTraceContext.set(context, traceparent);
        }
      } else if (storedTraceparent !== null) {
        this.persistence.agentTraceContext.delete(context);
      }
    });
    return result;
  }

  private readTraceparent(context: Context): string | null {
    return (this.persistence.agentTraceContext.get(context) as string | undefined) ?? null;
  }

  private resolveTracing(): ResolvedTracing | null {
    if (this.tracer === undefined) {
      this.tracer = callSafely("create the tracer", () => this.createTracer()) ?? null;
    }
    if (this.tracer === null || loadedOpenTelemetryApi == null) {
      return null;
    }
    return { api: loadedOpenTelemetryApi, tracer: this.tracer };
  }

  private createTracer(): OpenTelemetryApi.Tracer | null {
    if (!this.options.isEnabled) {
      return null;
    }
    const api = loadOpenTelemetryApi();
    if (api === null) {
      return null;
    }
    const schemaUrl = genAISemanticConventionsSchemaUrls[this.options.semanticConventionsVersion];
    const tracerProvider = this.options.tracerProvider ?? api.trace.getTracerProvider();
    return tracerProvider.getTracer(tracerName, readPackageVersion(), { schemaUrl }) as OpenTelemetryApi.Tracer;
  }
}

interface ResolvedTracing {
  readonly api: OpenTelemetryApiModule;
  readonly tracer: OpenTelemetryApi.Tracer;
}

class RecordingModelCallSpan implements ModelCallSpan {
  private outcome: AgentTurnOutcome | undefined;
  private appendedMessageKeys: readonly string[] = [];

  public constructor(
    private readonly isLatestVersion: boolean,
    private readonly shouldCaptureContent: boolean,
  ) {}

  public recordOutcome(outcome: AgentTurnOutcome, appendedMessageKeys: readonly string[]): void {
    this.outcome = outcome;
    this.appendedMessageKeys = appendedMessageKeys;
  }

  public emit(
    tracing: ResolvedTracing,
    parent: OpenTelemetryApi.Context,
    startTime: number,
    context: Context,
    stepType: string,
    turn: AgentTurnContext,
    hasFailed: boolean,
    error: unknown,
  ): void {
    const { api, tracer } = tracing;
    const outcome = this.outcome;
    const requestModel = nonEmptyString(outcome?.modelCallMetadata?.requestModel);
    // One span for several model calls must not double count usage that provider spans report.
    const isSingleModelCall = outcome === undefined || outcome.usage?.modelCalls === 1;
    const span = tracer.startSpan(
      requestModel === undefined ? "chat" : `chat ${requestModel}`,
      {
        kind: isSingleModelCall ? api.SpanKind.CLIENT : api.SpanKind.INTERNAL,
        attributes: {
          "gen_ai.operation.name": "chat",
          ...(requestModel === undefined ? {} : { "gen_ai.request.model": requestModel }),
        },
        startTime,
      },
      parent,
    );
    try {
      if (!span.isRecording()) {
        return;
      }
      span.setAttributes(stepAttributes(context, stepType, turn.turnId));
      const lastMessage = turn.messages.at(-1);
      if (lastMessage !== undefined) {
        span.setAttribute("dex.agent.input_messages.last_key", messageSequenceKey(lastMessage.sequence));
      }
      if (this.shouldCaptureContent) {
        span.setAttribute("gen_ai.input.messages", JSON.stringify(inputMessages(turn)));
      }
      if (outcome !== undefined) {
        this.setOutcomeAttributes(span, outcome, isSingleModelCall);
      }
      if (hasFailed) {
        recordSpanError(api, span, error);
      }
    } finally {
      span.end();
    }
  }

  private setOutcomeAttributes(
    span: OpenTelemetryApi.Span,
    outcome: AgentTurnOutcome,
    isSingleModelCall: boolean,
  ): void {
    const metadata = outcome.modelCallMetadata;
    const responseModel = nonEmptyString(metadata?.responseModel);
    if (responseModel !== undefined) {
      span.setAttribute("gen_ai.response.model", responseModel);
    }
    const providerName = nonEmptyString(metadata?.providerName);
    if (providerName !== undefined) {
      span.setAttribute(this.isLatestVersion ? "gen_ai.provider.name" : "gen_ai.system", providerName);
    }
    const responseId = nonEmptyString(metadata?.responseId);
    if (responseId !== undefined) {
      span.setAttribute("gen_ai.response.id", responseId);
    }
    const finishReasons = Array.isArray(metadata?.finishReasons)
      ? metadata.finishReasons.filter((reason): reason is string => nonEmptyString(reason) !== undefined)
      : [];
    if (finishReasons.length > 0) {
      span.setAttribute("gen_ai.response.finish_reasons", finishReasons);
    }
    if (isSingleModelCall) {
      this.setUsageAttributes(span, outcome.usage);
    }
    let outputMessages: ChatMessage[];
    if (outcome.kind === "completed") {
      const producedIndex = firstProducedMessageIndex(outcome.newMessages);
      span.setAttribute("dex.agent.output_messages.keys", this.appendedMessageKeys.slice(producedIndex));
      outputMessages = outcome.newMessages.slice(producedIndex).map(chatMessage);
    } else {
      span.setAttribute(
        "dex.agent.requested_tool_call.ids",
        outcome.toolCalls.map((toolCall) => toolCall.toolCallId),
      );
      outputMessages = [
        {
          role: "assistant",
          parts: outcome.toolCalls.map((toolCall) =>
            toolCallPart(toolCall.toolCallId, toolCall.toolName, toolCall.arguments),
          ),
        },
      ];
    }
    if (this.shouldCaptureContent) {
      span.setAttribute(
        "gen_ai.output.messages",
        JSON.stringify(outputMessages.filter((message) => message.parts.length > 0)),
      );
    }
  }

  private setUsageAttributes(span: OpenTelemetryApi.Span, usage: AgentUsage | undefined): void {
    // Runners report zero for counts the framework does not expose.
    if (usage === undefined || (usage.inputTokens === 0 && usage.outputTokens === 0)) {
      return;
    }
    span.setAttribute("gen_ai.usage.input_tokens", usage.inputTokens);
    span.setAttribute("gen_ai.usage.output_tokens", usage.outputTokens);
    if (!this.isLatestVersion) {
      return;
    }
    if (usage.cachedInputTokens > 0) {
      span.setAttribute("gen_ai.usage.cache_read.input_tokens", usage.cachedInputTokens);
    }
    if (usage.reasoningTokens > 0) {
      span.setAttribute("gen_ai.usage.reasoning.output_tokens", usage.reasoningTokens);
    }
  }
}

class RecordingToolCallSpan implements ToolCallSpan {
  public constructor(
    private readonly api: OpenTelemetryApiModule,
    private readonly span: OpenTelemetryApi.Span,
    private readonly shouldCaptureContent: boolean,
  ) {}

  public recordFinished(finishedToolCall: AgentToolCall | AgentToolCallRecord): void {
    callSafely("record the tool call outcome", () => {
      const span = this.span;
      if (!span.isRecording()) {
        return;
      }
      span.setAttribute("dex.agent.tool_call.status", finishedToolCall.status);
      if (finishedToolCall.status === "failed") {
        span.setAttribute("error.type", toolExecutionErrorType);
        span.setStatus({
          code: this.api.SpanStatusCode.ERROR,
          message: finishedToolCall.error ?? toolExecutionErrorType,
        });
      } else if (this.shouldCaptureContent && finishedToolCall.output !== null) {
        span.setAttribute("gen_ai.tool.call.result", finishedToolCall.output);
      }
    });
  }
}

function callSafely<Value>(description: string, operation: () => Value): Value | undefined {
  try {
    return operation();
  } catch (error: unknown) {
    logTracingFailure(description, error);
    return undefined;
  }
}

function logTracingFailure(description: string, error: unknown): void {
  // The first failure logs a warning with its stack; later ones log at debug level.
  if (hasLoggedTracingFailure) {
    console.debug(`@superdurable/dex-agents tracing could not ${description}; the Step continues`, error);
    return;
  }
  hasLoggedTracingFailure = true;
  console.warn(`@superdurable/dex-agents tracing could not ${description}; the Step continues`, error);
}

function loadOpenTelemetryApi(): OpenTelemetryApiModule | null {
  if (loadedOpenTelemetryApi === undefined) {
    try {
      loadedOpenTelemetryApi = requireFromCore("@opentelemetry/api") as OpenTelemetryApiModule;
    } catch {
      // The API is an optional peer dependency; without it the session emits nothing.
      loadedOpenTelemetryApi = null;
    }
  }
  return loadedOpenTelemetryApi;
}

function readPackageVersion(): string | undefined {
  try {
    return (requireFromCore("../../package.json") as { readonly version?: string }).version;
  } catch {
    return undefined;
  }
}

function startActivationSpan(
  tracing: ResolvedTracing,
  agentName: string,
  previousRootTraceparent: string | null,
): OpenTelemetryApi.Span {
  const { api, tracer } = tracing;
  const previousRoot = parseTraceparent(api, previousRootTraceparent);
  // A root span keeps every activation a trace of its own.
  return tracer.startSpan(
    `invoke_agent ${agentName}`,
    {
      root: true,
      kind: api.SpanKind.INTERNAL,
      attributes: { "gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": agentName },
      links: previousRoot === null ? [] : [{ context: previousRoot }],
    },
    api.ROOT_CONTEXT,
  );
}

/** Runs `run` in `otelContext`; a context manager failure runs it without the context instead. */
function runInContext<Result>(
  api: OpenTelemetryApiModule,
  otelContext: OpenTelemetryApi.Context | undefined,
  run: () => Result,
): Result {
  if (otelContext === undefined) {
    return run();
  }
  let hasRun = false;
  const runOnce = (): Result => {
    hasRun = true;
    return run();
  };
  try {
    return api.context.with(otelContext, runOnce);
  } catch (error: unknown) {
    if (hasRun) {
      throw error;
    }
    logTracingFailure("activate the trace context", error);
    return run();
  }
}

function runInSpanSync<Result>(api: OpenTelemetryApiModule, span: OpenTelemetryApi.Span, run: () => Result): Result {
  const spanContext = callSafely("activate the span", () => api.trace.setSpan(api.context.active(), span));
  try {
    return runInContext(api, spanContext, run);
  } catch (error: unknown) {
    callSafely("record the error on the span", () => recordSpanError(api, span, error));
    throw error;
  } finally {
    callSafely("end the span", () => span.end());
  }
}

async function runInSpanAsync<Result>(
  api: OpenTelemetryApiModule,
  span: OpenTelemetryApi.Span,
  run: () => Promise<Result>,
): Promise<Result> {
  const spanContext = callSafely("activate the span", () => api.trace.setSpan(api.context.active(), span));
  try {
    return await runInContext(api, spanContext, run);
  } catch (error: unknown) {
    callSafely("record the error on the span", () => recordSpanError(api, span, error));
    throw error;
  } finally {
    callSafely("end the span", () => span.end());
  }
}

function recordSpanError(api: OpenTelemetryApiModule, span: OpenTelemetryApi.Span, error: unknown): void {
  if (!span.isRecording()) {
    return;
  }
  const errorType = error instanceof Error && error.name !== "" ? error.name : "_OTHER";
  span.setAttribute("error.type", errorType);
  if (error instanceof Error) {
    span.recordException(error);
  }
  const message = error instanceof Error ? error.message : String(error);
  span.setStatus({ code: api.SpanStatusCode.ERROR, message: message === "" ? errorType : message });
}

function stepAttributes(context: Context, stepType: string, turnId: string | null): OpenTelemetryApi.Attributes {
  return {
    "gen_ai.conversation.id": context.flowId,
    "session.id": context.flowId,
    "dex.flow.id": context.flowId,
    "dex.run.id": context.runId,
    "dex.step.type": stepType,
    "dex.step.execution_id": context.stepExecutionId,
    "dex.step.attempt": context.attempt,
    ...(turnId === null ? {} : { "dex.agent.turn.id": turnId }),
  };
}

function activationParentContext(api: OpenTelemetryApiModule, traceparent: string | null): OpenTelemetryApi.Context {
  const root = parseTraceparent(api, traceparent);
  return root === null ? api.ROOT_CONTEXT : api.trace.setSpanContext(api.ROOT_CONTEXT, root);
}

function formatTraceparent(api: OpenTelemetryApiModule, spanContext: OpenTelemetryApi.SpanContext): string | null {
  if (!api.isSpanContextValid(spanContext)) {
    return null;
  }
  const traceFlags = spanContext.traceFlags.toString(16).padStart(2, "0");
  return `00-${spanContext.traceId}-${spanContext.spanId}-${traceFlags}`;
}

function parseTraceparent(
  api: OpenTelemetryApiModule,
  traceparent: string | null,
): OpenTelemetryApi.SpanContext | null {
  const match = traceparent === null ? null : traceparentPattern.exec(traceparent);
  if (match === null) {
    return null;
  }
  const [, traceId = "", spanId = "", traceFlags = "00"] = match;
  const spanContext: OpenTelemetryApi.SpanContext = {
    traceId,
    spanId,
    traceFlags: Number.parseInt(traceFlags, 16),
    isRemote: true,
  };
  return api.isSpanContextValid(spanContext) ? spanContext : null;
}

function nonEmptyString(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

function inputMessages(turn: AgentTurnContext): ChatMessage[] {
  const messages = turn.messages.map(chatMessage);
  // A resumed call also received the turn's tool results, which are not messages yet.
  for (const record of turn.toolCalls) {
    messages.push({
      role: "assistant",
      parts: [toolCallPart(record.toolCallId, record.toolName, record.arguments)],
    });
    messages.push({
      role: "tool",
      parts: [{ type: "tool_call_response", id: record.toolCallId, response: describeToolCallOutcome(record) }],
    });
  }
  return messages.filter((message) => message.parts.length > 0);
}

function firstProducedMessageIndex(newMessages: readonly FrameworkMessage[]): number {
  // newMessages spans the whole turn; this call produced only what follows the last tool result.
  let lastToolResultIndex = -1;
  newMessages.forEach((message, index) => {
    if (message.role === "tool") {
      lastToolResultIndex = index;
    }
  });
  return lastToolResultIndex + 1;
}

function chatMessage(message: AgentMessage | FrameworkMessage): ChatMessage {
  const parts: Record<string, unknown>[] = [];
  if (message.text) {
    parts.push(textPart(message.text));
  }
  if (message.role === "tool") {
    if (message.toolOutput !== null) {
      parts.push({
        type: "tool_call_response",
        ...(message.toolCallId === null ? {} : { id: message.toolCallId }),
        response: message.toolOutput,
      });
    }
  } else if (message.toolName !== null) {
    parts.push(toolCallPart(message.toolCallId, message.toolName, message.toolArguments));
  }
  return { role: message.role, parts };
}

function textPart(text: string): Record<string, unknown> {
  return { type: "text", content: text };
}

function toolCallPart(toolCallId: string | null, toolName: string, toolArguments: string | null): Record<string, unknown> {
  return {
    type: "tool_call",
    ...(toolCallId === null ? {} : { id: toolCallId }),
    name: toolName,
    ...(toolArguments === null ? {} : { arguments: parseJsonValue(toolArguments) }),
  };
}

function parseJsonValue(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}
