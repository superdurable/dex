// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  ChannelCondition,
  CloseDecision,
  CloseDecisionType,
  ConditionCombination as ProtoConditionCombination,
  ExecuteMethodFailurePolicy,
  InvokeExecuteMethodResponse,
  InvokeWaitForMethodResponse,
  InvokeWorkerRPCResponse,
  StepDecision as ProtoStepDecision,
  StepMovement as ProtoStepMovement,
  StepOptions as ProtoStepOptions,
  TimerCondition,
  SubFlowCondition as ProtoSubFlowCondition,
  SubFlowOptions as ProtoSubFlowOptions,
  SubFlowReusePolicy as ProtoSubFlowReusePolicy,
  FlowRetryPolicy,
  FlowConfig as ProtoFlowConfig,
  ActiveStepSearchMode as ProtoActiveStepSearchMode,
  StepDurability as ProtoStepDurability,
  IndexType as ProtoIndexType,
  WaitForMethodFailurePolicy,
  WaitingCondition,
  WaitingConditionType,
  type AttributeWrite,
  type ChannelMessage,
  type ConditionResults,
  type InvokeExecuteMethodRequest,
  type InvokeWaitForMethodRequest,
  type InvokeWorkerRPCRequest,
  type KV,
  type RetryPolicy as ProtoRetryPolicy,
  type Value,
} from "./gen/dex.js";
import { InvalidStepResultError, ValueMappingError } from "./errors.js";
import {
  registeredFlowByName,
  registeredFlow,
  registeredRPCByName,
  registeredStep,
  type RegisteredFlow,
  type RegisteredStep,
  type Registry,
} from "./flow.js";
import { InvocationContext } from "./invocation-context.js";
import { Attribute, AttributeMap, IndexType } from "./persistence.js";
import { mapAttributeStoreName, mapAttributeStoreSync } from "./attribute-store-sync.js";
import { ActiveStepSearchMode, type FlowConfig } from "./options.js";
import { SubFlowReusePolicy, type SubFlowOptions } from "./subflow.js";
import type { RegisteredRPC } from "./rpc.js";
import type {
  RetryPolicy,
  StepDecision,
  StepMovement,
  StepOptions,
} from "./step.js";
import {
  decodeValue,
  encodeUnknown,
  encodeValue,
  type ValueHydrator,
} from "./value-mapper.js";
import { Channel, ChannelMap, type Condition, type Wait } from "./wait.js";


export class WorkerDispatcher {
  public constructor(
    private readonly registry: Registry,
    private readonly hydrator: ValueHydrator,
  ) {}

  public async invokeWaitFor(
    original: InvokeWaitForMethodRequest,
  ): Promise<InvokeWaitForMethodResponse> {
    const request = await this.hydrateWaitFor(original);
    const flow = registeredFlowByName(this.registry, request.flowType);
    const step = registeredStep(flow, request.stepType);
    const context = new InvocationContext("waitFor", flow, request.context, request.attributes);
    const input = decodeValue(step.step.inputCodec, requireValue(request.stepInput, "Step input"));
    if (step.step.waitFor === undefined) {
      throw new TypeError(`Step ${step.name} does not implement waitFor`);
    }
    const wait = await step.step.waitFor(context, input);
    try {
      return InvokeWaitForMethodResponse.create({
        upsertAttributes: [...context.getAttributeWrites()],
        waitingCondition: mapWait(this.registry, flow, wait),
        upsertStepExeLocals: [...context.getLocalWrites()],
        recordEvents: [...context.getEvents()],
        publishToChannel: [...context.getPublications()],
      });
    } catch (failure) {
      throw invalidStepResult(flow.name, step.name, "waitFor", failure);
    }
  }

  public async invokeExecute(
    original: InvokeExecuteMethodRequest,
  ): Promise<InvokeExecuteMethodResponse> {
    const request = await this.hydrateExecute(original);
    const flow = registeredFlowByName(this.registry, request.flowType);
    const step = registeredStep(flow, request.stepType);
    const context = new InvocationContext(
      "execute",
      flow,
      request.context,
      request.attributes,
      request.stepExeLocals,
      request.conditionResults,
    );
    const input = decodeValue(step.step.inputCodec, requireValue(request.stepInput, "Step input"));
    const decision = await step.step.execute(context, input);
    try {
      return InvokeExecuteMethodResponse.create({
        stepDecision: mapDecision(flow, decision),
        upsertAttributes: [...context.getAttributeWrites()],
        recordEvents: [...context.getEvents()],
        upsertStepExeLocals: [...context.getLocalWrites()],
        publishToChannel: [...context.getPublications()],
      });
    } catch (failure) {
      throw invalidStepResult(flow.name, step.name, "execute", failure);
    }
  }

  public async invokeRPC(original: InvokeWorkerRPCRequest): Promise<InvokeWorkerRPCResponse> {
    const request = await this.hydrateRPC(original);
    const flow = registeredFlowByName(this.registry, request.flowType);
    const rpc = registeredRPCByName(flow, request.rpcName);
    const context = new InvocationContext(
      "rpc",
      flow,
      request.context,
      request.attributes,
      [],
      undefined,
      request.channelInfos,
    );
    const returned = await invokeRPC(flow, rpc, context, request.input);
    try {
      const result = rpcResult(rpc, returned);
      return InvokeWorkerRPCResponse.create({
        output:
          rpc.options.outputCodec === undefined
            ? encodeUnknown(undefined)
            : encodeValue(rpc.options.outputCodec, result?.output),
        stepDecision:
          result?.nextSteps === undefined || result.nextSteps.length === 0
            ? undefined
            : ProtoStepDecision.create({ nextSteps: mapMovements(flow, result.nextSteps) }),
        upsertAttributes: [...context.getAttributeWrites()],
        recordEvents: [...context.getEvents()],
        publishToChannel: [...context.getPublications()],
      });
    } catch (failure) {
      throw invalidStepResult(flow.name, undefined, "rpc", failure);
    }
  }

  private async hydrateWaitFor(
    request: InvokeWaitForMethodRequest,
  ): Promise<InvokeWaitForMethodRequest> {
    const values = await this.hydrator.hydrateAll([
      request.stepInput,
      ...request.attributes.map((entry) => entry.value),
    ]);
    return {
      ...request,
      stepInput: values[0],
      attributes: replaceEntryValues(request.attributes, values.slice(1)),
    };
  }

  private async hydrateExecute(
    request: InvokeExecuteMethodRequest,
  ): Promise<InvokeExecuteMethodRequest> {
    const channelValues = request.conditionResults?.channelResults.flatMap(
      (result) => result.values,
    ) ?? [];
    const subFlowValues = request.conditionResults?.subFlowResults.flatMap(
      (result) => result.results.map((completion) => completion.completedStepOutput),
    ) ?? [];
    const values = await this.hydrator.hydrateAll([
      request.stepInput,
      ...request.attributes.map((entry) => entry.value),
      ...request.stepExeLocals.map((entry) => entry.value),
      ...channelValues,
      ...subFlowValues,
    ]);
    let offset = 1;
    const attributes = replaceEntryValues(
      request.attributes,
      values.slice(offset, (offset += request.attributes.length)),
    );
    const stepExeLocals = replaceEntryValues(
      request.stepExeLocals,
      values.slice(offset, (offset += request.stepExeLocals.length)),
    );
    const conditionResults = replaceConditionValues(
      request.conditionResults,
      values.slice(offset),
    );
    return { ...request, attributes, stepExeLocals, conditionResults };
  }

  private async hydrateRPC(request: InvokeWorkerRPCRequest): Promise<InvokeWorkerRPCRequest> {
    const hasInput = request.input !== undefined;
    const values = await this.hydrator.hydrateAll([
      ...(hasInput ? [request.input] : []),
      ...request.attributes.map((entry) => entry.value),
    ]);
    return {
      ...request,
      input: hasInput ? values[0] : undefined,
      attributes: replaceEntryValues(request.attributes, values.slice(hasInput ? 1 : 0)),
    };
  }
}

function invokeRPC(
  flow: RegisteredFlow,
  rpc: RegisteredRPC,
  context: InvocationContext,
  input: Value | undefined,
): unknown {
  if (rpc.options.inputCodec === undefined) {
    return rpc.method.call(flow.flow, context);
  }
  return rpc.method.call(
    flow.flow,
    context,
    decodeValue(rpc.options.inputCodec, requireValue(input, "RPC input")),
  );
}

function rpcResult(
  rpc: RegisteredRPC,
  returned: unknown,
): { output: unknown; nextSteps?: readonly StepMovement<unknown>[] } | undefined {
  if (rpc.options.outputCodec === undefined) {
    if (returned !== undefined) {
      throw new TypeError(`procedure RPC ${rpc.name} must not return a value`);
    }
    return undefined;
  }
  if (typeof returned !== "object" || returned === null || !("output" in returned)) {
    throw new TypeError(`function RPC ${rpc.name} must return RPCResult`);
  }
  return returned as { output: unknown; nextSteps?: readonly StepMovement<unknown>[] };
}

function mapWait(
  registry: Registry,
  flow: RegisteredFlow,
  wait: Wait | undefined,
): WaitingCondition | undefined {
  if (wait === undefined) {
    throw new TypeError("waitFor returned undefined");
  }
  if (wait.kind === "skipImmediately") {
    return undefined;
  }
  const mapper = new ConditionMapper(registry, flow);
  let waitingConditionType: WaitingConditionType;
  const combinations: ProtoConditionCombination[] = [];
  if (wait.kind === "allOf" || wait.kind === "anyOf") {
    if (wait.conditions.length === 0) {
      throw new TypeError("Wait requires at least one Condition");
    }
    waitingConditionType =
      wait.kind === "allOf"
        ? WaitingConditionType.WAITING_CONDITION_TYPE_ALL_COMPLETED
        : WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMPLETED;
    for (const condition of wait.conditions) {
      mapper.add(condition, false);
    }
  } else {
    waitingConditionType =
      WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED;
    for (const combination of wait.combinations) {
      combinations.push(
        ProtoConditionCombination.create({
          conditionIds: combination.conditions.map((condition) => mapper.add(condition, true)),
        }),
      );
    }
  }
  return WaitingCondition.create({
    waitingConditionType,
    timerConditions: mapper.timers,
    channelConditions: mapper.channels,
    subFlowConditions: mapper.subFlows,
    conditionCombinations: combinations,
  });
}

function mapDecision(flow: RegisteredFlow, decision: StepDecision | undefined): ProtoStepDecision {
  if (decision === undefined) {
    throw new TypeError("execute returned undefined");
  }
  if (decision.kind === "next") {
    if (decision.movements.length === 0) {
      throw new TypeError("goToMulti requires a movement");
    }
    return ProtoStepDecision.create({ nextSteps: mapMovements(flow, decision.movements) });
  }
  if (decision.kind === "deadEnd") {
    return ProtoStepDecision.create({
      closeDecision: CloseDecision.create({
        closeDecisionType: CloseDecisionType.CLOSE_DECISION_TYPE_DEAD_END,
      }),
    });
  }
  if (decision.kind === "forceCompleteIfChannelsEmpty") {
    const channels = decision.channels.map((channel) => {
      if (!(channel instanceof Channel) || channel instanceof ChannelMap) {
        throw new TypeError("conditional close requires static Channels");
      }
      return channel.name;
    });
    return ProtoStepDecision.create({
      nextSteps: [mapMovement(flow, decision.fallback)],
      closeDecision: CloseDecision.create({
        closeDecisionType:
          CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
        conditionalChannelNames: channels,
        closeInput: encodeUnknown(decision.output),
      }),
    });
  }
  const closeTypes = {
    gracefulComplete: CloseDecisionType.CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
    forceComplete: CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_COMPLETE,
    forceFail: CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_FAIL,
  } as const;
  const closeInput = decision.kind === "forceFail"
    ? encodeUnknown(decision.reason)
    : decision.output === undefined
      ? undefined
      : encodeUnknown(decision.output);
  return ProtoStepDecision.create({
    closeDecision: CloseDecision.create({
      closeDecisionType: closeTypes[decision.kind],
      closeInput,
    }),
  });
}

function mapMovements(
  flow: RegisteredFlow,
  movements: readonly StepMovement<unknown>[],
): ProtoStepMovement[] {
  return movements.map((movement) => mapMovement(flow, movement));
}

function mapMovement(flow: RegisteredFlow, movement: StepMovement<unknown>): ProtoStepMovement {
  const target = flow.steps.find((candidate) => candidate.step === movement.step);
  if (target === undefined) {
    throw new TypeError("Step movement target does not belong to Flow");
  }
  return ProtoStepMovement.create({
    stepType: target.name,
    stepInput: encodeValue(target.step.inputCodec, movement.input),
    stepOptions: mapStepOptions(
      flow,
      movement.options ?? target.step.getStepOptions?.(),
      target.step.waitFor === undefined,
    ),
  });
}

function mapStepOptions(
  flow: RegisteredFlow,
  options: StepOptions | undefined,
  skipWaitFor: boolean,
): ProtoStepOptions {
  const failureTarget = options?.executeFailure?.step;
  const failureDefinition =
    failureTarget === undefined
      ? undefined
      : flow.steps.find((candidate) => candidate.step === failureTarget);
  if (failureTarget !== undefined && failureDefinition === undefined) {
    throw new TypeError("execute failure Step must belong to the Flow");
  }
  const failureOptions = options?.executeFailure?.options ?? failureDefinition?.step.getStepOptions?.();
  return ProtoStepOptions.create({
    waitForTimeoutSeconds: seconds(options?.waitForMethodTimeoutMs),
    executeTimeoutSeconds: seconds(options?.executeMethodTimeoutMs),
    waitForRetryPolicy: mapRetry(options?.waitForRetry),
    executeRetryPolicy: mapRetry(options?.executeRetry),
    waitForFailurePolicy:
      options?.waitForFailure === "proceed"
        ? WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE
        : options?.waitForFailure === "failFlow"
          ? WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE
          : WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_UNSPECIFIED,
    executeFailurePolicy:
      failureDefinition === undefined
        ? ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED
        : ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
    executeFailureProceedStepType: failureDefinition?.name ?? "",
    executeFailureProceedStepOptions:
      failureDefinition === undefined
        ? undefined
        : mapStepOptions(
            flow,
            failureOptions,
            failureDefinition.step.waitFor === undefined,
          ),
    skipWaitFor,
    waitForDurabilityOverride:
      options?.waitForDurability === "sync"
        ? 1
        : options?.waitForDurability === "async"
          ? 2
          : 0,
    executeDurabilityOverride:
      options?.executeDurability === "sync"
        ? 1
        : options?.executeDurability === "async"
          ? 2
          : 0,
    waitForLockAttributeKeys: (options?.waitForLockAttributes ?? []).map((lock) =>
      lock.instance === undefined
        ? lock.attribute.name
        : physicalName(lock.attribute.name, lock.instance),
    ),
    executeLockAttributeKeys: (options?.executeLockAttributes ?? []).map((lock) =>
      lock.instance === undefined
        ? lock.attribute.name
        : physicalName(lock.attribute.name, lock.instance),
    ),
  });
}

function mapRetry(retry: RetryPolicy | undefined): ProtoRetryPolicy | undefined {
  if (retry === undefined) {
    return undefined;
  }
  return {
    initialIntervalSeconds: seconds(retry.initialIntervalMs),
    backoffCoefficient: retry.backoffCoefficient ?? 0,
    maximumIntervalSeconds: seconds(retry.maximumIntervalMs),
    maximumAttempts: retry.maximumAttempts ?? 0,
    totalDurationSeconds: seconds(retry.totalDurationMs),
  };
}

class ConditionMapper {
  public readonly timers: TimerCondition[] = [];
  public readonly channels: ChannelCondition[] = [];
  public readonly subFlows: ProtoSubFlowCondition[] = [];
  private readonly ids = new Map<Condition, string>();
  private readonly used = new Set<string>();

  public constructor(
    private readonly registry: Registry,
    private readonly flow: RegisteredFlow,
  ) {}

  public add(condition: Condition, idRequired: boolean): string {
    const existing = this.ids.get(condition);
    if (existing !== undefined) {
      return existing;
    }
    const id = condition.conditionId ?? "";
    if (idRequired && id === "") {
      throw new TypeError("anyCombinationOf requires every Condition to have an ID");
    }
    if (condition.conditionId !== undefined && id === "") {
      throw new TypeError("empty Condition ID");
    }
    if (id !== "" && this.used.has(id)) {
      throw new TypeError("duplicate Condition ID");
    }
    if (id !== "") {
      this.used.add(id);
    }
    if (condition.kind === "timer") {
      this.timers.push(
        TimerCondition.create({
          conditionId: id,
          durationSeconds: BigInt(seconds(condition.durationMs)),
        }),
      );
    } else if (condition.kind === "channel") {
      const registered = this.flow.persistence.get(condition.channelName ?? "");
      if (!(registered instanceof Channel) && !(registered instanceof ChannelMap)) {
        throw new TypeError(`Channel is not registered: ${condition.channelName ?? ""}`);
      }
      const channelName =
        registered instanceof ChannelMap
          ? physicalName(registered.name, condition.instance)
          : registered.name;
      this.channels.push(
        ChannelCondition.create({
          conditionId: id,
          channelName,
          atLeast: condition.atLeast,
          atMost: condition.atMost,
        }),
      );
    } else {
      const targetFlow = condition.subFlow;
      if (targetFlow === undefined) {
        throw new TypeError("SubFlow requires a target Flow");
      }
      const target = registeredFlow(this.registry, targetFlow);
      const start = target.startStep;
      if (start === undefined) {
        throw new TypeError(`SubFlow ${target.name} has no starting Step`);
      }
      const options = condition.subFlowOptions ?? {};
      this.subFlows.push(
        ProtoSubFlowCondition.create({
          conditionId: id,
          subFlowType: target.name,
          startStepType: start.name,
          stepInput: encodeValue(start.step.inputCodec, condition.subFlowInput),
          stepOptions: mapStepOptions(
            target,
            start.step.getStepOptions?.(),
            start.step.waitFor === undefined,
          ),
          options: mapSubFlowOptions(target, options),
          subFlowIndex: this.subFlows.length,
        }),
      );
    }
    this.ids.set(condition, id);
    return id;
  }
}

function mapSubFlowOptions(
  target: RegisteredFlow,
  options: SubFlowOptions,
): ProtoSubFlowOptions {
  const attributes = (options.attributes ?? []).map((initial) => {
    if (target.persistence.get(initial.attribute.name) !== initial.attribute) {
      throw new TypeError(`SubFlow Attribute does not belong to ${target.name}`);
    }
    let key: string;
    if (initial.attribute instanceof AttributeMap) {
      key = physicalName(initial.attribute.name, initial.instance);
    } else {
      if (initial.instance !== undefined) {
        throw new TypeError("static Attribute cannot use an instance");
      }
      key = initial.attribute.name;
    }
    return {
      key,
      value: encodeValue(initial.attribute.codec, initial.value),
      indexConfig: mapIndex(initial.attribute.index),
      syncConfig: mapAttributeStoreSync(initial.attribute),
    };
  });
  const reusePolicy =
    options.reusePolicy === SubFlowReusePolicy.ATTACH
      ? ProtoSubFlowReusePolicy.SUB_FLOW_REUSE_POLICY_ATTACH
      : options.reusePolicy === SubFlowReusePolicy.ALWAYS_RESTART
        ? ProtoSubFlowReusePolicy.SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART
        : ProtoSubFlowReusePolicy.SUB_FLOW_REUSE_POLICY_RESTART_IF_PREVIOUS_EXITS_ABNORMALLY;
  return ProtoSubFlowOptions.create({
    reusePolicy,
    flowTimeoutSeconds: seconds(options.timeoutMs),
    flowStartDelaySeconds: seconds(options.startDelayMs),
    retryPolicy: mapFlowRetry(options.retryPolicy),
    attributes,
    flowConfigOverride: mapSubFlowConfig(options.configOverride),
  });
}

function mapFlowRetry(retry: RetryPolicy | undefined): FlowRetryPolicy | undefined {
  if (retry === undefined) return undefined;
  return FlowRetryPolicy.create({
    initialIntervalSeconds: seconds(retry.initialIntervalMs),
    backoffCoefficient: retry.backoffCoefficient ?? 0,
    maximumIntervalSeconds: seconds(retry.maximumIntervalMs),
    maximumAttempts: retry.maximumAttempts ?? 0,
  });
}

function mapSubFlowConfig(config: FlowConfig | undefined): ProtoFlowConfig | undefined {
  if (config === undefined) return undefined;
  return ProtoFlowConfig.create({
    activeStepSearchMode:
      config.activeStepSearchMode === ActiveStepSearchMode.ALL
        ? ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
        : config.activeStepSearchMode === undefined
          ? undefined
          : ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED,
    attributeSyncConfigName: mapAttributeStoreName(config),
    continueAsNewThreshold: config.continueAsNewThreshold,
    continueAsNewPageSizeInBytes: config.continueAsNewPageSizeBytes,
    stepDurability:
      config.stepDurability === "sync"
        ? ProtoStepDurability.STEP_DURABILITY_SYNC
        : config.stepDurability === "async"
          ? ProtoStepDurability.STEP_DURABILITY_ASYNC
          : undefined,
    workerTarget:
      config.workerTarget === undefined
        ? undefined
        : {
            address: config.workerTarget.address,
            isHeadlessAddress: config.workerTarget.headless ?? false,
          },
  });
}

function mapIndex(index: Attribute<unknown>["index"] | undefined) {
  if (index === undefined) return undefined;
  const types: Record<IndexType, ProtoIndexType> = {
    [IndexType.KEYWORD]: ProtoIndexType.INDEX_TYPE_KEYWORD,
    [IndexType.FULL_TEXT]: ProtoIndexType.INDEX_TYPE_TEXT,
    [IndexType.KEYWORD_ARRAY]: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
    [IndexType.INT]: ProtoIndexType.INDEX_TYPE_INT,
    [IndexType.DOUBLE]: ProtoIndexType.INDEX_TYPE_DOUBLE,
    [IndexType.BOOL]: ProtoIndexType.INDEX_TYPE_BOOL,
    [IndexType.DATETIME]: ProtoIndexType.INDEX_TYPE_DATETIME,
  };
  return { enable: true, type: types[index.type], indexKey: index.indexKey ?? "" };
}

function replaceEntryValues(entries: readonly KV[], values: readonly Value[]): KV[] {
  if (entries.length !== values.length) {
    throw new TypeError("hydrated entry count does not match request");
  }
  return entries.map((entry, index) => ({ ...entry, value: values[index] }));
}

function replaceConditionValues(
  results: ConditionResults | undefined,
  values: readonly Value[],
): ConditionResults | undefined {
  if (results === undefined) {
    if (values.length !== 0) {
      throw new TypeError("unexpected hydrated Condition values");
    }
    return undefined;
  }
  let offset = 0;
  const channelResults = results.channelResults.map((result) => {
    const next = offset + result.values.length;
    const replaced = { ...result, values: values.slice(offset, next) };
    offset = next;
    return replaced;
  });
  const subFlowResults = results.subFlowResults.map((result) => ({
    ...result,
    results: result.results.map((completion) => ({
      ...completion,
      completedStepOutput: values[offset++],
    })),
  }));
  if (offset !== values.length) {
    throw new TypeError("hydrated Condition value count does not match request");
  }
  return { ...results, channelResults, subFlowResults };
}

function requireValue(value: Value | undefined, kind: string): Value {
  if (value?.kind === undefined) {
    throw new TypeError(`${kind} is required`);
  }
  return value;
}

function physicalName(name: string, instance: string | undefined): string {
  if (instance === undefined || instance === "") {
    throw new TypeError(`map definition ${name} requires an instance`);
  }
  return `${name}/${encodeURIComponent(instance).replace(/[!'()*]/g, (character) =>
    `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
  )}`;
}

function seconds(milliseconds: number | undefined): number {
  if (milliseconds === undefined) {
    return 0;
  }
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds % 1_000 !== 0) {
    throw new RangeError("duration must be a non-negative whole number of seconds");
  }
  return milliseconds / 1_000;
}

function invalidStepResult(
  flowType: string,
  stepType: string | undefined,
  method: "waitFor" | "execute" | "rpc",
  failure: unknown,
): Error {
  if (failure instanceof ValueMappingError) {
    return failure;
  }
  if (failure instanceof InvalidStepResultError) {
    return failure;
  }
  const detail = failure instanceof Error ? failure.message : String(failure);
  return new InvalidStepResultError(flowType, stepType, method, detail, { cause: failure });
}
