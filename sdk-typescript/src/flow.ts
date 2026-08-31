// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { AttributeMap, IndexType, type Attribute, type PersistenceSchema } from "./persistence.js";
import { IndexType as ProtoIndexType } from "./gen/dex.js";
import { FlowDefinitionError } from "./errors.js";
import { registeredRPCs, type RegisteredRPC } from "./rpc.js";
import { StepList, type Step, type StepClass, type StepDecision } from "./step.js";
import { requireName } from "./validation.js";
import type { Channel, ChannelMap } from "./wait.js";
import type { Context } from "./context.js";
import type { Stream } from "./stream.js";

/**
 * Defines one durable application Flow and its registered API surface.
 * @typeParam StartInput - Value accepted by the optional starting Step.
 */
export interface Flow<StartInput = void> {
  /**
   * Returns the protocol Flow type.
   * @returns A non-empty Flow type unique within the Registry.
   */
  getFlowType(): string;
  /**
   * Returns this Flow's Step definitions.
   * @returns Zero or one starting Step plus all other registered Steps.
   */
  getSteps(): StepList<StartInput>;
  /**
   * Returns this Flow's persistence definitions.
   * @returns Attributes and Channels owned by this Flow. Flows without this method have none.
   */
  getPersistenceSchema?(): PersistenceSchema;
  /**
   * Handles expiration of this Flow's durable soft-timeout timer.
   *
   * When present, a positive timeout defaults to the handler policy. Dex awaits the result and
   * applies it with normal Step Execute validation. The hook runs at most once and may transition,
   * dead-end, complete, fail, or request graceful completion.
   * @param context - Timeout invocation Context; it must not be retained.
   * @returns A StepDecision, synchronously or asynchronously.
   */
  handleTimeout?(context: Context): StepDecision | Promise<StepDecision>;
}

/**
 * Validates and stores Flow definitions shared by Client and Worker.
 * Construction checks names, Step/RPC signatures, persistence definitions, locks,
 * and Attribute indexes atomically.
 *
 * @example
 * ```ts
 * const orders = new OrdersFlow();
 * const registry = new Registry([orders]);
 * ```
 */
export class Registry {
  /** Registered Flow instances in input order. */
  public readonly flows: readonly Flow<any>[];

  /**
   * Creates a Registry.
   * @param flows - Flow instances with unique Flow types.
   * @throws {@link FlowDefinitionError} when any public definition is invalid.
   */
  public constructor(flows: readonly Flow<any>[]) {
    const flowsByInstance = new Map<Flow<any>, RegisteredFlow>();
    const flowsByName = new Map<string, RegisteredFlow>();
    const rpcsByMethod = new Map<Function, RegisteredRPC>();
    const streamsByInstance = new Map<Stream<unknown>, RegisteredFlow>();
    const flowNames = new Set<string>();
    const attributeIndexes = new Map<string, ProtoIndexType>();
    for (const flow of flows) {
      const name = flowType(flow);
      if (flowNames.has(name)) {
        throw new FlowDefinitionError(`duplicate Flow ${name}`);
      }
      flowNames.add(name);
      const registered = registerFlow(flow, name, attributeIndexes);
      flowsByInstance.set(flow, registered);
      flowsByName.set(name, registered);
      for (const stream of registered.streams) {
        if (streamsByInstance.has(stream)) {
          throw new FlowDefinitionError(`Stream ${stream.name} is registered by multiple Flows`);
        }
        streamsByInstance.set(stream, registered);
      }
      for (const rpc of registered.rpcs) {
        if (rpcsByMethod.has(rpc.method)) {
          throw new FlowDefinitionError(`Flow ${name} RPC method ${rpc.name} is registered by multiple Flows`);
        }
        rpcsByMethod.set(rpc.method, rpc);
      }
    }
    this.flows = Object.freeze([...flows]);
    registryMetadata.set(this, {
      flowsByInstance,
      flowsByName,
      rpcsByMethod,
      streamsByInstance,
      attributeIndexes,
    });
  }
}

export interface RegisteredStep {
  readonly name: string;
  readonly step: Step<any>;
  readonly isStartStep: boolean;
}

export interface RegisteredFlow {
  readonly name: string;
  readonly flow: Flow<any>;
  readonly hasTimeoutHandler: boolean;
  readonly steps: readonly RegisteredStep[];
  readonly stepsByClass: ReadonlyMap<StepClass<any>, RegisteredStep>;
  readonly startStep?: RegisteredStep;
  readonly rpcs: readonly RegisteredRPC[];
  readonly persistence: ReadonlyMap<
    string,
    Attribute<unknown> | AttributeMap<unknown> | Channel<unknown> | ChannelMap<unknown> | Stream<unknown>
  >;
  readonly streams: ReadonlySet<Stream<unknown>>;
}

interface RegistryMetadata {
  readonly flowsByInstance: ReadonlyMap<Flow<any>, RegisteredFlow>;
  readonly flowsByName: ReadonlyMap<string, RegisteredFlow>;
  readonly rpcsByMethod: ReadonlyMap<Function, RegisteredRPC>;
  readonly streamsByInstance: ReadonlyMap<Stream<unknown>, RegisteredFlow>;
  readonly attributeIndexes: ReadonlyMap<string, ProtoIndexType>;
}

const registryMetadata = new WeakMap<Registry, RegistryMetadata>();

export function registeredFlow(registry: Registry, flow: Flow<any>): RegisteredFlow {
  const registered = metadata(registry).flowsByInstance.get(flow);
  if (registered === undefined) {
    throw new FlowDefinitionError("Flow instance is not registered");
  }
  return registered;
}

export function registeredRPC(registry: Registry, method: Function): RegisteredRPC {
  const registered = metadata(registry).rpcsByMethod.get(method);
  if (registered === undefined) {
    throw new FlowDefinitionError("RPC method is not registered");
  }
  return registered;
}

export function registeredFlowByName(registry: Registry, name: string): RegisteredFlow {
  const registered = metadata(registry).flowsByName.get(name);
  if (registered === undefined) {
    throw new FlowDefinitionError(`Flow is not registered: ${name}`);
  }
  return registered;
}

export function registeredStep(flow: RegisteredFlow, name: string): RegisteredStep {
  const registered = flow.steps.find((step) => step.name === name);
  if (registered === undefined) {
    throw new FlowDefinitionError(`Flow ${flow.name} Step is not registered: ${name}`);
  }
  return registered;
}

export function registeredRPCByName(flow: RegisteredFlow, name: string): RegisteredRPC {
  const registered = flow.rpcs.find((rpc) => rpc.name === name);
  if (registered === undefined) {
    throw new FlowDefinitionError(`Flow ${flow.name} RPC is not registered: ${name}`);
  }
  return registered;
}

export function registeredAttributeIndexes(
  registry: Registry,
): ReadonlyMap<string, ProtoIndexType> {
  return metadata(registry).attributeIndexes;
}

export function registeredStream(
  registry: Registry,
  stream: Stream<unknown>,
): RegisteredFlow {
  const flow = metadata(registry).streamsByInstance.get(stream);
  if (flow === undefined) {
    throw new FlowDefinitionError(`Stream is not registered: ${stream.name}`);
  }
  return flow;
}

export function requireFlowStream(flow: RegisteredFlow, stream: Stream<unknown>): void {
  if (!flow.streams.has(stream)) {
    throw new FlowDefinitionError(`Flow ${flow.name} does not register Stream ${stream.name}`);
  }
}

function metadata(registry: Registry): RegistryMetadata {
  const value = registryMetadata.get(registry);
  if (value === undefined) {
    throw new FlowDefinitionError("Registry was not initialized by the Dex SDK");
  }
  return value;
}

function flowType(flow: Flow<any>): string {
  try {
    if (typeof flow.getFlowType !== "function") {
      throw new FlowDefinitionError("Flow must implement getFlowType");
    }
    const name = flow.getFlowType();
    requireName(name);
    return name;
  } catch (failure) {
    throw definitionError("Flow registration failed", failure);
  }
}

function stepType(flowName: string, step: Step<any>): string {
  if (typeof step.getStepType !== "function") {
    throw new FlowDefinitionError(`Flow ${flowName} Step must implement getStepType`);
  }
  const name = step.getStepType();
  requireName(name);
  return name;
}

function registerFlow(
  flow: Flow<any>,
  name: string,
  attributeIndexes: Map<string, ProtoIndexType>,
): RegisteredFlow {
  try {
    return doRegisterFlow(flow, name, attributeIndexes);
  } catch (failure) {
    throw definitionError(`Flow ${name} registration failed`, failure);
  }
}

function doRegisterFlow(
  flow: Flow<any>,
  name: string,
  attributeIndexes: Map<string, ProtoIndexType>,
): RegisteredFlow {
  const stepDefinitions = flow.getSteps();
  if (!(stepDefinitions instanceof StepList)) {
    throw new FlowDefinitionError(`Flow ${name} steps must be a StepList`);
  }
  const stepNames = new Set<string>();
  const steps: RegisteredStep[] = [];
  const stepsByClass = new Map<StepClass<any>, RegisteredStep>();
  let startStep: RegisteredStep | undefined;
  let hasStartStep = false;
  for (const definition of stepDefinitions) {
    if (definition.isStartStep) {
      if (hasStartStep) {
        throw new FlowDefinitionError(`Flow ${name} must not have multiple start Steps`);
      }
      hasStartStep = true;
    }
    const step = definition.step;
    const stepClass = registeredStepClass(step);
    const stepName = stepType(name, step);
    if (stepNames.has(stepName)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate Step ${stepName}`);
    }
    if (stepsByClass.has(stepClass)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate Step class ${stepClass.name}`);
    }
    stepNames.add(stepName);
    const registered = { name: stepName, step, isStartStep: definition.isStartStep };
    steps.push(registered);
    stepsByClass.set(stepClass, registered);
    if (definition.isStartStep) {
      startStep = registered;
    }
  }

  const schema = flow.getPersistenceSchema?.() ?? {};
  const persistenceDefinitions = [
    ...(schema.attributes ?? []),
    ...(schema.channels ?? []),
    ...(schema.streams ?? []),
  ];
  const persistence = new Map<string, (typeof persistenceDefinitions)[number]>();
  for (const definition of persistenceDefinitions) {
    if (persistence.has(definition.name)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate persistence definition ${definition.name}`);
    }
    persistence.set(definition.name, definition);
    if ("index" in definition && definition.index !== undefined) {
      const isMap = definition instanceof AttributeMap;
      if (isMap && (definition.index.indexKey === undefined || definition.index.indexKey.length === 0)) {
        throw new FlowDefinitionError(`Flow ${name} indexed AttributeMap ${definition.name} requires an index key`);
      }
      const key = definition.index.indexKey ?? definition.name;
      const type = protoIndexType(definition.index.type);
      const existing = attributeIndexes.get(key);
      if (existing !== undefined && existing !== type) {
        throw new FlowDefinitionError(`Attribute index ${key} has conflicting types ${existing} and ${type}`);
      }
      attributeIndexes.set(key, type);
    }
  }
  const rpcNames = new Set<string>();
  const attributes = new Set(schema.attributes ?? []);
  const rpcs = registeredRPCs(flow);
  for (const registeredRPC of rpcs) {
    if (rpcNames.has(registeredRPC.name)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate RPC ${registeredRPC.name}`);
    }
    rpcNames.add(registeredRPC.name);
    if (
      registeredRPC.options.lockAttributes?.some((lock) => !attributes.has(lock.attribute)) === true
    ) {
      throw new FlowDefinitionError(`Flow ${name} RPC ${registeredRPC.name} locks an unregistered attribute`);
    }
  }
  return Object.freeze({
    name,
    flow,
    hasTimeoutHandler: typeof flow.handleTimeout === "function",
    steps: Object.freeze(steps),
    stepsByClass,
    ...(startStep === undefined ? {} : { startStep }),
    rpcs,
    persistence,
    streams: new Set(schema.streams ?? []),
  });
}

function registeredStepClass<Input>(step: Step<Input>): StepClass<Input> {
  if (step.constructor === Object) {
    throw new FlowDefinitionError("Step must be a class instance");
  }
  return step.constructor as StepClass<Input>;
}

function protoIndexType(indexType: IndexType): ProtoIndexType {
  return {
    [IndexType.KEYWORD]: ProtoIndexType.INDEX_TYPE_KEYWORD,
    [IndexType.FULL_TEXT]: ProtoIndexType.INDEX_TYPE_TEXT,
    [IndexType.KEYWORD_ARRAY]: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
    [IndexType.INT]: ProtoIndexType.INDEX_TYPE_INT,
    [IndexType.DOUBLE]: ProtoIndexType.INDEX_TYPE_DOUBLE,
    [IndexType.BOOL]: ProtoIndexType.INDEX_TYPE_BOOL,
    [IndexType.DATETIME]: ProtoIndexType.INDEX_TYPE_DATETIME,
  }[indexType];
}

function definitionError(context: string, failure: unknown): FlowDefinitionError {
  if (failure instanceof FlowDefinitionError) {
    return failure;
  }
  const detail = failure instanceof Error ? failure.message : String(failure);
  return new FlowDefinitionError(`${context}: ${detail}`, { cause: failure });
}
