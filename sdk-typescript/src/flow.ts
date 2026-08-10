// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Attribute, AttributeMap, PersistenceSchema } from "./persistence.js";
import { FlowDefinitionError } from "./errors.js";
import { registeredRPCs, type RegisteredRPC } from "./rpc.js";
import { StepList, type Step } from "./step.js";
import { requireName } from "./validation.js";
import type { Channel, ChannelMap } from "./wait.js";

export interface Flow<StartInput = void> {
  getFlowType(): string;
  getSteps(): StepList<StartInput>;
  getPersistenceSchema?(): PersistenceSchema;
}

export class Registry {
  public readonly flows: readonly Flow<any>[];

  public constructor(flows: readonly Flow<any>[]) {
    const flowsByInstance = new Map<Flow<any>, RegisteredFlow>();
    const flowsByName = new Map<string, RegisteredFlow>();
    const rpcsByMethod = new Map<Function, RegisteredRPC>();
    const flowNames = new Set<string>();
    for (const flow of flows) {
      const name = flowType(flow);
      if (flowNames.has(name)) {
        throw new FlowDefinitionError(`duplicate Flow ${name}`);
      }
      flowNames.add(name);
      const registered = registerFlow(flow, name);
      flowsByInstance.set(flow, registered);
      flowsByName.set(name, registered);
      for (const rpc of registered.rpcs) {
        if (rpcsByMethod.has(rpc.method)) {
          throw new FlowDefinitionError(`Flow ${name} RPC method ${rpc.name} is registered by multiple Flows`);
        }
        rpcsByMethod.set(rpc.method, rpc);
      }
    }
    this.flows = Object.freeze([...flows]);
    registryMetadata.set(this, { flowsByInstance, flowsByName, rpcsByMethod });
  }
}

export interface RegisteredStep {
  readonly name: string;
  readonly step: Step<unknown>;
  readonly isStartStep: boolean;
}

export interface RegisteredFlow {
  readonly name: string;
  readonly flow: Flow<any>;
  readonly steps: readonly RegisteredStep[];
  readonly startStep?: RegisteredStep;
  readonly rpcs: readonly RegisteredRPC[];
  readonly persistence: ReadonlyMap<
    string,
    Attribute<unknown> | AttributeMap<unknown> | Channel<unknown> | ChannelMap<unknown>
  >;
}

interface RegistryMetadata {
  readonly flowsByInstance: ReadonlyMap<Flow<any>, RegisteredFlow>;
  readonly flowsByName: ReadonlyMap<string, RegisteredFlow>;
  readonly rpcsByMethod: ReadonlyMap<Function, RegisteredRPC>;
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

function stepType(flowName: string, step: Step<unknown>): string {
  if (typeof step.getStepType !== "function") {
    throw new FlowDefinitionError(`Flow ${flowName} Step must implement getStepType`);
  }
  const name = step.getStepType();
  requireName(name);
  return name;
}

function registerFlow(flow: Flow<any>, name: string): RegisteredFlow {
  try {
    return doRegisterFlow(flow, name);
  } catch (failure) {
    throw definitionError(`Flow ${name} registration failed`, failure);
  }
}

function doRegisterFlow(flow: Flow<any>, name: string): RegisteredFlow {
  const stepDefinitions = flow.getSteps();
  if (!(stepDefinitions instanceof StepList)) {
    throw new FlowDefinitionError(`Flow ${name} steps must be a StepList`);
  }
  const stepNames = new Set<string>();
  const steps: RegisteredStep[] = [];
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
    const stepName = stepType(name, step);
    if (stepNames.has(stepName)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate Step ${stepName}`);
    }
    stepNames.add(stepName);
    const registered = { name: stepName, step, isStartStep: definition.isStartStep };
    steps.push(registered);
    if (definition.isStartStep) {
      startStep = registered;
    }
  }

  const schema = flow.getPersistenceSchema?.() ?? {};
  const persistenceDefinitions = [...(schema.attributes ?? []), ...(schema.channels ?? [])];
  const persistence = new Map<string, (typeof persistenceDefinitions)[number]>();
  for (const definition of persistenceDefinitions) {
    if (persistence.has(definition.name)) {
      throw new FlowDefinitionError(`Flow ${name} has duplicate persistence definition ${definition.name}`);
    }
    persistence.set(definition.name, definition);
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
    steps: Object.freeze(steps),
    ...(startStep === undefined ? {} : { startStep }),
    rpcs,
    persistence,
  });
}

function definitionError(context: string, failure: unknown): FlowDefinitionError {
  if (failure instanceof FlowDefinitionError) {
    return failure;
  }
  const detail = failure instanceof Error ? failure.message : String(failure);
  return new FlowDefinitionError(`${context}: ${detail}`, { cause: failure });
}
