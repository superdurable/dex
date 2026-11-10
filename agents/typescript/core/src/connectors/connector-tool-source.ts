// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AsyncContext, RetryPolicy } from "@superdurable/dex";
import {
  RetryError,
  createCall,
  defaultConnectorStepExecuteRetryPolicy,
  runMutation,
  runQuery,
  throwDexRetry,
  type Call,
  type ConnectionRef,
  type Failure,
  type Mutation,
  type Query,
  type QueryDefinition,
  type QueryResult,
} from "@superdurable/connectors-sdk";

import {
  ToolExecutionError,
  ToolOutcomeUnknownError,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type ToolCallStepOptions,
  type ToolExecutionResult,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "../agent-framework-runner.js";
import type { AgentToolCall, ToolEffect } from "../agent-session-values.js";
import { serializeJsonWithSpacedSeparators } from "../stepwise/json-schema-validation.js";
import type { SourceToolDefinition, ToolSource } from "../stepwise/tool-source.js";
import { checkConnectorInputSchema, compareCodePoints, findConnectorInputViolation } from "./connector-input-schema.js";
import { ConnectorStepContext, readSavedHeartbeatValue } from "./connector-step-context.js";

/**
 * Default {@link ConnectorToolSource.sourceName}.
 * @since 0.4.0
 */
export const defaultConnectorSourceName = "connectors";

/**
 * Default bound, in bytes of UTF-8 text, on the result one connector tool call gives the model: 64 KiB.
 * @since 0.4.0
 */
export const defaultConnectorResultMaxBytes = 64 * 1024;

/**
 * Note appended to a truncated result; `{total_bytes}` is the size of the full result. The same text as the
 * Python core.
 * @since 0.4.0
 */
export const connectorResultTruncatedNote = "[Dex] truncated, {total_bytes} bytes";

/**
 * Error text of a single-attempt write whose fence proved no send before the connector's Retry; the same text as
 * the Python core.
 * @since 0.4.0
 */
export const connectorNotSentRetryLater = "not sent; the provider asked to retry later";

// The most CallTool attempts of any tool; a connector's own policy can only narrow it.
const maximumToolAttempts = 3;
const uncertainBranch = "uncertain";

/**
 * Decides from a call's decoded JSON arguments whether that call waits for approval.
 * @since 0.4.0
 */
export type ApprovalPredicate = (toolArguments: Readonly<Record<string, unknown>>) => boolean;

/**
 * What a connector tool call records for the branch its operation selected.
 *
 * - `succeeded`: a required branch, such as `sent` or `refunded`. The model reads the result value.
 * - `rejected`: a branch the manifest lists as `rejected` under `agent.branches`, such as `providerRejected`.
 *   Nothing took effect. The record is `failed` with an error the model reads, and the session continues.
 * - `failed`: an optional branch of a Query that `agent.branches` does not list, including `defect`.
 * - `outcome_unknown`: a branch listed as `unknown`, `uncertain`, or an optional branch of a Mutation that
 *   `agent.branches` does not list, including `defect`. The write may have taken effect.
 *
 * Changed in 0.5.0: `defect` no longer maps to `failed` before `agent.branches` is read. A Mutation's `defect`
 * can follow a provider call, so an unlisted one is `outcome_unknown`. A listed `uncertain` follows its listing.
 * @since 0.4.0
 */
export type ConnectorBranchOutcome = "succeeded" | "rejected" | "failed" | "outcome_unknown";

/**
 * The validator a Connector package exports for an operation's input, such as `CreateRefundInputSchema`. A zod
 * schema satisfies it.
 * @since 0.4.0
 */
export interface ConnectorInputValidator<Input> {
  /**
   * Checks and converts decoded JSON arguments into the operation's input.
   * @param value - The decoded arguments.
   * @returns `success` and the input, or `success: false`.
   */
  safeParse(value: unknown): { readonly success: true; readonly data: Input } | { readonly success: false };
}

/**
 * The parts of a Connector package's `dex-connector.json` that `connectorTool` reads.
 * @since 0.4.0
 */
export interface ConnectorPackageManifest {
  /** The Connector ID, such as `stripe`. */
  readonly connectorId: string;
  /** One entry per operation. */
  readonly factories: readonly ConnectorManifestFactory[];
}

/**
 * One operation's entry in `dex-connector.json`.
 * @since 0.4.0
 */
export interface ConnectorManifestFactory {
  /** `query` or `mutation`. */
  readonly kind: string;
  /** The operation ID, such as `createRefund`. */
  readonly operationId: string;
  /** The input type's name, such as `CreateRefundInput`. */
  readonly inputType?: string;
  /** Model-facing description. */
  readonly description?: string;
  /** The JSON Schema of the input. */
  readonly inputSchema?: Readonly<Record<string, unknown>>;
  /** The agent review: `exposed`, `approval`, `retry`, and `branches`. */
  readonly agent?: {
    readonly exposed?: unknown;
    readonly approval?: unknown;
    readonly retry?: unknown;
    readonly branches?: unknown;
  };
}

/**
 * Options of {@link connectorTool}.
 * @typeParam Input - The operation's input type.
 * @since 0.4.0
 */
export interface ConnectorToolOptions<Input> {
  /**
   * The package's parsed `dex-connector.json`, such as
   * `import manifest from "@superdurable/connector-stripe/dex-connector.json" with { type: "json" }`.
   */
  readonly manifest: ConnectorPackageManifest;
  /** The validator the package exports for the operation's input, such as `CreateRefundInputSchema`. */
  readonly inputValidator: ConnectorInputValidator<Input>;
  /**
   * Tool name; defaults to `<connectorId>_<operation in snake case>`, such as `stripe_create_refund`. The snake
   * case is the one the Connector library uses for Python Step factory names: an acronym run stays one word, so
   * `listStoryIDs` becomes `list_story_ids` and `getHTTPResponse` becomes `get_http_response`.
   *
   * Changed in 0.5.0: the default keeps an acronym run as one word; it used to split `listStoryIDs` into
   * `list_story_i_d_s`.
   */
  readonly name?: string;
  /** Model-facing description; defaults to the manifest's. */
  readonly description?: string;
  /**
   * `true` or `false` overrides the manifest's `agent.approval` for every call; a predicate decides per call from
   * the decoded JSON arguments, and a predicate that throws requires approval. Omit it to follow the manifest.
   */
  readonly requiresApproval?: boolean | ApprovalPredicate;
  /** Bound on the result the model reads; defaults to the source's `maxResultBytes`. */
  readonly maxResultBytes?: number;
}

/**
 * One Connector operation offered to a Stepwise agent as a tool. Create it with {@link connectorTool}.
 * @since 0.4.0
 */
export interface ConnectorTool {
  /** Tool name the model calls. */
  readonly name: string;
  /** Tool description for the model. */
  readonly description: string;
  /** JSON Schema of the arguments, the manifest's `inputSchema`. */
  readonly inputSchema: Readonly<Record<string, unknown>>;
  /** The Connector ID. */
  readonly connectorId: string;
  /** The operation ID. */
  readonly operationId: string;
  /** `query` or `mutation`. */
  readonly kind: "query" | "mutation";
  /**
   * `read` for a Query, `idempotent_write` for a Mutation with `agent.retry: safe`, and `non_idempotent_write`
   * for any other Mutation.
   */
  readonly effect: ToolEffect;
  /** Whether the manifest says `agent.approval: recommended`. */
  readonly isApprovalRecommended: boolean;
  /** The application's approval choice, or `undefined` to follow the manifest. */
  readonly requiresApproval: boolean | ApprovalPredicate | undefined;
  /** Bound on the result the model reads, or `undefined` for the source's bound. */
  readonly maxResultBytes: number | undefined;
  /** The logical connection the operation runs with. */
  readonly connection: ConnectionRef;
  /** The manifest's `agent.branches`. */
  readonly agentBranches: Readonly<Record<string, string>>;
  /** The operation's branches that are not optional. */
  readonly requiredBranches: ReadonlySet<string>;
  /** The operation's Execute timeout in milliseconds, or `undefined`. */
  readonly executeMethodTimeout: number | undefined;
  /** The operation's Execute retry policy, which bounds a `CallTool` call's retries. */
  readonly retryPolicy: RetryPolicy;
  /** Validates, decodes, and runs one invocation. */
  readonly invoke: (call: Call, toolArguments: Readonly<Record<string, unknown>>) => Promise<QueryResult<unknown>>;
}

/**
 * Returns what a connector tool call records for one selected branch.
 *
 * The rules apply in this order, as the Connector library's manifest authoring guide defines them:
 *
 * 1. A required branch is `succeeded`.
 * 2. A branch that `agent.branches` lists as `rejected` is `rejected`.
 * 3. A branch that `agent.branches` lists as `unknown` is `outcome_unknown`.
 * 4. An unlisted `uncertain` is `outcome_unknown`.
 * 5. Any other optional branch of a Mutation is `outcome_unknown`. This includes `defect`: the Connector SDK can
 *    select `defect` after the provider was called, so the write may have happened.
 * 6. Any other optional branch of a Query, including `defect`, is `failed`.
 *
 * The rule fails closed: an optional branch of a Mutation that the manifest does not classify is
 * `outcome_unknown`, because the write may have happened.
 *
 * Changed in 0.5.0: `agent.branches` is read before `defect` and `uncertain`. An unlisted `defect` of a Mutation
 * is `outcome_unknown` instead of `failed`, and an `uncertain` listed as `rejected` is `rejected`.
 *
 * @example
 * ```ts
 * classifyConnectorBranch({ kind: "mutation", branch: "invalidResponse", requiredBranches: new Set(["refunded"]),
 *   agentBranches: {} }); // "outcome_unknown"
 * ```
 * @param options - The operation kind, the selected branch, the required branches, and `agent.branches`.
 * @returns The outcome to record.
 * @since 0.4.0
 */
export function classifyConnectorBranch(options: {
  readonly kind: "query" | "mutation";
  readonly branch: string;
  readonly requiredBranches: ReadonlySet<string>;
  readonly agentBranches: Readonly<Record<string, string>>;
}): ConnectorBranchOutcome {
  const { kind, branch, requiredBranches, agentBranches } = options;
  if (requiredBranches.has(branch)) {
    return "succeeded";
  }
  const listed = Object.hasOwn(agentBranches, branch) ? agentBranches[branch] : undefined;
  if (listed === "rejected") {
    return "rejected";
  }
  if (listed === "unknown" || branch === uncertainBranch || kind === "mutation") {
    return "outcome_unknown";
  }
  return "failed";
}

/**
 * Offers one reviewed Connector operation to a Stepwise agent as a tool.
 *
 * The tool's name, description, and argument schema come from the operation's entry in its package's
 * `dex-connector.json`. Only an operation whose entry says `agent.exposed: true` can become a tool; nothing is
 * exposed by default, and the application still names each operation it offers.
 *
 * The manifest decides how the call runs. A Query is `read` and runs in `CallTool`. A Mutation with
 * `agent.retry: safe` is `idempotent_write` and runs in `CallTool`, whose retries stay inside the operation's own
 * Execute retry policy and timeout. Any other Mutation is `non_idempotent_write` and runs once in
 * `CallSingleAttemptTool`. The effect always comes from the manifest; a session's `toolPolicies` entry for the
 * tool changes only its approval.
 *
 * A call waits for `resolve_interrupt` when the manifest says `agent.approval: recommended`;
 * {@link ConnectorToolOptions.requiresApproval} overrides that. Arguments that break the schema never run, so
 * they never wait for approval.
 *
 * Changed in 0.5.0: the default tool name keeps an acronym run as one word, as the Connector library does:
 * `listStoryIDs` becomes `list_story_ids`, not `list_story_i_d_s`.
 *
 * @example
 * ```ts
 * import manifest from "@superdurable/connector-stripe/dex-connector.json" with { type: "json" };
 * import { CreateRefundInputSchema, newProjectConnection } from "@superdurable/connector-stripe";
 *
 * const stripe = newProjectConnection(project, "billing-stripe");
 * const refund = connectorTool(stripe.operations.createRefund(), stripe.reference, {
 *   manifest,
 *   inputValidator: CreateRefundInputSchema,
 *   requiresApproval: (args) => Number(args["amount"]) > 5000,
 * });
 * const source = new ConnectorToolSource([refund]);
 * ```
 * @typeParam Input - The operation's input type.
 * @typeParam Output - The operation's result value type.
 * @param operation - The connector operation, a Query or a Mutation.
 * @param connection - The logical connection reference the call runs with. Only the reference is stored; the
 * Worker resolves credentials.
 * @param options - The manifest, the input validator, and the application's choices.
 * @returns The tool.
 * @throws {TypeError} When the manifest has no entry for the operation, the entry is not `agent.exposed: true`,
 * its kind differs from the operation's, its `agent` block holds an unknown value or lists a branch it may not,
 * or its `inputSchema` uses a keyword the agent core cannot check.
 * @throws {RangeError} When `maxResultBytes` is not positive.
 * @since 0.4.0
 */
export function connectorTool<Input, Output>(
  operation: Query<Input, Output> | Mutation<Input, Output>,
  connection: ConnectionRef,
  options: ConnectorToolOptions<Input>,
): ConnectorTool {
  const definition: QueryDefinition = operation.definition();
  const isMutation = typeof (operation as Partial<Mutation<Input, Output>>).idempotencyKey === "function";
  const kind = isMutation ? "mutation" : "query";
  const { connectorId, operationId } = definition.operation;
  const label = `${connectorId} ${operationId}`;
  const manifest = options.manifest;
  if (manifest.connectorId !== connectorId) {
    throw new TypeError(`${label}: the manifest describes Connector ${String(manifest.connectorId)}`);
  }
  const factory = manifest.factories.find((entry) => entry.operationId === operationId);
  if (factory === undefined) {
    throw new TypeError(`${label}: the manifest has no entry for this operation`);
  }
  const agent = factory.agent;
  if (typeof agent !== "object" || agent === null || agent.exposed !== true) {
    throw new TypeError(`${label} is not exposed to agents: its manifest lacks agent.exposed: true`);
  }
  if (factory.kind !== kind) {
    throw new TypeError(`${label}: the manifest says ${String(factory.kind)}, the operation is a ${kind}`);
  }
  const { approval, retry } = agent;
  const branches = agent.branches ?? {};
  if (approval !== undefined && approval !== "recommended") {
    throw new TypeError(`${label}: unknown agent.approval ${JSON.stringify(approval)}`);
  }
  if ((retry !== undefined && retry !== "safe") || (retry !== undefined && kind === "query")) {
    throw new TypeError(`${label}: unknown agent.retry ${JSON.stringify(retry)}`);
  }
  if (
    typeof branches !== "object" ||
    branches === null ||
    Array.isArray(branches) ||
    Object.values(branches).some((value) => value !== "rejected" && value !== "unknown")
  ) {
    throw new TypeError(`${label}: agent.branches values must be rejected or unknown`);
  }
  const agentBranches = branches as Readonly<Record<string, string>>;
  const optionalBranches = new Set(definition.branches.filter((branch) => branch.optional === true).map((branch) => branch.id));
  if (Object.keys(agentBranches).some((branch) => !optionalBranches.has(branch))) {
    throw new TypeError(`${label}: agent.branches may list only optional branches`);
  }
  if (kind === "query" && Object.values(agentBranches).includes("unknown")) {
    throw new TypeError(`${label}: agent.branches unknown is for Mutations only`);
  }
  const schema = factory.inputSchema;
  if (typeof schema !== "object" || schema === null) {
    throw new TypeError(`${label}: the manifest has no inputSchema`);
  }
  checkConnectorInputSchema(schema);
  if (options.maxResultBytes !== undefined && !(options.maxResultBytes >= 1)) {
    throw new RangeError("maxResultBytes must be positive");
  }
  const effect: ToolEffect =
    kind === "query" ? "read" : retry === "safe" ? "idempotent_write" : "non_idempotent_write";
  const inputValidator = options.inputValidator;
  const toolName = options.name ?? `${connectorId}_${lowerCamelToSnake(operationId)}`;
  const invoke = async (call: Call, toolArguments: Readonly<Record<string, unknown>>): Promise<QueryResult<unknown>> => {
    const parsed = inputValidator.safeParse(toolArguments);
    if (!parsed.success) {
      // A fixed text: validator messages differ between languages and versions.
      throw new ToolExecutionError(`invalid arguments for ${toolName}: arguments do not decode`);
    }
    return isMutation
      ? runMutation(operation as Mutation<Input, Output>, call, parsed.data)
      : runQuery(operation as Query<Input, Output>, call, parsed.data);
  };
  const timeout = definition.stepDefaults.executeMethodTimeoutMs;
  return {
    name: toolName,
    description: options.description ?? factory.description ?? "",
    inputSchema: schema,
    connectorId,
    operationId,
    kind,
    effect,
    isApprovalRecommended: approval === "recommended",
    requiresApproval: options.requiresApproval,
    maxResultBytes: options.maxResultBytes,
    connection,
    agentBranches: { ...agentBranches },
    requiredBranches: new Set(definition.branches.filter((branch) => branch.optional !== true).map((branch) => branch.id)),
    executeMethodTimeout: timeout === undefined || timeout === 0 ? undefined : timeout,
    retryPolicy: boundedRetryPolicy(definition.stepDefaults.executeRetry ?? defaultConnectorStepExecuteRetryPolicy()),
    invoke,
  };
}

/**
 * Offers reviewed Connector operations to a Stepwise agent as tools.
 *
 * Put it in `AgentDefinition.toolSources`, as `McpToolSource` is. It lists exactly the tools the application
 * passes; no Connector is exposed by default. Each call runs one operation invocation in its own tool Step, through
 * the Connector SDK's `runQuery` or `runMutation`, so the connector's Call ID, idempotency key, credential
 * handling, and single-dispatch fence apply. The Call ID is derived from the Flow ID and the tool Step's execution
 * ID, so it is the same on every attempt of that Step.
 *
 * Before anything runs, the model's arguments are checked against the manifest's `inputSchema` and then decoded
 * with the package's input validator. Either failure is a `failed` call the model can correct; the operation never
 * runs.
 *
 * Connector writes of one model response run one at a time in the model's order, and reads run in parallel. A
 * write whose approval is needed waits for it only when its turn comes. Once a write does not succeed, the later
 * writes of that response are recorded `failed` with `skipped` set.
 *
 * The selected branch maps as {@link classifyConnectorBranch} describes. A connector retry throws through the
 * Step, so the tool Step's retry policy applies. A single-attempt write that asks to retry ends `outcome_unknown`,
 * unless its single-dispatch fence saved a `notSent` or `canResend` marker in this attempt: that proves the
 * provider did not process the request, so the call records `failed` with {@link connectorNotSentRetryLater}.
 * Either way it stops the later writes. A result longer than `maxResultBytes` reaches the model truncated, with
 * {@link connectorResultTruncatedNote}, and the record's `truncatedOutputBytes` holds the full size.
 *
 * Install `@superdurable/connectors-sdk` next to this package; it is an optional peer dependency.
 *
 * @example
 * ```ts
 * const flow = createStepwiseAgentSessionFlow(
 *   { name: "support", instructions: "You help customers.", model,
 *     toolSources: [new ConnectorToolSource([refund, reply])] },
 *   "SupportAgent",
 * );
 * ```
 * @since 0.4.0
 */
export class ConnectorToolSource implements ToolSource {
  /** The source's stable name, stored with each call; keep it across deploys. */
  public readonly sourceName: string;
  private readonly toolsByName: ReadonlyMap<string, ConnectorTool>;
  private readonly definitions: readonly SourceToolDefinition[];
  private readonly maxResultBytes: number;

  /**
   * Creates the source.
   * @param tools - The tools to offer, in the order the model sees them.
   * @param options - `sourceName`, default {@link defaultConnectorSourceName}, and `maxResultBytes`, default
   * {@link defaultConnectorResultMaxBytes}.
   * @throws {TypeError} When two tools share a name or `sourceName` is blank.
   * @throws {RangeError} When `maxResultBytes` is not positive.
   */
  public constructor(
    tools: readonly ConnectorTool[],
    options: { readonly sourceName?: string; readonly maxResultBytes?: number } = {},
  ) {
    const sourceName = options.sourceName ?? defaultConnectorSourceName;
    if (sourceName.trim() === "") {
      throw new TypeError("sourceName must not be blank");
    }
    const maxResultBytes = options.maxResultBytes ?? defaultConnectorResultMaxBytes;
    if (!(maxResultBytes >= 1)) {
      throw new RangeError("maxResultBytes must be positive");
    }
    this.toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
    if (this.toolsByName.size !== tools.length) {
      throw new TypeError("two connector tools share a name");
    }
    this.sourceName = sourceName;
    this.maxResultBytes = maxResultBytes;
    this.definitions = tools.map((tool) => ({
      name: tool.name,
      description: tool.description,
      inputSchema: tool.inputSchema,
    }));
  }

  /**
   * Returns the tools; the list is fixed when the source is built.
   * @returns The tool definitions.
   */
  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return this.definitions;
  }

  /**
   * Returns the tools without I/O.
   * @returns The tool definitions.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.definitions;
  }

  /**
   * Returns every tool name, so the runner refuses a collision.
   * @returns The names.
   */
  public declaredToolNames(): ReadonlySet<string> {
    return new Set(this.toolsByName.keys());
  }

  /**
   * Builds the call with the manifest's effect and the call's approval.
   * @param turn - The `RunModel` context; a `toolPolicies` entry for the tool changes only its approval.
   * @param call - The model's tool call.
   * @returns The deferred call, ordered when it is a write.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall {
    const tool = this.requireTool(call.toolName);
    const sessionPolicy =
      turn.toolPolicies !== undefined && Object.hasOwn(turn.toolPolicies, call.toolName)
        ? turn.toolPolicies[call.toolName]
        : undefined;
    const requiresApproval = sessionPolicy?.requiresApproval ?? requiresApprovalOf(tool, call.arguments);
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: { effect: tool.effect, requiresApproval },
      toolSourceReference: serializeJsonWithSpacedSeparators({ tool: tool.name }),
      ordered: tool.effect !== "read",
    };
  }

  /**
   * Returns the operation's timeout and retry policy for the call's tool Step.
   * @param toolCall - The record whose tool Step starts.
   * @returns The options, or `undefined` when this source no longer has the tool.
   */
  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    const tool = this.toolsByName.get(referenceToolName(toolCall));
    if (tool === undefined) {
      return undefined;
    }
    return {
      ...(tool.executeMethodTimeout === undefined ? {} : { executeMethodTimeout: tool.executeMethodTimeout }),
      executeRetry: tool.retryPolicy,
    };
  }

  /**
   * Checks, decodes, and runs one call, then maps the selected branch.
   * @param call - The approved call, with the tool Step's live context.
   * @returns The result value as JSON text, bounded by `maxResultBytes`.
   * @throws {ToolExecutionError} When the arguments break the schema or do not decode, the operation selected a
   * `rejected` or failed branch, or a single-attempt write asked to retry after its fence recorded `notSent` or
   * `canResend` in this attempt, which proves the provider did not process it ({@link connectorNotSentRetryLater}).
   * @throws {ToolOutcomeUnknownError} When the operation selected an unknown branch.
   * @throws {RangeError} When this source no longer has the tool, so the effect decides the outcome.
   * @throws Any other error, such as a connector retry for the tool Step's retry policy.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const record = call.toolCall;
    const tool = this.toolsByName.get(referenceToolName(record));
    if (tool === undefined) {
      throw new RangeError(`connector tool ${record.toolName} is not configured on this Worker`);
    }
    const toolArguments = parseToolArguments(tool.name, record.arguments);
    const problem = findConnectorInputViolation(toolArguments, tool.inputSchema);
    if (problem !== undefined) {
      throw new ToolExecutionError(`invalid arguments for ${tool.name}: ${problem}`);
    }
    if (call.context === undefined) {
      throw new Error("a connector tool call needs the tool Step's context");
    }
    const stepContext = new ConnectorStepContext(call.context, {
      previousCheckpoint: call.heartbeatCheckpoint,
      heartbeat: () => call.heartbeat(),
      ...(call.saveHeartbeatCheckpoint === undefined
        ? {}
        : { saveCheckpoint: (checkpoint: string) => call.saveHeartbeatCheckpoint!(checkpoint) }),
    });
    const connectorCall = createCall(stepContext as unknown as AsyncContext, tool.connection, {
      connectorId: tool.connectorId,
      operationId: tool.operationId,
    });
    let result: QueryResult<unknown>;
    try {
      result = await tool.invoke(connectorCall, toolArguments);
    } catch (error: unknown) {
      if (error instanceof RetryError) {
        if (tool.effect === "non_idempotent_write" && isProvenNotSent(connectorCall, stepContext)) {
          // One attempt only, so a Retry would record outcome_unknown though nothing was sent.
          throw new ToolExecutionError(notSentDetail(tool, error.failure));
        }
        throwDexRetry(error);
      }
      throw error;
    }
    const outcome = classifyConnectorBranch({
      kind: tool.kind,
      branch: result.branch,
      requiredBranches: tool.requiredBranches,
      agentBranches: tool.agentBranches,
    });
    if (outcome === "succeeded") {
      return boundConnectorResult(encodeResultValue(result.value), tool.maxResultBytes ?? this.maxResultBytes);
    }
    const detail = branchDetail(tool, result.branch, result.failure);
    if (outcome === "outcome_unknown") {
      throw new ToolOutcomeUnknownError(detail);
    }
    throw new ToolExecutionError(detail);
  }

  /**
   * Refuses a poll: connector tools never start tool tasks.
   * @param poll - The call.
   * @returns Never.
   * @throws {RangeError} Always.
   */
  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    throw new RangeError(`connector tool ${poll.toolCall.toolName} starts no task`);
  }

  private requireTool(toolName: string): ConnectorTool {
    const tool = this.toolsByName.get(toolName);
    if (tool === undefined) {
      throw new RangeError(`connector tool ${toolName} is not configured on this Worker`);
    }
    return tool;
  }
}

/**
 * Returns `output` whole, or its first `maxResultBytes` bytes and {@link connectorResultTruncatedNote}.
 * @param output - The full result text.
 * @param maxResultBytes - The bound, in bytes of UTF-8 text, on the kept part.
 * @returns The result, with `truncatedOutputBytes` set when it was cut.
 * @since 0.4.0
 */
export function boundConnectorResult(output: string, maxResultBytes: number): ToolExecutionResult {
  const encoded = Buffer.from(output, "utf8");
  if (encoded.length <= maxResultBytes) {
    return { output };
  }
  let end = maxResultBytes;
  // A cut inside a character drops its partial bytes, as Python's "ignore" decoding does.
  while (end > 0 && ((encoded[end] ?? 0) & 0xc0) === 0x80) {
    end -= 1;
  }
  const kept = encoded.subarray(0, end).toString("utf8");
  const note = connectorResultTruncatedNote.replace("{total_bytes}", String(encoded.length));
  return { output: `${kept}\n${note}`, truncatedOutputBytes: encoded.length };
}

/**
 * Returns the text the model reads for one result value, the same in every core: keys sorted by code point, no
 * spaces, unescaped UTF-8, and a number with no fractional part written as an integer.
 * @param jsonText - The result value as JSON text.
 * @returns The canonical text.
 * @since 0.4.0
 */
export function formatConnectorResult(jsonText: string): string {
  return JSON.stringify(sortObjectKeys(JSON.parse(jsonText)));
}

function sortObjectKeys(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortObjectKeys);
  }
  if (typeof value === "object" && value !== null) {
    const record = value as Readonly<Record<string, unknown>>;
    return Object.fromEntries(
      Object.keys(record)
        .sort(compareCodePoints)
        .map((key) => [key, sortObjectKeys(record[key])]),
    );
  }
  return value;
}

function encodeResultValue(value: unknown): string {
  return value === undefined || value === null ? "null" : formatConnectorResult(JSON.stringify(value));
}

function requiresApprovalOf(tool: ConnectorTool, toolArguments: string): boolean {
  const choice = tool.requiresApproval;
  if (choice === undefined) {
    return tool.isApprovalRecommended;
  }
  if (typeof choice === "boolean") {
    return choice;
  }
  let values: unknown;
  try {
    values = JSON.parse(toolArguments === "" ? "{}" : toolArguments);
  } catch {
    return false;
  }
  if (!isRecord(values) || findConnectorInputViolation(values, tool.inputSchema) !== undefined) {
    // The tool Step refuses these arguments before the operation runs.
    return false;
  }
  try {
    return Boolean(choice(values));
  } catch {
    return true;
  }
}

function parseToolArguments(toolName: string, toolArguments: string): Readonly<Record<string, unknown>> {
  let values: unknown;
  try {
    values = JSON.parse(toolArguments === "" ? "{}" : toolArguments);
  } catch {
    throw new ToolExecutionError(`invalid arguments for ${toolName}: arguments are not valid JSON`);
  }
  if (!isRecord(values)) {
    throw new ToolExecutionError(`invalid arguments for ${toolName}: expected a JSON object`);
  }
  return values;
}

/** Reports whether this attempt's last saved fence marker proves the write was not sent. */
export function isProvenNotSent(call: Call, context: ConnectorStepContext): boolean {
  const marker = readSavedHeartbeatValue(context);
  if (!isRecord(marker)) {
    return false;
  }
  return (
    marker["callId"] === call.id &&
    marker["attempt"] === context.attempt &&
    (marker["state"] === "notSent" || marker["state"] === "canResend") &&
    (marker["providerId"] === undefined || marker["providerId"] === "")
  );
}

function notSentDetail(tool: ConnectorTool, failure: Failure | undefined): string {
  const detail = `${tool.connectorId} ${tool.operationId} ${connectorNotSentRetryLater}`;
  return failure === undefined ? detail : `${detail}: ${failure.kind}: ${failure.message}`;
}

function branchDetail(tool: ConnectorTool, branch: string, failure: Failure | undefined): string {
  const detail = `${tool.connectorId} ${tool.operationId} returned ${branch}`;
  return failure === undefined ? detail : `${detail}: ${failure.kind}: ${failure.message}`;
}

function referenceToolName(record: AgentToolCall): string {
  try {
    const reference: unknown = JSON.parse(record.toolSourceReference ?? "");
    const name = isRecord(reference) ? reference["tool"] : undefined;
    return typeof name === "string" ? name : record.toolName;
  } catch {
    return record.toolName;
  }
}

function boundedRetryPolicy(policy: {
  readonly initialIntervalMs: number;
  readonly backoffCoefficient: number;
  readonly maximumIntervalMs: number;
  readonly maximumAttempts: number;
  readonly totalDurationMs: number;
}): RetryPolicy {
  const attempts = policy.maximumAttempts;
  return {
    initialIntervalMs: policy.initialIntervalMs,
    backoffCoefficient: policy.backoffCoefficient,
    maximumIntervalMs: policy.maximumIntervalMs,
    maximumAttempts: attempts <= 0 ? maximumToolAttempts : Math.min(attempts, maximumToolAttempts),
    totalDurationMs: policy.totalDurationMs,
  };
}

/**
 * Returns an operation ID in snake case, as connectorctl `lowerCamelToSnake` names the Python Step factories.
 * @param operationId - The lower camel case operation ID, such as `listStoryIDs`.
 * @returns The snake case name, such as `list_story_ids`.
 */
export function lowerCamelToSnake(operationId: string): string {
  const characters = Array.from(operationId);
  let converted = "";
  for (const [index, character] of characters.entries()) {
    if (!isAsciiUpper(character)) {
      converted += character;
      continue;
    }
    const previous = characters[index - 1];
    const following = characters[index + 1];
    const isPreviousLowerOrDigit = isAsciiLower(previous) || isAsciiDigit(previous);
    // A trailing lowercase s pluralizes the acronym, as in listStoryIDs.
    const isAcronymPlural = following === "s" && !isAsciiLower(characters[index + 2]);
    const isEndOfAcronym = isAsciiUpper(previous) && isAsciiLower(following) && !isAcronymPlural;
    if (isPreviousLowerOrDigit || isEndOfAcronym) {
      converted += "_";
    }
    converted += character.toLowerCase();
  }
  return converted;
}

function isAsciiUpper(character: string | undefined): boolean {
  return character !== undefined && character >= "A" && character <= "Z";
}

function isAsciiLower(character: string | undefined): boolean {
  return character !== undefined && character >= "a" && character <= "z";
}

function isAsciiDigit(character: string | undefined): boolean {
  return character !== undefined && character >= "0" && character <= "9";
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
