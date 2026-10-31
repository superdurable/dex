// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";

import {
  createSdkMcpServer,
  query,
  type HookCallbackMatcher,
  type HookEvent,
  type McpServerConfig,
  type Options,
  type SDKMessage,
  type SDKResultMessage,
  type SDKUserMessage,
} from "@anthropic-ai/claude-agent-sdk";
import {
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type AgentUsage,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ToolExecutionResult,
} from "@superdurable/dex-agents";
import * as zodV3 from "zod/v3";
import * as zodV4 from "zod/v4";

import { ToolCallGate, ToolCatalog, type ClaudeCustomTool } from "./claude-tool-call-gate.js";
import {
  decodeRunState,
  encodeRunState,
  encodeUserPromptItem,
  loadSessionTranscript,
  projectTurnEntries,
  buildTurnPromptContent,
  toolResultText,
  type ClaudeTranscriptEntry,
  type PromptTextBlock,
} from "./claude-transcript.js";
import { TurnSegmentSessionStore } from "./turn-segment-session-store.js";

/**
 * Name of the in-process MCP server that serves custom tools unless you pass another.
 *
 * The model sees a custom tool `lookup_order` as `mcp__tools__lookup_order`. Tool policies still use
 * the bare name, `lookup_order`. The value matches the Python adapter.
 */
export const defaultCustomToolServerName = "tools";

/**
 * Output recorded for an approved tool that runs inside the Claude Code CLI, such as `Bash`.
 *
 * The model never sees it: the CLI runs the real tool when the turn resumes. It appears only as the
 * `output` of the tool call record while the turn is active. The text matches the Python adapter.
 */
export const approvedCliToolOutput = "approved; Claude Code runs this tool when the turn resumes";

/**
 * Identifies the turn segment that asks an options factory for Claude Agent SDK `Options`.
 *
 * {@link ClaudeAgentRunner} calls a {@link ClaudeAgentOptionsFactory} once per `RunModel` attempt
 * with this context. Use it to give each session its own working directory or sandbox, for example
 * `cwd: \`/srv/agents/${context.flowId}\``. Keep the result deterministic for a Flow ID: a resumed turn
 * must see the same working directory, tools, and model as the attempt that suspended it.
 */
export interface ClaudeAgentOptionsContext {
  /** Session ID, which is the Flow ID. */
  readonly flowId: string;
  /** `turn-<n>` of the running turn. */
  readonly turnId: string;
  /** One-based `RunModel` attempt. */
  readonly attempt: number;
}

/**
 * Claude Agent SDK `Options` that an application may set for a Dex agent session.
 *
 * The adapter owns the options left out of this type and sets them for every turn segment:
 * `canUseTool`, `sessionStore`, `resume`, and `includePartialMessages`, plus its own
 * `abortController`. It also owns `permissionPromptToolName`, `sessionId`, `continue`, `forkSession`,
 * `resumeSessionAt`, `resumeDropsTurn`, and `enableFileCheckpointing`, which it rejects. It adds
 * its PreToolUse hook after yours, its custom tool server to `mcpServers`, and the CLI arguments
 * `--await-initialize` and `--mcp-config` to `extraArgs`; `extraArgs` must not set those, `--resume`,
 * or `--session-id`. A turn segment is one `RunModel` Step: the CLI runs from the turn's prompt or a
 * resumed deferred call until it defers the next call or answers.
 */
export type ClaudeAgentQueryOptions = Omit<
  Options,
  | "abortController"
  | "canUseTool"
  | "continue"
  | "enableFileCheckpointing"
  | "forkSession"
  | "includePartialMessages"
  | "permissionPromptToolName"
  | "resume"
  | "resumeDropsTurn"
  | "resumeSessionAt"
  | "sessionId"
  | "sessionStore"
>;

/**
 * Builds the application's Claude Agent SDK options for one turn segment.
 *
 * @param context - The session, turn, and attempt that run the segment.
 * @returns Options for this segment; see {@link ClaudeAgentQueryOptions} for the options you may set.
 */
export type ClaudeAgentOptionsFactory = (context: ClaudeAgentOptionsContext) => ClaudeAgentQueryOptions;

/** Construction options of {@link ClaudeAgentRunner}. */
export interface ClaudeAgentRunnerOptions {
  /**
   * Custom tools created with `tool()` from `@anthropic-ai/claude-agent-sdk`. The runner serves them
   * from an in-process MCP server. Their MCP annotations classify them when no explicit policy exists:
   * `readOnlyHint` means `read`, and `idempotentHint` without `destructiveHint` means
   * `idempotent_write`. Defaults to none.
   */
  readonly customTools?: readonly ClaudeCustomTool[];
  /**
   * Name of the in-process MCP server for custom tools. The model sees each custom tool as
   * `mcp__<name>__<tool>`. It must be letters, digits, `-`, and single underscores. Defaults to
   * {@link defaultCustomToolServerName}.
   */
  readonly customToolServerName?: string;
}

const adapterOwnedOptionNames = [
  "abortController",
  "canUseTool",
  "continue",
  "enableFileCheckpointing",
  "forkSession",
  "permissionPromptToolName",
  "resume",
  "resumeDropsTurn",
  "resumeSessionAt",
  "sessionId",
  "sessionStore",
] as const;
const permissionModesThatSkipTheAdapter = new Set(["acceptEdits", "auto", "bypassPermissions"]);
const customToolServerNamePattern = /^[A-Za-z0-9-]+(?:_[A-Za-z0-9-]+)*$/;
const runningDeferredToolExecution = new AsyncLocalStorage<DeferredToolExecution>();

/**
 * Returns the deferred tool call that the running custom tool handler serves.
 *
 * A custom tool handler receives only its arguments and an MCP `extra` object, so it cannot see the Dex
 * Step that runs it. Call this function inside the handler to read the approved record, the attempt,
 * and the idempotency key `<flowId>/<toolCallId>`. Pass that key to an external system that supports
 * idempotency, so an `idempotent_write` retry has no further effect.
 *
 * @example
 * ```ts
 * const createRefund = tool("create_refund", "Refund an order", { orderId: z.string() }, async (args) => {
 *   const execution = currentDeferredToolExecution();
 *   const refund = await payments.refund(args.orderId, { idempotencyKey: execution?.idempotencyKey });
 *   return { content: [{ type: "text", text: `refunded ${refund.id}` }] };
 * }, { annotations: { idempotentHint: true } });
 * ```
 * @returns The `DeferredToolExecution` while `executeDeferredToolCall` runs the handler in a tool Step,
 * or `undefined` when the handler runs inline inside `RunModel`, as `read` tools do.
 */
export function currentDeferredToolExecution(): DeferredToolExecution | undefined {
  return runningDeferredToolExecution.getStore();
}

/**
 * Runs a Claude Agent SDK agent as a durable Dex agent session.
 *
 * The Claude Agent SDK runs its agent loop in a Claude Code CLI subprocess, so it is a whole-turn
 * harness: one `RunModel` Step runs one turn segment, which may make several model calls and inline
 * tool calls, until the CLI defers a call or answers. Every `RunModel` attempt starts a new CLI process
 * with `query()`. The Dex session stores the CLI's transcript entries, and the runner resumes the Claude
 * session from them through a `SessionStore`, so any Worker can run the next segment.
 *
 * Custom tools, created with `tool()`, run in an in-process MCP server that the runner creates. A
 * PreToolUse hook applies the session's tool policies to every call. `read` calls without approval run
 * inline. The first other custom tool call of a segment is deferred: the CLI stops, the session runs
 * the call once in `CallTool` or `CallSingleAttemptTool`, after `AwaitToolApproval` when needed, and
 * `ResumeTurn` starts a `RunModel` whose CLI receives the recorded result instead of running the tool
 * again. A later deferrable call of the same segment is denied with a request to call it again alone,
 * because the CLI resumes only one deferred call. Tools that run inside the CLI, such as `Bash`, `Write`, or `Edit`, cannot be recorded.
 * They defer only when their policy requires approval, run inside the resumed turn after approval, and
 * are at-least-once: a crash during `RunModel` can run them again.
 *
 * Most applications call {@link createClaudeAgentSessionFlow} instead of constructing the runner.
 *
 * A turn attempt fails with a `TypeError` when the options set an option the adapter owns (see
 * {@link ClaudeAgentQueryOptions}); when `permissionMode` is `"acceptEdits"`, `"auto"`, or
 * `"bypassPermissions"`; when `mcpServers` already uses the custom tool server name; or when an
 * `allowedTools` entry names a tool whose calls must defer. `permissionMode` unset, `"default"`, and
 * `"dontAsk"` all work.
 *
 * @example
 * ```ts
 * const runner = new ClaudeAgentRunner(
 *   (context) => ({ model: "claude-sonnet-5", cwd: `/srv/agents/${context.flowId}`, settingSources: [] }),
 *   { customTools: [lookupOrder, createRefund] },
 * );
 * const flow = createAgentSessionFlow(runner, "SupportAgent");
 * ```
 */
export class ClaudeAgentRunner implements AgentFrameworkRunner {
  private readonly toolCatalog: ToolCatalog;

  /**
   * Creates a runner for one agent.
   *
   * @param options - The application's Claude Agent SDK options, or a factory that returns them for
   * each turn segment. A factory runs on the Worker for every `RunModel` attempt.
   * @param runnerOptions - Custom tools and the custom tool server name.
   * @throws {TypeError} When the server name is invalid or two custom tools share a name.
   */
  public constructor(
    private readonly options: ClaudeAgentQueryOptions | ClaudeAgentOptionsFactory,
    runnerOptions: ClaudeAgentRunnerOptions = {},
  ) {
    const customToolServerName = runnerOptions.customToolServerName ?? defaultCustomToolServerName;
    if (!customToolServerNamePattern.test(customToolServerName)) {
      throw new TypeError(
        "customToolServerName must be letters, digits, '-', and single underscores",
      );
    }
    this.toolCatalog = new ToolCatalog(runnerOptions.customTools ?? [], customToolServerName);
  }

  /**
   * Stores user text as the prompt of the next turn.
   *
   * The CLI records its own copy of the prompt in the transcript when the turn runs, so this item is
   * never replayed to the CLI.
   *
   * @param text - The user message text.
   * @returns A `"user"` message whose `frameworkItem` is `{"kind": "user_prompt", "text": text}`.
   */
  public buildUserMessage(text: string): FrameworkMessage {
    return {
      role: "user",
      text,
      toolCallId: null,
      toolName: null,
      toolArguments: null,
      toolOutput: null,
      frameworkItem: encodeUserPromptItem(text),
    };
  }

  /**
   * Runs or resumes one turn segment in a new Claude Code CLI process.
   *
   * A first segment sends the turn's prompt and resumes the Claude session from the transcript of
   * earlier turns. A resumed segment sends no prompt: the CLI finds the deferred tool use at the end of
   * the transcript and resumes it by itself, through the PreToolUse hook, which now answers from the
   * call's final record. The CLI then adds a hidden "Continue from where you left off." message. The
   * runner keeps the CLI's input open until the segment's result message, then closes it.
   *
   * @param turn - Committed session state for this segment.
   * @returns `ToolCallsRequested` with the one call the CLI deferred, and the Claude session ID and the
   * turn's transcript entries so far as run state; otherwise `TurnCompleted` with the turn's transcript
   * entries grouped into messages.
   * @throws {TypeError} When the options are unsupported.
   * @throws {Error} When the CLI reported an error result, ended without a result, or its transcript
   * could not be stored. The session retries the segment.
   */
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const committedTranscript = loadSessionTranscript(turn.messages);
    let sessionId: string | null;
    let earlierTurnEntries: readonly ClaudeTranscriptEntry[];
    let promptContent: string | PromptTextBlock[] | undefined;
    if (turn.frameworkRunState === null) {
      sessionId = committedTranscript.sessionId;
      earlierTurnEntries = [];
      promptContent = buildTurnPromptContent(turn.messages, turn.turnId);
    } else {
      const suspendedTurn = decodeRunState(turn.frameworkRunState);
      sessionId = suspendedTurn.sessionId;
      earlierTurnEntries = suspendedTurn.turnEntries;
      promptContent = undefined;
    }
    const resumedEntries = [...committedTranscript.entries, ...earlierTurnEntries];
    const resumedSessionId = resumedEntries.length === 0 ? null : sessionId;
    const sessionStore = new TurnSegmentSessionStore(resumedSessionId, resumedEntries);
    const toolCallGate = new ToolCallGate(turn, this.toolCatalog);
    const observer = new TurnSegmentObserver(turn, toolCallGate);
    const segmentInput = new TurnSegmentInput(promptContent);
    const abortController = new AbortController();
    const abortSegment = (): void => abortController.abort(turn.cancellationSignal.reason);
    if (turn.cancellationSignal.aborted) {
      abortSegment();
    }
    turn.cancellationSignal.addEventListener("abort", abortSegment, { once: true });
    try {
      const applicationOptions = this.readApplicationOptions(turn, toolCallGate);
      const options = this.buildTurnOptions(applicationOptions, {
        resumedSessionId,
        sessionStore,
        toolCallGate,
        abortController,
      });
      for await (const message of query({ prompt: segmentInput, options })) {
        observer.observe(message);
        if (message.type === "result") {
          segmentInput.close();
        }
      }
    } finally {
      segmentInput.close();
      turn.cancellationSignal.removeEventListener("abort", abortSegment);
    }
    const result = observer.successfulResult();
    const segmentTranscript = sessionStore.takeSegmentTranscript();
    const turnEntries = [...earlierTurnEntries, ...segmentTranscript.entries];
    const usage = observer.usage(result);
    if (result.subtype === "success" && result.deferred_tool_use !== undefined) {
      return {
        kind: "tool_calls_requested",
        frameworkRunState: encodeRunState(segmentTranscript.sessionId, turnEntries),
        toolCalls: [toolCallGate.deferredToolCall(result.deferred_tool_use)],
        usage,
      };
    }
    return {
      kind: "completed",
      newMessages: projectTurnEntries(segmentTranscript.sessionId, turnEntries, (cliToolName) =>
        this.toolCatalog.policyToolName(cliToolName),
      ),
      usage,
    };
  }

  /**
   * Runs one approved or deferred call outside the CLI.
   *
   * A custom tool runs its handler once, after its arguments are parsed with the tool's input schema.
   * {@link currentDeferredToolExecution} returns `call` inside the handler, and the handler's `extra`
   * argument carries only `signal`, `requestId`, and `_meta`. A tool that runs inside the CLI is only
   * marked approved here; the resumed turn runs it.
   *
   * @param call - The approved record and its idempotency key.
   * @returns The custom tool's text output, or {@link approvedCliToolOutput} for a tool that runs inside
   * the CLI. Non-text content is replaced by a short placeholder.
   * @throws {ToolExecutionError} When the arguments do not match the input schema, or the custom tool
   * returned `isError`. The model sees the text.
   * @throws Any error the handler throws, which fails the Step attempt.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const record = call.toolCall;
    const customTool = this.toolCatalog.customTool(record.toolName);
    if (customTool === undefined) {
      return { output: approvedCliToolOutput };
    }
    const toolArguments = parseCustomToolArguments(customTool, record.arguments);
    const handlerExtra = {
      signal: call.cancellationSignal,
      requestId: record.toolCallId,
      _meta: { "claudecode/toolUseId": record.toolCallId },
    };
    const result = await runningDeferredToolExecution.run(call, () =>
      customTool.handler(toolArguments, handlerExtra),
    );
    const text = toolResultText(result.content);
    if (result.isError === true) {
      throw new ToolExecutionError(text);
    }
    return { output: text };
  }

  private readApplicationOptions(turn: AgentTurnContext, toolCallGate: ToolCallGate): ClaudeAgentQueryOptions {
    const applicationOptions =
      typeof this.options === "function"
        ? this.options({ flowId: turn.flowId, turnId: turn.turnId, attempt: turn.attempt })
        : this.options;
    this.validateApplicationOptions(applicationOptions, toolCallGate);
    return applicationOptions;
  }

  private buildTurnOptions(
    applicationOptions: ClaudeAgentQueryOptions,
    segment: TurnSegmentResources,
  ): Options {
    const serverName = this.toolCatalog.customToolServerName;
    const mcpServers: Record<string, McpServerConfig> = { ...applicationOptions.mcpServers };
    const extraArgs: Record<string, string | null> = { ...applicationOptions.extraArgs };
    // A defense: an earlier design saw the CLI resume a deferred call before registering the hooks.
    extraArgs["await-initialize"] = null;
    const customTools = this.toolCatalog.customTools;
    if (customTools.length > 0) {
      mcpServers[serverName] = createSdkMcpServer({
        name: serverName,
        tools: customTools.map((customTool) => segment.toolCallGate.gatedCustomTool(customTool)),
      });
      // Declared at startup, as the Python SDK does, the server's tools exist when a deferred call resumes.
      extraArgs["mcp-config"] = JSON.stringify({ mcpServers: { [serverName]: { type: "sdk", name: serverName } } });
    }
    const hooks: Partial<Record<HookEvent, HookCallbackMatcher[]>> = { ...applicationOptions.hooks };
    hooks.PreToolUse = [...(hooks.PreToolUse ?? []), { hooks: [segment.toolCallGate.decidePreToolUse] }];
    return {
      ...applicationOptions,
      mcpServers,
      hooks,
      canUseTool: segment.toolCallGate.decideToolPermission,
      sessionStore: segment.sessionStore,
      ...(segment.resumedSessionId === null ? {} : { resume: segment.resumedSessionId }),
      includePartialMessages: true,
      abortController: segment.abortController,
      extraArgs,
    };
  }

  private validateApplicationOptions(
    options: ClaudeAgentQueryOptions,
    toolCallGate: ToolCallGate,
  ): void {
    const optionValues = options as Readonly<Record<string, unknown>>;
    const setOptionNames = adapterOwnedOptionNames.filter(
      (optionName) => optionValues[optionName] !== undefined && optionValues[optionName] !== false,
    );
    if (setOptionNames.length > 0) {
      throw new TypeError(
        `the Claude agent adapter owns ${setOptionNames.join(", ")}; leave them unset in the options`,
      );
    }
    if (options.permissionMode !== undefined && permissionModesThatSkipTheAdapter.has(options.permissionMode)) {
      throw new TypeError(
        `permissionMode ${JSON.stringify(options.permissionMode)} lets tools run without the ` +
          "session's tool policies; use 'default' or 'dontAsk'",
      );
    }
    for (const argumentName of ["mcp-config", "resume", "session-id", "await-initialize"]) {
      if (options.extraArgs !== undefined && Object.hasOwn(options.extraArgs, argumentName)) {
        throw new TypeError(`the Claude agent adapter owns the CLI argument --${argumentName}; leave it out of extraArgs`);
      }
    }
    const serverName = this.toolCatalog.customToolServerName;
    if (
      this.toolCatalog.customTools.length > 0 &&
      options.mcpServers !== undefined &&
      Object.hasOwn(options.mcpServers, serverName)
    ) {
      throw new TypeError(`mcpServers already has a server named ${JSON.stringify(serverName)}`);
    }
    for (const allowedTool of options.allowedTools ?? []) {
      const policyToolName = this.toolCatalog.policyToolName(allowedTool.split("(", 1)[0]?.trim() ?? "");
      const policy = toolCallGate.resolvePolicy(policyToolName);
      if (this.toolCatalog.requiresDeferral(policyToolName, policy)) {
        throw new TypeError(
          `allowedTools entry ${JSON.stringify(allowedTool)} would let ${policyToolName} run without ` +
            "its approval or Step; remove it or give the tool a policy that runs inline",
        );
      }
    }
  }
}

interface TurnSegmentResources {
  readonly resumedSessionId: string | null;
  readonly sessionStore: TurnSegmentSessionStore;
  readonly toolCallGate: ToolCallGate;
  readonly abortController: AbortController;
}

/** The CLI's input for one segment: the prompt, if any, then nothing until the result arrives. */
class TurnSegmentInput implements AsyncIterable<SDKUserMessage> {
  private readonly closed: Promise<void>;
  private resolveClosed: () => void = () => undefined;

  public constructor(private readonly promptContent: string | PromptTextBlock[] | undefined) {
    this.closed = new Promise<void>((resolve) => {
      this.resolveClosed = resolve;
    });
  }

  public close(): void {
    this.resolveClosed();
  }

  public async *[Symbol.asyncIterator](): AsyncIterator<SDKUserMessage> {
    if (this.promptContent !== undefined) {
      yield {
        type: "user",
        session_id: "",
        message: { role: "user", content: this.promptContent },
        parent_tool_use_id: null,
      };
    }
    // Closing stdin early would end the CLI before the segment's result.
    await this.closed;
  }
}

/** Turns the SDK message stream of one segment into events, usage, and a result. */
class TurnSegmentObserver {
  private readonly modelMessageIds = new Set<string>();
  private readonly mirrorErrors: string[] = [];
  private result: SDKResultMessage | undefined;

  public constructor(
    private readonly turn: AgentTurnContext,
    private readonly toolCallGate: ToolCallGate,
  ) {}

  public observe(message: SDKMessage): void {
    switch (message.type) {
      case "stream_event":
        if (message.parent_tool_use_id === null) {
          this.writeStreamEventDelta(message.event);
        }
        return;
      case "assistant":
        if (message.parent_tool_use_id === null) {
          this.modelMessageIds.add(message.message.id ?? message.uuid);
        }
        return;
      case "user":
        this.recordToolResults(message.message.content);
        return;
      case "system":
        if (message.subtype === "mirror_error") {
          this.mirrorErrors.push(String((message as { readonly error?: unknown }).error ?? "unknown error"));
        }
        return;
      case "result":
        this.result = message;
        return;
      default:
        return;
    }
  }

  public successfulResult(): SDKResultMessage {
    if (this.mirrorErrors.length > 0) {
      throw new Error(`storing the Claude Code transcript failed: ${this.mirrorErrors.join("; ")}`);
    }
    if (this.result === undefined) {
      throw new Error("the Claude Code CLI ended the turn without a result message");
    }
    if (this.result.is_error || this.result.subtype !== "success") {
      const details =
        this.result.subtype === "success" ? this.result.result : this.result.errors.join("; ");
      throw new Error(
        `the Claude Code CLI reported ${this.result.subtype}: ${details === "" ? "no details" : details}`,
      );
    }
    return this.result;
  }

  public usage(result: SDKResultMessage): AgentUsage {
    const resultUsage = result.usage as unknown as Readonly<Record<string, unknown>>;
    const cacheReadTokens = tokenCount(resultUsage["cache_read_input_tokens"]);
    const cacheCreationTokens = tokenCount(resultUsage["cache_creation_input_tokens"]);
    return {
      modelCalls: this.modelMessageIds.size,
      inputTokens: tokenCount(resultUsage["input_tokens"]) + cacheReadTokens + cacheCreationTokens,
      outputTokens: tokenCount(resultUsage["output_tokens"]),
      cachedInputTokens: cacheReadTokens,
      reasoningTokens: 0,
    };
  }

  private writeStreamEventDelta(event: unknown): void {
    if (!isRecord(event) || event["type"] !== "content_block_delta" || !isRecord(event["delta"])) {
      return;
    }
    const delta = event["delta"];
    if (delta["type"] === "text_delta") {
      this.turn.events.writeTextDelta(String(delta["text"] ?? ""));
    } else if (delta["type"] === "thinking_delta") {
      this.turn.events.writeReasoningDelta(String(delta["thinking"] ?? ""));
    }
  }

  private recordToolResults(content: unknown): void {
    if (!Array.isArray(content)) {
      return;
    }
    for (const block of content) {
      if (isRecord(block) && block["type"] === "tool_result") {
        this.toolCallGate.recordToolResult(String(block["tool_use_id"]), block["is_error"] === true);
      }
    }
  }
}

function parseCustomToolArguments(
  customTool: ClaudeCustomTool,
  argumentsJson: string,
): Record<string, unknown> {
  const rawArguments: unknown = JSON.parse(argumentsJson);
  const shape = customTool.inputSchema as Record<string, unknown>;
  const isZodV4Shape = Object.values(shape).some(
    (schema) => typeof schema === "object" && schema !== null && "_zod" in schema,
  );
  const parsed = isZodV4Shape
    ? zodV4.object(shape as zodV4.ZodRawShape).safeParse(rawArguments)
    : zodV3.object(shape as zodV3.ZodRawShape).safeParse(rawArguments);
  if (!parsed.success) {
    throw new ToolExecutionError(`invalid arguments for ${customTool.name}: ${parsed.error.message}`);
  }
  return parsed.data as Record<string, unknown>;
}

function tokenCount(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
