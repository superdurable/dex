// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { PersistenceSchema } from "./persistence.js";
import { registeredRPCs, type RegisteredRPC } from "./rpc.js";
import { StepList, type Step } from "./step.js";
import { requireName } from "./validation.js";

export interface Flow<StartInput = void> {
  getFlowType(): string;
  getSteps(): StepList<StartInput>;
  getPersistenceSchema?(): PersistenceSchema;
}

export class Registry {
  public readonly flows: readonly Flow<any>[];

  public constructor(flows: readonly Flow<any>[]) {
    const flowsByInstance = new Map<Flow<any>, RegisteredFlow>();
    const rpcsByMethod = new Map<Function, RegisteredRPC>();
    const flowNames = new Set<string>();
    for (const flow of flows) {
      const name = flowType(flow);
      if (flowNames.has(name)) {
        throw new TypeError(`duplicate Flow ${name}`);
      }
      flowNames.add(name);
      const registered = registerFlow(flow, name);
      flowsByInstance.set(flow, registered);
      for (const rpc of registered.rpcs) {
        if (rpcsByMethod.has(rpc.method)) {
          throw new TypeError(`RPC method ${rpc.name} is registered by multiple Flows`);
        }
        rpcsByMethod.set(rpc.method, rpc);
      }
    }
    this.flows = Object.freeze([...flows]);
    registryMetadata.set(this, { flowsByInstance, rpcsByMethod });
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
}

interface RegistryMetadata {
  readonly flowsByInstance: ReadonlyMap<Flow<any>, RegisteredFlow>;
  readonly rpcsByMethod: ReadonlyMap<Function, RegisteredRPC>;
}

const registryMetadata = new WeakMap<Registry, RegistryMetadata>();

export function registeredFlow(registry: Registry, flow: Flow<any>): RegisteredFlow {
  const registered = metadata(registry).flowsByInstance.get(flow);
  if (registered === undefined) {
    throw new TypeError("Flow instance is not registered");
  }
  return registered;
}

export function registeredRPC(registry: Registry, method: Function): RegisteredRPC {
  const registered = metadata(registry).rpcsByMethod.get(method);
  if (registered === undefined) {
    throw new TypeError("RPC method is not registered");
  }
  return registered;
}

function metadata(registry: Registry): RegistryMetadata {
  const value = registryMetadata.get(registry);
  if (value === undefined) {
    throw new TypeError("Registry was not initialized by the Dex SDK");
  }
  return value;
}

function flowType(flow: Flow<any>): string {
  if (typeof flow.getFlowType !== "function") {
    throw new TypeError("Flow must implement getFlowType");
  }
  const name = flow.getFlowType();
  requireName(name);
  return name;
}

function stepType(step: Step<unknown>): string {
  if (typeof step.getStepType !== "function") {
    throw new TypeError("Step must implement getStepType");
  }
  const name = step.getStepType();
  requireName(name);
  return name;
}

function registerFlow(flow: Flow<any>, name: string): RegisteredFlow {
  const definitions = flow.getSteps();
  if (!(definitions instanceof StepList)) {
    throw new TypeError("Flow steps must be a StepList");
  }
  const stepNames = new Set<string>();
  const steps: RegisteredStep[] = [];
  let startStep: RegisteredStep | undefined;
  let hasStartStep = false;
  for (const definition of definitions) {
    if (definition.isStartStep) {
      if (hasStartStep) {
        throw new TypeError("Flow must not have multiple start Steps");
      }
      hasStartStep = true;
    }
    const step = definition.step;
    const name = stepType(step);
    if (stepNames.has(name)) {
      throw new TypeError(`duplicate Step ${name}`);
    }
    stepNames.add(name);
    const registered = { name, step, isStartStep: definition.isStartStep };
    steps.push(registered);
    if (definition.isStartStep) {
      startStep = registered;
    }
  }

  const rpcNames = new Set<string>();
  const attributes = new Set(flow.getPersistenceSchema?.().attributes ?? []);
  const rpcs = registeredRPCs(flow);
  for (const registeredRPC of rpcs) {
    if (rpcNames.has(registeredRPC.name)) {
      throw new TypeError(`duplicate RPC ${registeredRPC.name}`);
    }
    rpcNames.add(registeredRPC.name);
    if (
      registeredRPC.options.lockAttributes?.some((lock) => !attributes.has(lock.attribute)) === true
    ) {
      throw new TypeError(`RPC ${registeredRPC.name} locks an unregistered attribute`);
    }
  }
  return Object.freeze({
    name,
    flow,
    steps: Object.freeze(steps),
    ...(startStep === undefined ? {} : { startStep }),
    rpcs,
  });
}
